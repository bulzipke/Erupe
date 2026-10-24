package questbulk

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }
func example() Plan {
	s := Source{Path: "snapshot.json", Bytes: 10, SHA256: strings.Repeat("a", 64)}
	return Plan{Schema: Schema, BatchID: "all-quests-local", Capacity: 16384, SnapshotSource: s,
		Existing:  []Existing{{ID: 19, QuestID: 40139, MaxPlayers: nil, QuestType: 9, Mark: nil, Flags: ptr(-1), StartTime: time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.FixedZone("KST", 9*3600)), ActiveDays: ptr(3), InactiveDays: ptr(1), CollabScope: ptr("")}},
		Additions: []Addition{{QuestID: 53065, MaxPlayers: 4, QuestType: 18, Mark: 1, Flags: -1, CollabScope: "", Evidence: s}}, ActivateQuestIDs: []int{53065, 40139}}
}

func TestStrictPlanInput(t *testing.T) {
	data, err := json.Marshal(example())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Existing[0].MaxPlayers != nil || parsed.Existing[0].ID != 19 || parsed.Existing[0].QuestType != 9 {
		t.Fatal("raw null/id/Type9 changed")
	}
	for name, bad := range map[string][]byte{
		"duplicate":                  []byte(strings.Replace(string(data), `"capacity":16384`, `"capacity":16384,"capacity":16384`, 1)),
		"unknown":                    []byte(strings.Replace(string(data), `"capacity":16384`, `"capacity":16384,"bypass_conditions":true`, 1)),
		"missing nullable raw field": []byte(strings.Replace(string(data), `"max_players":null,`, "", 1)),
		"null required addition":     []byte(strings.Replace(string(data), `"max_players":4`, `"max_players":null`, 1)),
		"trailing":                   append(append([]byte{}, data...), []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(bad); err == nil {
				t.Fatal("accepted invalid plan")
			}
		})
	}
}

func TestExactRosterAndMetadataRequired(t *testing.T) {
	for name, alter := range map[string]func(*Plan){
		"stock capacity": func(p *Plan) { p.Capacity = 512 },
		"over capacity": func(p *Plan) {
			p.Capacity = 513
			for i := 1; i <= 514; i++ {
				p.ActivateQuestIDs = append(p.ActivateQuestIDs, i)
			}
		},
		"duplicate quest":         func(p *Plan) { p.ActivateQuestIDs = append(p.ActivateQuestIDs, 40139) },
		"missing row":             func(p *Plan) { p.ActivateQuestIDs = append(p.ActivateQuestIDs, 100) },
		"duplicate existing":      func(p *Plan) { p.Existing = append(p.Existing, p.Existing[0]) },
		"existing plus addition":  func(p *Plan) { p.Additions[0].QuestID = 40139 },
		"body type not wire type": func(p *Plan) { p.Additions[0].QuestType = 1031 },
		"scope injection":         func(p *Plan) { p.Additions[0].CollabScope = "nier';DELETE FROM event_quests;--" },
		"missing evidence":        func(p *Plan) { p.Additions[0].Evidence = Source{} },
		"missing timestamp":       func(p *Plan) { p.Existing[0].StartTime = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			p := example()
			alter(&p)
			if _, err := Build(p); err == nil {
				t.Fatal("accepted invalid roster/metadata")
			}
		})
	}
}

func TestBundlePreservesIDsAndProtectsUndo(t *testing.T) {
	p := example()
	bundle, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bundle.ApplySQL, "UPDATE public.event_quests SET start_time=CURRENT_TIMESTAMP,active_days=0,inactive_days=0") || strings.Contains(bundle.ApplySQL, "DELETE FROM public.event_quests") {
		t.Fatal("apply changes original registrations")
	}
	for _, sql := range []string{bundle.ApplySQL, bundle.RehearseSQL} {
		if !strings.Contains(sql, "EXCEPT ALL") || !strings.Contains(sql, "(19,40139,NULL,9,NULL,-1,'2020-01-01T18:04:05.123456Z'") {
			t.Fatal("snapshot guard lost raw ID/null/type/timestamp")
		}
	}
	if strings.Contains(bundle.RehearseSQL, "pg_get_serial_sequence") || strings.Contains(bundle.RehearseSQL, "SELECT max_players,quest_type,quest_id,mark,flags,CURRENT_TIMESTAMP") {
		t.Fatal("rehearsal may allocate real serial IDs")
	}
	if !strings.HasSuffix(bundle.RehearseSQL, "ROLLBACK;\n") || !strings.Contains(bundle.RollbackSQL, "to_jsonb(d) IS DISTINCT FROM b.after_row") || !strings.Contains(bundle.RollbackSQL, "rolled_back=true") || strings.Contains(bundle.RollbackSQL, "DROP TABLE") {
		t.Fatal("undo guard/history missing")
	}
	if !strings.Contains(bundle.ApplySQL, "'nier'") && p.Additions[0].CollabScope == "nier" {
		t.Fatal("scope missing")
	}
	p.ActivateQuestIDs = []int{40139, 53065}
	same, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if same != bundle {
		t.Fatal("input ordering changed bundle identity")
	}
}
