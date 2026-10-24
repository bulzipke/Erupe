package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
	"erupe-ce/network/mhfpacket"
)

type commonListFixture struct {
	dir, report string
	input       rehearsalInput
	events      rehearsalEventInput
	catalog     *questdelivery.Catalog
	bank        map[string][]int
	managed     map[int]bool
	enabled     map[int]bool
	times       []time.Time
}

func commonListLoad(t *testing.T) commonListFixture {
	t.Helper()
	dir := os.Getenv("ERUPE_QUEST_COMMON_FIXTURE")
	if dir == "" {
		t.Skip("detached common-list fixture not requested")
	}
	var contract struct {
		Schema       string `json:"schema"`
		FixtureFiles []struct {
			Path  string `json:"path"`
			SHA   string `json:"sha256"`
			Bytes int    `json:"bytes"`
		} `json:"fixture_files"`
		BinaryRoot    string `json:"binary_root"`
		BinaryCapture struct {
			Path string `json:"path"`
			SHA  string `json:"sha256"`
		} `json:"binary_capture"`
		Times []time.Time `json:"snapshot_probe_times"`
	}
	rehearsalJSON(t, filepath.Join(dir, "..", "common-fixture-build.json"), &contract)
	if contract.Schema != "quest-common-runtime-fixture-v1" || len(contract.Times) != 4 || len(contract.FixtureFiles) != 8 {
		t.Fatal("invalid detached common fixture contract")
	}
	for _, file := range contract.FixtureFiles {
		data, err := os.ReadFile(file.Path)
		sum := sha256.Sum256(data)
		if err != nil || len(data) != file.Bytes || hex.EncodeToString(sum[:]) != file.SHA {
			t.Fatal("common fixture fingerprint mismatch", file.Path, err)
		}
	}
	captureBytes, err := os.ReadFile(contract.BinaryCapture.Path)
	sum := sha256.Sum256(captureBytes)
	if err != nil || hex.EncodeToString(sum[:]) != contract.BinaryCapture.SHA {
		t.Fatal("captured binary inventory fingerprint mismatch", err)
	}
	var capture struct {
		Records []struct {
			Name  string `json:"name"`
			Bytes int    `json:"bytes"`
			SHA   string `json:"sha256"`
		} `json:"records"`
	}
	if err := json.Unmarshal(captureBytes, &capture); err != nil || len(capture.Records) != 877 {
		t.Fatal("invalid captured body inventory", err)
	}
	for _, row := range capture.Records {
		if filepath.Base(row.Name) != row.Name {
			t.Fatal("captured body name is not a basename")
		}
		data, err := os.ReadFile(filepath.Join(contract.BinaryRoot, "quests", row.Name))
		sum := sha256.Sum256(data)
		if err != nil || len(data) != row.Bytes || hex.EncodeToString(sum[:]) != row.SHA {
			t.Fatal("captured body changed", row.Name, err)
		}
	}
	f := commonListFixture{dir: dir, report: os.Getenv("ERUPE_QUEST_COMMON_REPORT"), managed: map[int]bool{}, enabled: map[int]bool{}, times: contract.Times}
	if f.report == "" {
		t.Fatal("explicit detached common-list report path required")
	}
	rehearsalJSON(t, filepath.Join(dir, "rehearsal-scenarios.json"), &f.input)
	rehearsalJSON(t, filepath.Join(dir, "annual-event-rows.json"), &f.events)
	var bank struct {
		Rosters map[string][]int `json:"rosters"`
	}
	rehearsalJSON(t, filepath.Join(dir, "expected-rosters.json"), &bank)
	f.bank = bank.Rosters
	for digest, ids := range f.bank {
		if !sort.IntsAreSorted(ids) || rehearsalDigest(ids) != digest {
			t.Fatal("original expected roster bank changed")
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "common-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.catalog, err = questdelivery.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var original, common map[string]json.RawMessage
	rehearsalJSON(t, filepath.Join(dir, "source-manifest.json"), &original)
	if err := json.Unmarshal(data, &common); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"quests", "schedules"} {
		var a, b any
		if json.Unmarshal(original[key], &a) != nil || json.Unmarshal(common[key], &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatal("all-channels projection changed original quest objects or dates", key)
		}
	}
	profiles := f.catalog.Profiles()
	if len(profiles) != 1 || len(profiles[0].Channels) != 0 || len(profiles[0].Worlds) != 12 || len(f.catalog.QuestIDs()) != 594 {
		t.Fatal("not a single all-existing-channels common profile")
	}
	if err := f.input.Config.ValidateQuestDeliveryWorlds(f.catalog); err != nil {
		t.Fatal(err)
	}
	for _, id := range f.catalog.QuestIDs() {
		f.managed[id] = true
	}
	var rules struct {
		Quests []struct {
			ID       int  `json:"quest_id"`
			Disabled bool `json:"disabled"`
		} `json:"quests"`
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	for _, row := range rules.Quests {
		if !row.Disabled {
			f.enabled[row.ID] = true
		}
	}
	f.input.Config.BinPath = contract.BinaryRoot
	if len(f.events.Rows) != 829 || len(f.events.Extra15) != 15 || len(f.events.Support) != 1 || len(f.input.Profiles) != 13 || len(f.input.Towers) != 4 || len(f.input.Segments) != 730 {
		t.Fatal("original fixture dimensions changed")
	}
	return f
}

// The unchanged golden bank represents the original per-world native filters.
// Remove the594 managed IDs whose placement changed, then add the common
// schedule's eligible ordinary IDs. Its590 enabled rows have no Collab/Return/
// Tower classification; dedicated unmanaged IDs remain exactly in the bank.
func (f commonListFixture) expected(t *testing.T, pi, ti int, at time.Time) []int {
	t.Helper()
	var expected *rehearsalExpected
	for _, segment := range f.input.Segments {
		if !at.Before(segment.From) && at.Before(segment.Until) {
			for i := range segment.Expected {
				row := &segment.Expected[i]
				if row.Profile == pi && row.Tower == ti {
					expected = row
					break
				}
			}
			break
		}
	}
	if expected == nil {
		t.Fatal("probe outside the original research-year/golden matrix", at, pi, ti)
	}
	base, ok := f.bank[expected.SHA]
	if !ok || len(base) != expected.Count {
		t.Fatal("missing original golden roster")
	}
	ids := make([]int, 0, len(base)+590)
	for _, id := range base {
		if !f.managed[id] {
			ids = append(ids, id)
		}
	}
	ids = append(ids, f.catalog.AllowedQuestIDs(f.input.Profiles[pi].World, 1, at)...)
	sort.Ints(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatal("invalid projected golden IDs")
		}
	}
	return ids
}

func TestQuestDeliveryCommonFixturePublicationAndConditions(t *testing.T) {
	f := commonListLoad(t)
	ordinary := 0
	rows := append(append([]EventQuest(nil), f.events.Rows...), f.events.Extra15...)
	for _, row := range rows {
		if f.enabled[row.QuestID] {
			if row.QuestID < 40000 || row.CollabScope != "" || row.QuestType == 55 || isReturnWorldQuest(row.QuestID) {
				t.Fatal("common golden projection cannot flatten a dedicated managed quest", row.QuestID)
			}
			for _, builtIn := range builtInCollabQuests {
				if builtIn.questID == row.QuestID {
					t.Fatal("managed Collab provider was flattened")
				}
			}
			ordinary++
		}
	}
	if ordinary != 590 {
		t.Fatal("enabled common listing sources missing or duplicated", ordinary)
	}
	comparisons := 0
	for _, segment := range f.input.Segments {
		want := f.catalog.AllowedQuestIDs(f.input.Profiles[0].World, 1, segment.From)
		for _, profile := range f.input.Profiles {
			for _, channel := range []int{1, 2} {
				if got := f.catalog.AllowedQuestIDs(profile.World, channel, segment.From); !reflect.DeepEqual(got, want) {
					t.Fatal("common publication retained a world/channel destination", profile.World, channel, segment.From)
				}
				comparisons++
			}
		}
	}
	t.Logf("common publication masks=%d; channel2 is synthetic, not an operating channel", comparisons)
}

func TestQuestDeliveryCommonCapturedSnapshotPages(t *testing.T) {
	f := commonListLoad(t)
	rows := append(append([]EventQuest(nil), f.events.Rows...), f.events.Extra15...)
	cache := NewQuestCache(3600)
	states, pages, peak := 0, 0, 0
	for _, at := range f.times {
		for pi, profile := range f.input.Profiles {
			for ti, tower := range f.input.Towers {
				want := f.expected(t, pi, ti, at)
				var first [][]byte
				for _, channel := range []int{profile.Channel, 2} {
					probe := profile
					probe.Channel = channel //2 is only a synthetic same-eligibility probe.
					s := rehearsalSession(f.input.Config, probe, tower, f.catalog, cache, rows)
					snapshot, err := s.encodeQuestDeliverySnapshot(append([]EventQuest(nil), rows...), at, func(eq EventQuest) ([]byte, error) {
						return makeEventQuestWithAccess(s, eq, s.questAccessSnapshot.Load(), at)
					})
					if err != nil {
						t.Fatal("common captured snapshot failed", pi, ti, at, err)
					}
					got := rehearsalSnapshotIDs(t, snapshot)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("common/dedicated golden mismatch pi=%d ti=%d ch=%d at=%s got=%d want=%d", pi, ti, channel, at, len(got), len(want))
					}
					if first == nil {
						first = snapshot.entries
					} else if !reflect.DeepEqual(first, snapshot.entries) {
						t.Fatal("same eligibility/channel probe changed native frames")
					}
					pages += rehearsalPages(t, snapshot)
					peak = max(peak, len(got))
					states++
				}
			}
		}
	}
	commonListReport(t, f.report+"-snapshot.json", map[string]any{"passed": true, "controlled_snapshot_states": states, "complete_snapshot_pages": pages, "observed_peak": peak, "probe_times": f.times, "production_changes": false, "actual_client_reception": false, "annual_peak_proven": false, "synthetic_channel2_included": true})
	t.Logf("controlled snapshot states=%d complete pages=%d sampled peak=%d", states, pages, peak)
}

