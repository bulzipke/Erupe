package channelserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func TestQuestDeliveryPageBoundaries(t *testing.T) {
	for _, size := range []int{questDataMinLen, questDataMaxLen} {
		for _, count := range []int{0, 1, 511, 512} {
			snapshot := &questDeliveryListSnapshot{}
			for i := 0; i < count; i++ {
				entry := bytes.Repeat([]byte{byte(i)}, size)
				binary.BigEndian.PutUint32(entry, uint32(i))
				snapshot.entries = append(snapshot.entries, entry)
			}
			var offset uint16
			for {
				bf := byteframe.NewByteFrame()
				bf.WriteUint16(0)
				total, returned := snapshot.appendPage(bf, offset)
				if int(total) != count || int(returned) > count-int(offset) || len(bf.Data()) > 60000+size {
					t.Fatal("invalid page bounds", count, size, total, returned, len(bf.Data()))
				}
				if !bytes.Equal(bf.Data()[2:], bytes.Join(snapshot.entries[int(offset):int(offset)+int(returned)], nil)) {
					t.Fatal("page changed entry order or content")
				}
				offset += returned
				if offset == total {
					break
				}
				if returned == 0 {
					t.Fatal("page made no progress")
				}
			}
			bf := byteframe.NewByteFrame()
			if total, returned := snapshot.appendPage(bf, uint16(count+1)); int(total) != count || returned != 0 || len(bf.Data()) != 0 {
				t.Fatal("out-of-range offset repeated entries")
			}
		}
	}
}

type rehearsalListRepo struct {
	mockEventRepo
	rows          []EventQuest
	reads, writes int
}

func (r *rehearsalListRepo) GetEventQuests() ([]EventQuest, error) {
	r.reads++
	return append([]EventQuest(nil), r.rows...), nil
}

func (r *rehearsalListRepo) UpdateEventQuestStartTimes([]EventQuestUpdate) error {
	r.writes++
	return nil
}

