// Package questbulk prepares offline PostgreSQL scripts for registering an
// explicit quest roster and enabling its legacy dates. It never opens a DB.
package questbulk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const Schema = "erupe-quest-bulk-plan-v1"

type Source struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Existing is an exact raw database row, before COALESCE. Nil is SQL NULL.
type Existing struct {
	ID           int64     `json:"id"`
	QuestID      int       `json:"quest_id"`
	MaxPlayers   *int      `json:"max_players"`
	QuestType    int       `json:"quest_type"`
	Mark         *int      `json:"mark"`
	Flags        *int      `json:"flags"`
	StartTime    time.Time `json:"start_time"`
	ActiveDays   *int      `json:"active_days"`
	InactiveDays *int      `json:"inactive_days"`
	CollabScope  *string   `json:"collab_scope"`
}

// Addition contains listing metadata, not the binary's differently numbered
// quest_type_id_raw. Evidence must identify the source of these listing fields.
type Addition struct {
	QuestID     int    `json:"quest_id"`
	MaxPlayers  int    `json:"max_players"`
	QuestType   int    `json:"quest_type"`
	Mark        int    `json:"mark"`
	Flags       int    `json:"flags"`
	CollabScope string `json:"collab_scope"`
	Evidence    Source `json:"evidence"`
}

type Plan struct {
	Schema           string     `json:"schema"`
	BatchID          string     `json:"batch_id"`
	Capacity         int        `json:"capacity"`
	SnapshotSource   Source     `json:"snapshot_source"`
	Existing         []Existing `json:"expected_existing"`
	Additions        []Addition `json:"additions"`
	ActivateQuestIDs []int      `json:"activate_quest_ids"`
}

type Bundle struct {
	PlanSHA256  string
	BackupTable string
	ApplySQL    string
	RehearseSQL string
	RollbackSQL string
}

var batchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const customMutationGuardSQL = "DO $bulk$ BEGIN IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='public.event_quests'::regclass AND NOT tgisinternal AND tgenabled<>'D') OR EXISTS (SELECT 1 FROM pg_rewrite WHERE ev_class='public.event_quests'::regclass AND rulename<>'_RETURN') THEN RAISE EXCEPTION 'Review custom event_quests triggers/rules before using this batch'; END IF; END $bulk$;\n"

func validSource(s Source) bool {
	return strings.TrimSpace(s.Path) != "" && s.Bytes > 0 && hashPattern.MatchString(s.SHA256)
}

func validScope(s string) bool {
	switch s {
	case "", "kaiji", "higanjima", "nier", "evangelion", "pso2":
		return true
	}
	return false
}

// Parse rejects unknown, duplicate, missing and trailing JSON fields. Even NULL
// fields on existing rows must be explicitly present in the raw snapshot.
func Parse(data []byte) (Plan, error) {
	if len(data) > 16<<20 {
		return Plan{}, fmt.Errorf("plan exceeds 16 MiB")
	}
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return Plan{}, err
	}
	var p Plan
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return Plan{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Plan{}, fmt.Errorf("trailing JSON")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Plan{}, err
	}
	if len(raw) != 7 {
		return Plan{}, fmt.Errorf("plan fields must use exact canonical names")
	}
	for _, k := range []string{"schema", "batch_id", "capacity", "snapshot_source", "expected_existing", "additions", "activate_quest_ids"} {
		if v, ok := raw[k]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return Plan{}, fmt.Errorf("missing/non-null field %s", k)
		}
	}
	for key, fields := range map[string][]string{
		"expected_existing": {"id", "quest_id", "max_players", "quest_type", "mark", "flags", "start_time", "active_days", "inactive_days", "collab_scope"},
		"additions":         {"quest_id", "max_players", "quest_type", "mark", "flags", "collab_scope", "evidence"},
	} {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw[key], &rows); err != nil {
			return Plan{}, err
		}
		for n, row := range rows {
			if len(row) != len(fields) {
				return Plan{}, fmt.Errorf("%s[%d] fields must use exact canonical names", key, n)
			}
			for _, field := range fields {
				value, ok := row[field]
				if !ok {
					return Plan{}, fmt.Errorf("%s[%d] missing %s", key, n, field)
				}
				nullable := key == "expected_existing" && (field == "max_players" || field == "mark" || field == "flags" || field == "active_days" || field == "inactive_days" || field == "collab_scope")
				if !nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return Plan{}, fmt.Errorf("%s[%d].%s cannot be null", key, n, field)
				}
			}
			if key == "additions" {
				if err := checkSourceObject(row["evidence"]); err != nil {
					return Plan{}, err
				}
			}
		}
	}
	if err := checkSourceObject(raw["snapshot_source"]); err != nil {
		return Plan{}, err
	}
	return p, p.Validate()
}