func TestQuestDeliveryCommonActualHandlerPages(t *testing.T) {
	f := commonListLoad(t)
	rows := append(append([]EventQuest(nil), f.events.Rows...), f.events.Extra15...)
	before, err := json.Marshal(f.events)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewQuestCache(3600)
	states, pages, largest, peak := 0, 0, 0, 0
	for pi, profile := range f.input.Profiles {
		for ti, tower := range f.input.Towers {
			for _, channel := range []int{profile.Channel, 2} {
				probe := profile
				probe.Channel = channel
				s := rehearsalSession(f.input.Config, probe, tower, f.catalog, cache, rows)
				repo := &rehearsalListRepo{rows: rows}
				s.server.eventRepo = repo
				at := time.Now()
				want := f.expected(t, pi, ti, at)
				var offset uint16
				var pinned *questDeliveryListSnapshot
				for {
					handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 919, Offset: offset})
					ack := licenseHandlerAck(t, s)
					if ack.ErrorCode != 0 || ack.AckHandle != 919 || !ack.IsBufferResponse || len(ack.AckData) < 6 {
						t.Fatal("actual common ACK failed")
					}
					if pinned == nil {
						pinned = s.questDeliveryList
					}
					if pinned == nil || pinned != s.questDeliveryList {
						t.Fatal("ACK pagination changed the pinned roster")
					}
					body := ack.AckData
					returned := binary.BigEndian.Uint16(body[:2])
					total := binary.BigEndian.Uint16(body[len(body)-4 : len(body)-2])
					own := binary.BigEndian.Uint16(body[len(body)-2:])
					if int(total) != len(pinned.entries) || own != offset || int(offset)+int(returned) > len(pinned.entries) {
						t.Fatal("ACK count/offset mismatch")
					}
					frames := bytes.Join(pinned.entries[int(offset):int(offset)+int(returned)], nil)
					if len(body) < len(frames)+6 || !bytes.Equal(body[2:2+len(frames)], frames) {
						t.Fatal("ACK omitted or changed a native frame")
					}
					largest = max(largest, len(body))
					pages++
					offset += returned
					if offset == total {
						break
					}
					if returned == 0 || pages > 10000 {
						t.Fatal("ACK pagination did not terminate")
					}
				}
				// The real handler uses time.Now; do not replace the production clock.
				if !reflect.DeepEqual(want, f.expected(t, pi, ti, time.Now())) {
					t.Fatal("research boundary crossed during actual handler probe; rerun")
				}
				got := rehearsalSnapshotIDs(t, pinned)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("actual handler/golden mismatch pi=%d ti=%d ch=%d got=%d want=%d", pi, ti, channel, len(got), len(want))
				}
				if repo.reads != 1 || repo.writes != 0 {
					t.Fatal("native pages reread or changed event schedules")
				}
				peak = max(peak, len(got))
				states++
			}
		}
	}
	after, err := json.Marshal(f.events)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("native rehearsal changed original event metadata")
	}
	commonListReport(t, f.report+"-handler.json", map[string]any{"passed": true, "real_handler_clock": "time.Now, not replaced", "profile_tower_channel_states": states, "complete_ACK_pages": pages, "largest_ACK_body_bytes": largest, "observed_current_peak": peak, "one_repo_read_per_state": true, "schedule_writes": 0, "golden_basis": "original unmanaged roster bank plus common ordinary managed schedule", "production_changes": false, "database_access": false, "actual_network_or_client_reception": false, "synthetic_channel2_included": true})
	t.Logf("actual ACK states=%d pages=%d largest=%d current sampled peak=%d", states, pages, largest, peak)
}

func commonListReport(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