// Exercise the actual request handler and complete ACK pages as well as the
// annual snapshot/page helper. Authority and DB data are still local fixtures.
func TestQuestDeliveryRehearsalNativePages(t *testing.T) {
	dir := os.Getenv("ERUPE_QUEST_REHEARSAL_DIR")
	if dir == "" {
		t.Skip("external annual research fixture not requested")
	}
	binPath, reportPath := os.Getenv("ERUPE_QUEST_REHEARSAL_BIN"), os.Getenv("ERUPE_QUEST_REHEARSAL_REPORT")
	if binPath == "" || reportPath == "" {
		t.Fatal("explicit binary input and report output required")
	}
	var input rehearsalInput
	rehearsalJSON(t, filepath.Join(dir, "rehearsal-scenarios.json"), &input)
	var eventInput rehearsalEventInput
	rehearsalJSON(t, filepath.Join(dir, "annual-event-rows.json"), &eventInput)
	data, err := os.ReadFile(filepath.Join(dir, "annual-manifest-with-support-plus15.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := questdelivery.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rows := append(append(append([]EventQuest(nil), eventInput.Rows...), eventInput.Support...), eventInput.Extra15...)
	input.Config.BinPath = binPath
	cache := NewQuestCache(3600)
	states, pages, largestACK := 0, 0, 0
	for _, profile := range input.Profiles {
		for _, tower := range input.Towers {
			s := rehearsalSession(input.Config, profile, tower, catalog, cache, rows)
			repo := &rehearsalListRepo{rows: rows}
			s.server.eventRepo = repo
			var offset uint16
			var first *questDeliveryListSnapshot
			for {
				handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 77, Offset: offset})
				ack := licenseHandlerAck(t, s)
				if ack.ErrorCode != 0 || ack.AckHandle != 77 || !ack.IsBufferResponse || len(ack.AckData) < 6 {
					t.Fatal("native enumeration ACK failed", ack)
				}
				if first == nil {
					first = s.questDeliveryList
				}
				if first == nil || first != s.questDeliveryList {
					t.Fatal("native pages changed the frozen snapshot")
				}
				body := ack.AckData
				returned := binary.BigEndian.Uint16(body[:2])
				total := binary.BigEndian.Uint16(body[len(body)-4 : len(body)-2])
				ownOffset := binary.BigEndian.Uint16(body[len(body)-2:])
				if int(total) != len(first.entries) || ownOffset != offset || int(offset)+int(returned) > len(first.entries) {
					t.Fatal("native total/offset/count mismatch")
				}
				want := bytes.Join(first.entries[int(offset):int(offset)+int(returned)], nil)
				if len(body) < 2+len(want)+4 || !bytes.Equal(body[2:2+len(want)], want) {
					t.Fatal("ACK lost or changed a quest frame")
				}
				if len(body) > largestACK {
					largestACK = len(body)
				}
				pages++
				offset += returned
				if offset == total {
					break
				}
				if returned == 0 || pages > 52*512 {
					t.Fatal("native paging did not terminate")
				}
			}
			if repo.reads != 1 || repo.writes != 0 {
				t.Fatal("paging reread or rewrote event schedules", repo.reads, repo.writes)
			}
			rehearsalSnapshotIDs(t, first)
			states++
		}
	}
	report := map[string]interface{}{"schema": "quest-delivery-native-handler-pages-v1", "verified_at": time.Now().UTC(), "passed": true,
		"binary_input": binPath, "profile_tower_states": states, "ACK_pages_checked": pages, "largest_ACK_body_bytes": largestACK,
		"one_snapshot_read_per_state": true, "schedule_writes": 0, "all_own_offsets_and_totals_match": true,
		"actual_network_or_client_reception": false, "database_access": false, "manual_claim_save_test": false, "production_changes": false}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSuffix(reportPath, filepath.Ext(reportPath)) + "-native-pages.json"
	if err := os.WriteFile(path, append(encoded, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("native handler states=%d ACK pages=%d largest body=%d", states, pages, largestACK)
}

type rehearsalProfile struct {
	World          string `json:"world"`
	Channel        int    `json:"channel"`
	WorldType      uint8  `json:"world_type"`
	CollabEvent    string `json:"collab_event"`
	ReturnEligible bool   `json:"return_eligible"`
}

type rehearsalTowerState struct {
	Key             string    `json:"key"`
	RotationEnabled bool      `json:"rotation_enabled"`
	EventActive     bool      `json:"event_active"`
	Data            TowerData `json:"data"`
}

type rehearsalExpected struct {
	Profile int    `json:"profile_index"`
	Tower   int    `json:"tower_state_index"`
	Count   int    `json:"count"`
	SHA     string `json:"wire_ids_sha256"`
}

type rehearsalSegment struct {
	From     time.Time           `json:"from"`
	Until    time.Time           `json:"until"`
	Expected []rehearsalExpected `json:"expected"`
}

type rehearsalInput struct {
	Config       cfg.Config             `json:"config"`
	Profiles     []rehearsalProfile     `json:"profiles"`
	Towers       []rehearsalTowerState  `json:"tower_states"`
	Segments     []rehearsalSegment     `json:"segments"`
	RosterBank   string                 `json:"roster_bank_file"`
	ExpectedPeak map[string]int         `json:"expected_peaks"`
	Overlay      map[string]interface{} `json:"expected_overlay_ids_by_world"`
}

type rehearsalEventInput struct {
	Rows    []EventQuest `json:"event_rows"`
	Support []EventQuest `json:"optional_support_rows"`
	Extra15 []EventQuest `json:"optional_15_rows"`
}

// No real event/character/authority repository is used by this rehearsal.
type rehearsalTowerRepo struct {
	mockTowerRepo
	active bool
}

func (r *rehearsalTowerRepo) EnsureTowerEvent(now time.Time, _ cfg.TowerRotationOptions, _ int32) (TowerEvent, error) {
	end := now.Add(time.Hour)
	if !r.active {
		end = now.Add(-time.Hour)
	}
	return TowerEvent{ID: 1, Start: now.Add(-2 * time.Hour), End: end, ClaimUntil: now.Add(2 * time.Hour)}, nil
}

func (r *rehearsalTowerRepo) ForTowerRound(int32) TowerRepo { return r }

type rehearsalAccessLoader struct{ value *QuestAccessSnapshot }

func (r rehearsalAccessLoader) Load(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
	return r.value, nil
}

func rehearsalJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 32<<20 {
		t.Fatal("invalid rehearsal input", path, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(path, err)
	}
}

func rehearsalDigest(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, ",") + "\n"))
	return hex.EncodeToString(sum[:])
}