func checkSourceObject(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 3 {
		return fmt.Errorf("source needs exact path/bytes/sha256 fields")
	}
	for _, name := range []string{"path", "bytes", "sha256"} {
		v, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("source field %s required", name)
		}
	}
	return nil
}

func rejectDuplicateKeys(d *json.Decoder) error {
	var walk func() error
	walk = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					if err != nil {
						return err
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return fmt.Errorf("duplicate/invalid JSON key %v", k)
					}
					seen[s] = true
					if err := walk(); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unexpected JSON delimiter")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func (p Plan) Validate() error {
	if p.Schema != Schema || !batchPattern.MatchString(p.BatchID) || !validSource(p.SnapshotSource) {
		return fmt.Errorf("invalid schema, batch ID or snapshot provenance")
	}
	if p.Capacity < 513 || p.Capacity > 65535 || len(p.ActivateQuestIDs) == 0 || len(p.ActivateQuestIDs) > p.Capacity {
		return fmt.Errorf("roster requires expanded capacity 513..65535 and cannot be empty/exceed capacity")
	}
	targets := map[int]bool{}
	covered := map[int]bool{}
	rowIDs := map[int64]bool{}
	for _, i := range p.ActivateQuestIDs {
		if i < 1 || i > 65535 || targets[i] {
			return fmt.Errorf("invalid/duplicate activation ID %d", i)
		}
		targets[i] = true
	}
	for _, r := range p.Existing {
		if r.ID < 1 || r.ID > 2147483647 || rowIDs[r.ID] || !targets[r.QuestID] || covered[r.QuestID] || r.QuestType < 0 || r.QuestType > 255 || r.StartTime.IsZero() || r.StartTime.Year() < 1 || r.StartTime.Year() > 9999 {
			return fmt.Errorf("invalid/duplicate existing row %d/%d", r.ID, r.QuestID)
		}
		if r.MaxPlayers != nil && (*r.MaxPlayers < 0 || *r.MaxPlayers > 255) || r.Mark != nil && (*r.Mark < 0 || int64(*r.Mark) > 2147483647) || r.Flags != nil && (*r.Flags < -1 || *r.Flags > 255) || r.CollabScope != nil && !validScope(*r.CollabScope) {
			return fmt.Errorf("invalid existing listing metadata %d", r.QuestID)
		}
		for _, v := range []*int{r.ActiveDays, r.InactiveDays} {
			if v != nil && (int64(*v) < -2147483648 || int64(*v) > 2147483647) {
				return fmt.Errorf("date value outside PostgreSQL int")
			}
		}
		rowIDs[r.ID] = true
		covered[r.QuestID] = true
	}
	for _, r := range p.Additions {
		if !targets[r.QuestID] || covered[r.QuestID] || r.QuestType < 0 || r.QuestType > 255 || r.MaxPlayers < 0 || r.MaxPlayers > 255 || r.Mark < 0 || int64(r.Mark) > 2147483647 || r.Flags < -1 || r.Flags > 255 || !validScope(r.CollabScope) || !validSource(r.Evidence) {
			return fmt.Errorf("invalid/duplicate addition or missing listing evidence %d", r.QuestID)
		}
		covered[r.QuestID] = true
	}
	if len(covered) != len(targets) {
		return fmt.Errorf("each activation ID needs exactly one expected row or addition")
	}
	return nil
}