func rehearsalWireID(t *testing.T, frame []byte) int {
	t.Helper()
	// Actual ZZ event frame: 22-byte BE header, then the little-endian quest
	// body. Its embedded quest ID is at body+0x2e (also quest_json.go).
	if len(frame) < questDataMinLen || len(frame) > questDataMaxLen {
		t.Fatal("invalid serialized event length", len(frame))
	}
	bodyLength := int(binary.BigEndian.Uint16(frame[20:22]))
	if 22+bodyLength+2 != len(frame) || frame[len(frame)-2] != 1 || frame[len(frame)-1] != 0 {
		t.Fatal("invalid native frame body/notes boundary")
	}
	return int(binary.LittleEndian.Uint16(frame[22+0x2e : 22+0x30]))
}

func rehearsalSnapshotIDs(t *testing.T, snapshot *questDeliveryListSnapshot) []int {
	t.Helper()
	ids := make([]int, len(snapshot.entries))
	seen := map[int]bool{}
	for i, entry := range snapshot.entries {
		ids[i] = rehearsalWireID(t, entry)
		if seen[ids[i]] {
			t.Fatal("duplicate actual embedded wire ID", ids[i])
		}
		seen[ids[i]] = true
	}
	sort.Ints(ids)
	return ids
}

func rehearsalPages(t *testing.T, snapshot *questDeliveryListSnapshot) int {
	t.Helper()
	var offset uint16
	pages := 0
	for {
		bf := byteframe.NewByteFrame()
		bf.WriteUint16(0)
		total, returned := snapshot.appendPage(bf, offset)
		if int(total) != len(snapshot.entries) || !bytes.Equal(bf.Data()[2:], bytes.Join(snapshot.entries[int(offset):int(offset)+int(returned)], nil)) {
			t.Fatal("actual page omitted or changed a frame")
		}
		pages++
		offset += returned
		if offset == total {
			return pages
		}
		if returned == 0 || pages > 512 {
			t.Fatal("native paging did not terminate")
		}
	}
}

func rehearsalSession(config cfg.Config, profile rehearsalProfile, tower rehearsalTowerState, catalog *questdelivery.Catalog, cache *QuestCache, rows []EventQuest) *Session {
	config.RealClientMode = cfg.ZZ
	config.TowerRotation.Enabled = tower.RotationEnabled
	server := createMockServer()
	server.erupeConfig = &config
	server.worldName, server.worldType, server.channelNumber, server.collabEvent = profile.World, profile.WorldType, profile.Channel, profile.CollabEvent
	server.questDelivery, server.questCache = catalog, cache
	server.towerRepo = &rehearsalTowerRepo{mockTowerRepo: mockTowerRepo{towerData: tower.Data}, active: tower.EventActive}
	server.userRepo = returnWorldUserMock{allowed: profile.ReturnEligible}
	server.eventRepo = &mockEventRepo{eventQuests: rows}
	access := newQuestAccessSnapshot(1, 1, time.Unix(1, 0), nil, nil)
	server.questAccess = &QuestAccessGameplay{required: map[int]struct{}{}, loader: rehearsalAccessLoader{value: access}}
	for _, row := range rows {
		if row.QuestType == QuestTypeSpecialTool {
			server.questAccess.required[row.QuestID] = struct{}{}
		}
	}
	session := createMockSession(1, server)
	session.userID, session.clientLang, session.logger = 1, "ko", zap.NewNop()
	session.questAccessSnapshot.Store(access)
	return session
}

// Opt in with ERUPE_QUEST_REHEARSAL_DIR, ERUPE_QUEST_REHEARSAL_BIN and
// ERUPE_QUEST_REHEARSAL_REPORT. The binary directory may be a verified NAS
// workspace copy or the local catalog. No NAS/DB/network writes or login occur.
// The external review fixture deliberately labels new row metadata TEST_ONLY.
func TestQuestDeliveryAnnualRuntimeRehearsal(t *testing.T) {
	dir := os.Getenv("ERUPE_QUEST_REHEARSAL_DIR")
	if dir == "" {
		t.Skip("external annual research fixture not requested")
	}
	binPath, reportPath := os.Getenv("ERUPE_QUEST_REHEARSAL_BIN"), os.Getenv("ERUPE_QUEST_REHEARSAL_REPORT")
	if binPath == "" || reportPath == "" {
		t.Fatal("rehearsal requires explicit binary input and report output")
	}
	var input rehearsalInput
	rehearsalJSON(t, filepath.Join(dir, "rehearsal-scenarios.json"), &input)
	var eventInput rehearsalEventInput
	rehearsalJSON(t, filepath.Join(dir, "annual-event-rows.json"), &eventInput)
	var bank struct {
		Rosters map[string][]int `json:"rosters"`
	}
	rehearsalJSON(t, filepath.Join(dir, input.RosterBank), &bank)
	if len(input.Profiles) != 13 || len(input.Towers) != 4 || len(input.Segments) != 730 || len(eventInput.Rows) != 829 {
		t.Fatal("unexpected annual fixture dimensions")
	}
	for sha, ids := range bank.Rosters {
		if !sort.IntsAreSorted(ids) || rehearsalDigest(ids) != sha {
			t.Fatal("corrupt expected roster bank", sha)
		}
	}
	input.Config.BinPath, input.Config.RealClientMode = binPath, cfg.ZZ
	before, _ := json.Marshal(eventInput)
	type variant struct {
		name, manifest string
		support, extra bool
		peakKey        string
	}
	variants := []variant{
		{"baseline", "annual-manifest.json", false, false, "base"},
		{"support", "annual-manifest-with-support.json", true, false, "with_support"},
		{"plus15", "annual-manifest-plus15.json", false, true, "with_additional15"},
		{"support_plus15", "annual-manifest-with-support-plus15.json", true, true, "with_support_and_additional15"},
	}
	type frameKey struct {
		Profile, Tower int
		Row            EventQuest
	}
	type frameResult struct {
		Data []byte
		Err  error
	}
	frames := map[frameKey]frameResult{}
	cache := NewQuestCache(3600)
	summary := map[string]interface{}{}
	allMissing := map[int]string{}
	totalContexts, totalStates, mismatchStates := 0, 0, 0
	maxPages, authorizedVariants := 0, 0
	for _, v := range variants {
		expectedPeak, ok := input.ExpectedPeak[v.peakKey]
		if !ok || expectedPeak < 0 || expectedPeak > questEnumerateClientCapacity {
			t.Fatal("missing or invalid expected peak", v.name, v.peakKey, expectedPeak)
		}
		manifestBytes, err := os.ReadFile(filepath.Join(dir, v.manifest))
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := questdelivery.Parse(manifestBytes)
		if err != nil {
			t.Fatal(v.name, err)
		}
		if err := input.Config.ValidateQuestDeliveryWorlds(catalog); err != nil {
			t.Fatal(err)
		}
		rows := append([]EventQuest(nil), eventInput.Rows...)
		if v.support {
			rows = append(rows, eventInput.Support...)
		}
		if v.extra {
			rows = append(rows, eventInput.Extra15...)
		}
		managed := map[int]bool{}
		var raw struct {
			Quests []struct {
				ID int `json:"quest_id"`
			} `json:"quests"`
		}
		if err := json.Unmarshal(manifestBytes, &raw); err != nil {
			t.Fatal(err)
		}
		for _, q := range raw.Quests {
			managed[q.ID] = true
		}
		managedIDs := make([]int, 0, len(managed))
		for id := range managed {
			managedIDs = append(managedIDs, id)
		}
		sort.Ints(managedIDs)
		// Prove the supplied constant-state intervals contain every boundary
		// emitted by the real schedule engine, before memoizing equal contexts.
		boundaries := map[int64]bool{}
		for _, segment := range input.Segments {
			boundaries[segment.From.UnixNano()] = true
			boundaries[segment.Until.UnixNano()] = true
		}
		windows, err := catalog.Windows(input.Segments[0].From, input.Segments[len(input.Segments)-1].Until)
		if err != nil {
			t.Fatal(err)
		}
		start, end := input.Segments[0].From, input.Segments[len(input.Segments)-1].Until
		for _, w := range windows {
			for _, at := range []time.Time{w.StartsAt, w.EndsAt} {
				if at.After(start) && at.Before(end) && !boundaries[at.UnixNano()] {
					t.Fatal("missing actual schedule boundary", at)
				}
			}
		}
		sessions := make([][]*Session, len(input.Profiles))
		for pi, p := range input.Profiles {
			for _, tower := range input.Towers {
				sessions[pi] = append(sessions[pi], rehearsalSession(input.Config, p, tower, catalog, cache, rows))
			}
		}
		type contextKey struct {
			Profile, Tower int
			Decisions      [32]byte
		}
		type contextResult struct {
			IDs   []int
			Pages int
		}
		contexts := map[contextKey]contextResult{}
		peak, variantMismatch := 0, 0
		for _, segment := range input.Segments {
			decisions := make([][32]byte, len(input.Profiles))
			for pi := range input.Profiles {
				mask := make([]byte, len(managedIDs))
				for i, id := range managedIDs {
					if sessions[pi][0].allowsQuestDelivery(id, segment.From) {
						mask[i] = 1
					}
				}
				decisions[pi] = sha256.Sum256(mask)
			}
			if len(segment.Expected) != 52 {
				t.Fatal("incomplete profile/tower matrix")
			}
			for _, expected := range segment.Expected {
				pi, ti := expected.Profile, expected.Tower
				s := sessions[pi][ti]
				key := contextKey{pi, ti, decisions[pi]}
				result, found := contexts[key]
				if !found {
					snapshot, err := s.encodeQuestDeliverySnapshot(append([]EventQuest(nil), rows...), segment.From, func(eq EventQuest) ([]byte, error) {
						fk := frameKey{pi, ti, eq}
						if cached, ok := frames[fk]; ok {
							return cached.Data, cached.Err
						}
						data, err := makeEventQuestWithAccess(s, eq, s.questAccessSnapshot.Load(), segment.From)
						if err != nil || len(data) < questDataMinLen || len(data) > questDataMaxLen {
							allMissing[eq.QuestID] = fmt.Sprintf("bytes=%d err=%v", len(data), err)
						} else {
							listed := towerMilestoneListing(eq, cfg.ZZ).QuestID
							if rehearsalWireID(t, data) != listed {
								t.Fatal("wrong embedded listing ID", eq.QuestID, listed)
							}
							if s.server.questAccess.requires(eq.QuestID) {
								grant := newQuestAccessSnapshot(1, 1, time.Unix(1, 0), map[int]questAccessGrant{eq.QuestID: {accountPermanent: true}}, nil)
								owned, grantErr := makeEventQuestWithAccess(s, eq, grant, segment.From)
								if grantErr != nil || data[11] != 0 || owned[11] != 1 {
									t.Fatal("invalid Type9 ownership marker", grantErr)
								}
								owned[11] = data[11]
								if !bytes.Equal(data, owned) {
									t.Fatal("authority changed native quest content or length")
								}
								authorizedVariants++
							}
						}
						frames[fk] = frameResult{data, err}
						return data, err
					})
					if err != nil {
						t.Fatal(v.name, pi, ti, err)
					}
					result = contextResult{rehearsalSnapshotIDs(t, snapshot), rehearsalPages(t, snapshot)}
					contexts[key] = result
				}
				want := append([]int(nil), bank.Rosters[expected.SHA]...)
				if len(want) != expected.Count {
					t.Fatal("invalid expected state", expected)
				}
				wantSet := map[int]bool{}
				for _, id := range want {
					wantSet[id] = true
				}
				if v.support && input.Profiles[pi].World == "리버럴" {
					for _, row := range eventInput.Support {
						wantSet[row.QuestID] = true
					}
				}
				if v.extra {
					for _, row := range eventInput.Extra15 {
						if managed, allowed := catalog.Allows(row.QuestID, input.Profiles[pi].World, input.Profiles[pi].Channel, segment.From); managed && allowed {
							wantSet[row.QuestID] = true
						}
					}
				}
				want = want[:0]
				for id := range wantSet {
					want = append(want, id)
				}
				sort.Ints(want)
				if !reflect.DeepEqual(want, result.IDs) {
					variantMismatch++
					mismatchStates++
				}
				if len(result.IDs) > peak {
					peak = len(result.IDs)
				}
				if result.Pages > maxPages {
					maxPages = result.Pages
				}
				totalStates++
			}
		}
		summary[v.name] = map[string]interface{}{"states_checked": len(input.Segments) * 52, "actual_encoder_contexts": len(contexts), "actual_peak": peak, "expected_peak": expectedPeak, "mismatched_states": variantMismatch}
		totalContexts += len(contexts)
		if peak != expectedPeak {
			mismatchStates++
		}
	}
	after, _ := json.Marshal(eventInput)
	if !bytes.Equal(before, after) {
		t.Fatal("rehearsal changed original event metadata")
	}
	report := map[string]interface{}{
		"schema": "quest-delivery-annual-runtime-rehearsal-v1", "verified_at": time.Now().UTC(),
		"passed":       mismatchStates == 0 && len(allMissing) == 0,
		"binary_input": binPath, "variants": summary, "state_references_checked": totalStates,
		"actual_encoder_contexts": totalContexts, "actual_serialized_event_contexts": len(frames),
		"Type9_locked_owned_frame_comparisons": authorizedVariants,
		"max_pages_per_serialized_roster":      maxPages, "missing_or_invalid_frames": allMissing,
		"mismatched_state_references": mismatchStates,
		"cache_contract":              "Context memo uses actual schedule decisions plus fixed profile/tower. All real schedule boundaries are verified. Serialized-frame memo includes profile, tower and the complete EventQuest row.",
		"limits":                      []string{"Test-only metadata for unregistered events; not final reception category or party-size approval.", "Tower/return/authority fixtures; no live player entitlement or yearly Tower schedule is asserted.", "Not actual client reception, quest entry, consumption, clear, item grant or save verification."},
		"production_changes":          false, "database_access": false, "manual_claim_save_test": false,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, append(encoded, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if mismatchStates != 0 || len(allMissing) != 0 {
		t.Fatalf("actual rehearsal mismatches=%d invalid files=%d; inspect %s", mismatchStates, len(allMissing), reportPath)
	}
	t.Logf("states=%d actual contexts=%d serialized contexts=%d Type9 comparisons=%d max pages=%d", totalStates, totalContexts, len(frames), authorizedVariants, maxPages)
}