func integer(v *int) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprint(*v)
}
func quoted(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
func scope(v *string) string {
	if v == nil {
		return "NULL"
	}
	return quoted(*v)
}

func Build(p Plan) (Bundle, error) {
	if err := p.Validate(); err != nil {
		return Bundle{}, err
	}
	// Canonical sorted copies ensure that caller ordering does not change identity.
	p.Existing = append([]Existing(nil), p.Existing...)
	p.Additions = append([]Addition(nil), p.Additions...)
	p.ActivateQuestIDs = append([]int(nil), p.ActivateQuestIDs...)
	sort.Slice(p.Existing, func(i, j int) bool { return p.Existing[i].QuestID < p.Existing[j].QuestID })
	sort.Slice(p.Additions, func(i, j int) bool { return p.Additions[i].QuestID < p.Additions[j].QuestID })
	sort.Ints(p.ActivateQuestIDs)
	canonical, err := json.Marshal(p)
	if err != nil {
		return Bundle{}, err
	}
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	table := "public.quest_bulk_" + hash[:20]
	var b strings.Builder
	fmt.Fprintf(&b, "-- %s / plan SHA256 %s\n-- Explicit roster only; original bodies, eligibility, rewards and history are unchanged.\nBEGIN;\nSET LOCAL lock_timeout = '5s';\nSET LOCAL statement_timeout = '120s';\nSET LOCAL TIME ZONE 'UTC';\nLOCK TABLE public.event_quests IN SHARE ROW EXCLUSIVE MODE;\n", p.BatchID, hash)
	fmt.Fprintf(&b, "DO $bulk$ BEGIN IF to_regclass('%s') IS NOT NULL THEN RAISE EXCEPTION 'This batch already has a backup; do not reapply'; END IF; END $bulk$;\n", table)
	b.WriteString(customMutationGuardSQL)
	b.WriteString("CREATE TEMP TABLE qb_expected (id integer,quest_id integer,max_players integer,quest_type integer,mark integer,flags integer,start_time timestamptz,active_days integer,inactive_days integer,collab_scope text) ON COMMIT DROP;\nCREATE TEMP TABLE qb_targets (quest_id integer PRIMARY KEY) ON COMMIT DROP;\n")
	b.WriteString("INSERT INTO qb_targets VALUES ")
	for i, id := range p.ActivateQuestIDs {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "(%d)", id)
	}
	b.WriteString(";\n")
	if len(p.Existing) > 0 {
		b.WriteString("INSERT INTO qb_expected (id,quest_id,max_players,quest_type,mark,flags,start_time,active_days,inactive_days,collab_scope) VALUES\n")
		for n, r := range p.Existing {
			if n > 0 {
				b.WriteString(",\n")
			}
			fmt.Fprintf(&b, "(%d,%d,%s,%d,%s,%s,%s::timestamptz,%s,%s,%s)", r.ID, r.QuestID, integer(r.MaxPlayers), r.QuestType, integer(r.Mark), integer(r.Flags), quoted(r.StartTime.UTC().Format(time.RFC3339Nano)), integer(r.ActiveDays), integer(r.InactiveDays), scope(r.CollabScope))
		}
		b.WriteString(";\n")
	}
	const columns = "id,quest_id,max_players,quest_type,mark,flags,start_time,active_days,inactive_days,collab_scope"
	fmt.Fprintf(&b, "DO $bulk$ BEGIN IF EXISTS ((SELECT %s FROM public.event_quests WHERE quest_id IN (SELECT quest_id FROM qb_targets) EXCEPT ALL SELECT %s FROM qb_expected) UNION ALL (SELECT %s FROM qb_expected EXCEPT ALL SELECT %s FROM public.event_quests WHERE quest_id IN (SELECT quest_id FROM qb_targets))) THEN RAISE EXCEPTION 'Target rows changed or are duplicated; refresh the raw snapshot'; END IF; END $bulk$;\n", columns, columns, columns, columns)
	fmt.Fprintf(&b, "CREATE TABLE %s (quest_id integer PRIMARY KEY, plan_sha256 text NOT NULL, before_row jsonb, after_row jsonb, rolled_back boolean NOT NULL DEFAULT false);\nINSERT INTO %s (quest_id,plan_sha256,before_row) SELECT t.quest_id,%s,to_jsonb(d) FROM qb_targets t LEFT JOIN public.event_quests d ON d.quest_id=t.quest_id;\n", table, table, quoted(hash))
	b.WriteString("UPDATE public.event_quests SET start_time=CURRENT_TIMESTAMP,active_days=0,inactive_days=0 WHERE quest_id IN (SELECT quest_id FROM qb_targets);\n")
	if len(p.Additions) > 0 {
		// Keep the addition data in a temp table so rehearsal can use temporary
		// explicit IDs: PostgreSQL nextval is NOT undone by transaction rollback.
		b.WriteString("CREATE TEMP TABLE qb_additions (max_players integer,quest_type integer,quest_id integer,mark integer,flags integer,collab_scope text) ON COMMIT DROP;\nINSERT INTO qb_additions VALUES\n")
		for n, r := range p.Additions {
			if n > 0 {
				b.WriteString(",\n")
			}
			fmt.Fprintf(&b, "(%d,%d,%d,%d,%d,%s)", r.MaxPlayers, r.QuestType, r.QuestID, r.Mark, r.Flags, quoted(r.CollabScope))
		}
		b.WriteString(";\n")
		b.WriteString("-- QB_ADDITION_INSERT\n")
	}
	fmt.Fprintf(&b, "UPDATE %s b SET after_row=to_jsonb(d) FROM public.event_quests d WHERE d.quest_id=b.quest_id;\nDO $bulk$ BEGIN IF (SELECT count(*) FROM public.event_quests WHERE quest_id IN (SELECT quest_id FROM qb_targets)) <> %d OR EXISTS (SELECT 1 FROM %s WHERE after_row IS NULL OR (after_row->>'active_days')::integer<>0 OR (after_row->>'inactive_days')::integer<>0) THEN RAISE EXCEPTION 'Incomplete bulk activation'; END IF; END $bulk$;\n", table, len(p.ActivateQuestIDs), table)
	fmt.Fprintf(&b, "DO $bulk$ BEGIN IF EXISTS (SELECT 1 FROM %s WHERE (after_row->>'start_time')::timestamptz IS DISTINCT FROM CURRENT_TIMESTAMP OR (before_row IS NOT NULL AND (after_row-'start_time'-'active_days'-'inactive_days') IS DISTINCT FROM (before_row-'start_time'-'active_days'-'inactive_days'))) THEN RAISE EXCEPTION 'Original row metadata or activation timestamp changed unexpectedly'; END IF; END $bulk$;\n", table)
	body := b.String()
	applyInsert := ""
	rehearseInsert := ""
	if len(p.Additions) > 0 {
		applyInsert = "DO $bulk$ DECLARE seq regclass; last_id bigint; called boolean; step bigint; BEGIN seq:=pg_get_serial_sequence('public.event_quests','id')::regclass; IF seq IS NULL THEN RAISE EXCEPTION 'event_quests.id needs its existing serial sequence'; END IF; SELECT seqincrement INTO step FROM pg_sequence WHERE seqrelid=seq; IF step<>1 THEN RAISE EXCEPTION 'Unexpected event row sequence increment'; END IF; EXECUTE format('SELECT last_value,is_called FROM %s',seq) INTO last_id,called; IF (last_id+(CASE WHEN called THEN 1 ELSE 0 END)) <= COALESCE((SELECT max(id) FROM public.event_quests),0) OR last_id+(SELECT count(*) FROM qb_additions)>2147483647 THEN RAISE EXCEPTION 'Event row sequence is behind or exhausted; repair it separately'; END IF; END $bulk$;\nINSERT INTO public.event_quests (max_players,quest_type,quest_id,mark,flags,start_time,active_days,inactive_days,collab_scope) SELECT max_players,quest_type,quest_id,mark,flags,CURRENT_TIMESTAMP,0,0,collab_scope FROM qb_additions ORDER BY quest_id;\n"
		rehearseInsert = "-- Rehearsal uses disposable explicit IDs and never calls nextval.\nDO $bulk$ BEGIN IF COALESCE((SELECT max(id)::bigint FROM public.event_quests),0)+(SELECT count(*) FROM qb_additions)>2147483647 THEN RAISE EXCEPTION 'No temporary event row IDs available'; END IF; END $bulk$;\nINSERT INTO public.event_quests (id,max_players,quest_type,quest_id,mark,flags,start_time,active_days,inactive_days,collab_scope) SELECT (COALESCE((SELECT max(id)::bigint FROM public.event_quests),0)+row_number() OVER (ORDER BY quest_id))::integer,max_players,quest_type,quest_id,mark,flags,CURRENT_TIMESTAMP,0,0,collab_scope FROM qb_additions ORDER BY quest_id;\n"
	}
	applyBody := strings.Replace(body, "-- QB_ADDITION_INSERT\n", applyInsert, 1)
	rehearseBody := strings.Replace(body, "-- QB_ADDITION_INSERT\n", rehearseInsert, 1)
	b.Reset()
	fmt.Fprintf(&b, "-- Undo only this exact unchanged batch %s. Backup/history is retained.\nBEGIN;\nSET LOCAL lock_timeout = '5s';\nSET LOCAL statement_timeout = '120s';\nSET LOCAL TIME ZONE 'UTC';\nLOCK TABLE public.event_quests IN SHARE ROW EXCLUSIVE MODE;\nLOCK TABLE %s IN EXCLUSIVE MODE;\n", hash, table)
	b.WriteString(customMutationGuardSQL)
	fmt.Fprintf(&b, "DO $bulk$ BEGIN IF (SELECT count(*) FROM %s)<>%d OR EXISTS (SELECT 1 FROM %s WHERE plan_sha256<>%s OR rolled_back OR after_row IS NULL) OR (SELECT count(*) FROM public.event_quests d JOIN %s b ON b.quest_id=d.quest_id)<>%d OR EXISTS (SELECT 1 FROM %s b LEFT JOIN public.event_quests d ON d.quest_id=b.quest_id WHERE to_jsonb(d) IS DISTINCT FROM b.after_row) THEN RAISE EXCEPTION 'Batch changed, missing, duplicated or already undone; refusing rollback'; END IF; END $bulk$;\n", table, len(p.ActivateQuestIDs), table, quoted(hash), table, len(p.ActivateQuestIDs), table)
	fmt.Fprintf(&b, "DELETE FROM public.event_quests d USING %s b WHERE b.before_row IS NULL AND d.quest_id=b.quest_id;\nUPDATE public.event_quests d SET start_time=old.start_time,active_days=old.active_days,inactive_days=old.inactive_days FROM %s b CROSS JOIN LATERAL jsonb_populate_record(NULL::public.event_quests,b.before_row) old WHERE b.before_row IS NOT NULL AND d.id=old.id;\n", table, table)
	fmt.Fprintf(&b, "DO $bulk$ BEGIN IF EXISTS (SELECT 1 FROM %s b LEFT JOIN public.event_quests d ON d.quest_id=b.quest_id WHERE (b.before_row IS NULL AND d.id IS NOT NULL) OR (b.before_row IS NOT NULL AND to_jsonb(d) IS DISTINCT FROM b.before_row)) THEN RAISE EXCEPTION 'Undo did not restore exact original rows'; END IF; END $bulk$;\nUPDATE %s SET rolled_back=true;\nCOMMIT;\n", table, table)
	return Bundle{PlanSHA256: hash, BackupTable: table, ApplySQL: applyBody + "COMMIT;\n", RehearseSQL: rehearseBody + "ROLLBACK;\n", RollbackSQL: b.String()}, nil
}
