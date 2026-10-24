package channelserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

var runtimeExpectedCollabIDs = []int{40211, 40212, 40213, 40214, 40215, 40217, 40221, 40223, 40224, 40225, 40226, 40227, 40239}

type collabCommonMetadataSource struct {
	calls int
	rows  []EventQuest
	err   error
}

func (source *collabCommonMetadataSource) GetEventQuests() ([]EventQuest, error) {
	source.calls++
	return source.rows, source.err
}

type collabCommonCallbackRepo struct {
	mockEventRepo
	read func() ([]EventQuest, error)
}

func (repo *collabCommonCallbackRepo) GetEventQuests() ([]EventQuest, error) { return repo.read() }

func collabRuntimeFixture(t *testing.T) map[string]any {
	t.Helper()
	root := os.Getenv("ERUPE_COLLAB_RUNTIME_FIXTURE")
	if root == "" {
		t.Fatal("explicit detached research fixture directory is required")
	}
	raw, err := os.ReadFile(filepath.Join(root, "common590-plus13-research.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func collabRuntimeCatalog(t *testing.T, fixture map[string]any) *questdelivery.Catalog {
	t.Helper()
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := questdelivery.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func collabRuntimeTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func collabRuntimeLocations(catalog *questdelivery.Catalog) []questdelivery.PublicationLocation {
	result := []questdelivery.PublicationLocation{}
	for _, world := range catalog.Profiles()[0].Worlds {
		result = append(result, questdelivery.PublicationLocation{World: world, Channel: 1}, questdelivery.PublicationLocation{World: world, Channel: 2})
	}
	return result // Channel2 is synthetic, not a new operating channel.
}

func collabRuntimePrepare(t *testing.T, catalog *questdelivery.Catalog, rows []EventQuest) *CommonCollabRuntimeCohort {
	t.Helper()
	source := &collabCommonMetadataSource{rows: rows}
	cohort, err := PrepareCommonCollabRuntime(catalog, collabRuntimeLocations(catalog), []string{"higanjima", "nier"}, source)
	if err != nil || source.calls != 1 {
		t.Fatalf("prepare failed/called more than once: %v / %d", err, source.calls)
	}
	return cohort
}

func collabRuntimeSession(t *testing.T, ordinary int, catalog *questdelivery.Catalog, cohort *CommonCollabRuntimeCohort) *Session {
	t.Helper()
	s := newEnumerateQuestSession(ordinary)
	s.logger = zap.NewNop()
	s.userID, s.clientLang = 77, "en"
	s.server.worldName, s.server.channelNumber = collabRuntimeLocations(catalog)[0].World, 1
	s.server.worldType, s.server.collabEvent = 5, collabNone
	s.server.questDelivery, s.server.commonCollab = catalog, cohort
	s.server.erupeConfig.QuestDelivery.Enabled = true
	s.server.erupeConfig.EventQuestCapacity = 16384
	s.server.userRepo = returnWorldUserMock{allowed: false}
	for _, row := range s.server.eventRepo.(*mockEventRepo).eventQuests {
		s.server.questCache.Put(row.QuestID, "en", make([]byte, 600))
	}
	for _, id := range runtimeExpectedCollabIDs {
		s.server.questCache.Put(id, "en", make([]byte, 600))
	}
	return s
}

func TestCollabCommonRuntimePreparationDeclarationAndStaticPermissions(t *testing.T) {
	source := &collabCommonMetadataSource{err: errors.New("must not query")}
	if cohort, err := PrepareCommonCollabRuntime(nil, nil, nil, source); err != nil || cohort != nil || source.calls != 0 {
		t.Fatal("nil declaration queried or activated", err)
	}
	fixture := collabRuntimeFixture(t)
	delete(fixture, "collab_presentations")
	absent := collabRuntimeCatalog(t, fixture)
	if cohort, err := PrepareCommonCollabRuntime(absent, nil, nil, source); err != nil || cohort != nil || source.calls != 0 {
		t.Fatal("absent declaration queried or activated", err)
	}
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	if cohort, err := PrepareCommonCollabRuntime(catalog, collabRuntimeLocations(catalog), nil, source); err == nil || cohort != nil || source.calls != 0 {
		t.Fatal("static permission validation performed a query or fell back")
	}
	if cohort, err := PrepareCommonCollabRuntime(catalog, collabRuntimeLocations(catalog), []string{"higanjima", "nier"}, source); err == nil || cohort != nil || source.calls != 1 {
		t.Fatal("metadata failure used builtins", err)
	}
	config := &cfg.Config{QuestDeliveryCatalog: catalog}
	config.QuestDelivery.Enabled = true
	for _, world := range catalog.Profiles()[0].Worlds {
		config.Entrance.Entries = append(config.Entrance.Entries, cfg.EntranceServerInfo{Name: world, Channels: []cfg.EntranceChannelInfo{{}, {}}})
	}
	config.GameplayOptions.EnableHiganjimaEvent, config.GameplayOptions.EnableNierEvent = true, true
	good := &collabCommonMetadataSource{}
	cohort, err := PrepareCommonCollabRuntimeFromConfig(config, good)
	if err != nil || good.calls != 1 {
		t.Fatal("config setup not prepared once", err)
	}
	config.QuestDelivery.Enabled = false
	if got, err := PrepareCommonCollabRuntimeFromConfig(config, good); err == nil || got != nil || good.calls != 1 {
		t.Fatal("inactive setting queried or silently activated the declared conversion")
	}
	config.QuestDelivery.Enabled = true
	config.GameplayOptions.EnableHiganjimaEvent = false
	if got := cohort.semantic.At(collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")).TuneValues(); !reflect.DeepEqual(got, []questdelivery.CollabTuneValue{{ID: 1144, Value: 1}}) {
		t.Fatal("published cohort followed later config mutation", got)
	}
}

func TestCollabCommonRuntimeDBScheduleAndScopeHolds(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	start := collabRuntimeTime(t, "2099-01-01T00:00:00+09:00")
	for _, test := range []struct {
		name   string
		row    EventQuest
		legacy bool
		hold   string
	}{
		{"zero_all", EventQuest{ID: 7, QuestID: 40215}, true, ""},
		{"zero_active_ignored_inactive_and_future_date", EventQuest{ID: 7, QuestID: 40215, InactiveDays: 3, StartTime: start.Add(time.Hour)}, true, ""},
		{"zero_active_one_sided_date", EventQuest{ID: 7, QuestID: 40215, StartTime: start.Add(time.Hour)}, true, ""},
		{"negative_active_original_always_but_held", EventQuest{ID: 7, QuestID: 40215, ActiveDays: -1}, true, "ambiguous_negative_schedule"},
		{"negative_inactive_original_zero_active_always_but_held", EventQuest{ID: 7, QuestID: 40215, InactiveDays: -1}, true, "ambiguous_negative_schedule"},
		{"negative_inactive_positive_active_original_false", EventQuest{ID: 7, QuestID: 40215, ActiveDays: 3, InactiveDays: -1, StartTime: start}, false, "ambiguous_negative_schedule"},
		{"missing_start", EventQuest{ID: 7, QuestID: 40215, ActiveDays: 3, InactiveDays: 1}, false, "inactive_or_invalid_schedule"},
		{"future_start", EventQuest{ID: 7, QuestID: 40215, ActiveDays: 3, InactiveDays: 1, StartTime: start.Add(time.Hour)}, false, "schedule_alignment_required"},
		{"valid_cycle", EventQuest{ID: 7, QuestID: 40215, ActiveDays: 3, InactiveDays: 1, StartTime: start}, true, "schedule_alignment_required"},
		{"zero_inactive_not_assumed_unbounded_before_start", EventQuest{ID: 7, QuestID: 40215, ActiveDays: 3, StartTime: start}, true, "schedule_alignment_required"},
		{"overflow", EventQuest{ID: 7, QuestID: 40215, ActiveDays: math.MaxInt, StartTime: start}, false, "inactive_or_invalid_schedule"},
		{"explicit_scope_conflict", EventQuest{ID: 7, QuestID: 40215, CollabScope: "nier"}, true, "scope_conflict"},
		{"invalid_identity", EventQuest{QuestID: 40215}, true, "invalid_identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if legacyQuestActiveAt(test.row, start) != test.legacy {
				t.Fatal("legacy semantics assertion wrong")
			}
			source := &collabCommonMetadataSource{rows: []EventQuest{test.row}}
			cohort, err := PrepareCommonCollabRuntime(catalog, collabRuntimeLocations(catalog), []string{"higanjima", "nier"}, source)
			if test.hold == "" {
				if err != nil || cohort == nil {
					t.Fatal("original always row was normalized or rejected", err)
				}
			} else {
				var hold *CommonCollabPreflightHold
				if !errors.As(err, &hold) || hold.Kind != test.hold || cohort != nil {
					t.Fatal("wrong typed hold", err)
				}
			}
			if source.calls != 1 || !reflect.DeepEqual(source.rows, []EventQuest{test.row}) {
				t.Fatal("source modified or re-read")
			}
		})
	}
	for name, rows := range map[string][]EventQuest{
		"duplicate_quest": {{ID: 7, QuestID: 40215}, {ID: 8, QuestID: 40215}},
		"shared_identity": {{ID: 7, QuestID: 40215}, {ID: 7, QuestID: 50000}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := PrepareCommonCollabRuntime(catalog, collabRuntimeLocations(catalog), []string{"higanjima", "nier"}, &collabCommonMetadataSource{rows: rows}); err == nil || got != nil {
				t.Fatal("ambiguous DB projection accepted")
			}
		})
	}
}

func TestCollabCommonRuntimeFrozenDBOverrideAndMetadata(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	override := EventQuest{ID: 77, QuestID: 40212, MaxPlayers: 2, QuestType: 18, Mark: 9, Flags: 123, InactiveDays: 3, StartTime: collabRuntimeTime(t, "2100-01-01T00:00:00+09:00")}
	source := &collabCommonMetadataSource{rows: []EventQuest{override}}
	cohort, err := PrepareCommonCollabRuntime(catalog, collabRuntimeLocations(catalog), []string{"higanjima", "nier"}, source)
	if err != nil {
		t.Fatal(err)
	}
	source.rows[0].MaxPlayers, source.rows[0].ID = 9, 999
	s := collabRuntimeSession(t, 1, catalog, cohort)
	rows := []EventQuest{{ID: 100, QuestID: 50000, Flags: -1}, {ID: 999, QuestID: 40212, MaxPlayers: 9, CollabScope: "kaiji"}}
	at := collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")
	seen := map[int]EventQuest{}
	snapshot, err := s.encodeQuestDeliverySnapshotWithReturn(rows, at, false, func(row EventQuest) ([]byte, error) { seen[row.QuestID] = row; return deliveryTestEncode(row) })
	if err != nil || !reflect.DeepEqual(seen[40212], override) || len(snapshot.entries) != 14 || snapshot.collabSourceHash != cohort.sourceHash || len(cohort.sourceHash) != 64 {
		t.Fatal("DB override lost or projection mutable", err, seen[40212])
	}
	if got := seen[40215]; got.MaxPlayers != 1 || got.QuestType != 18 || got.Flags != -1 || got.CollabScope != "kaiji" {
		t.Fatal("Kaiji original builtin metadata changed", got)
	}
	ids := map[uint32]bool{}
	for _, row := range seen {
		if ids[row.ID] {
			t.Fatal("list identity collision")
		}
		ids[row.ID] = true
	}
	if source.calls != 1 {
		t.Fatal("per-session source projection re-read")
	}
	if _, err := s.encodeQuestDeliverySnapshotWithReturn([]EventQuest{{ID: 77, QuestID: 50000}}, at, false, deliveryTestEncode); err == nil {
		t.Fatal("frozen DB identity collision silently changed")
	}
	if _, err := s.encodeQuestDeliverySnapshotWithReturn([]EventQuest{{ID: math.MaxUint32, QuestID: 50000}}, at, false, deliveryTestEncode); err == nil {
		t.Fatal("synthetic list identity wrapped")
	}
}

func TestCollabCommonRuntimeWorldAndLegacySelectorsCannotChangeCommonView(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	at := collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")
	comparisons := 0
	s := collabRuntimeSession(t, 0, catalog, cohort)
	for _, location := range collabRuntimeLocations(catalog) {
		for worldType := uint8(0); worldType <= 6; worldType++ {
			for _, selector := range []string{"", collabNone, collabRandom, collabKaiji, collabNier, collabEvangelion} {
				s.server.worldName, s.server.channelNumber, s.server.worldType, s.server.collabEvent = location.World, location.Channel, worldType, selector
				s.collabEvent = collabPSO2
				s.server.collabRotation = newCollabRotation(func() string { panic("common publication requested a login draw") })
				snapshot, err := s.encodeQuestDeliverySnapshotWithReturn(nil, at, false, deliveryTestEncode)
				if err != nil || len(snapshot.entries) != 13 || !reflect.DeepEqual(snapshot.collabTuneValues, []tuneValue{{ID: 1144, Value: 1}}) {
					t.Fatal("common view retained world/random gating", location, selector, err)
				}
				for i, frame := range snapshot.entries {
					if int(binary.LittleEndian.Uint32(frame)) != runtimeExpectedCollabIDs[i] {
						t.Fatal("common IDs differ")
					}
				}
				comparisons++
			}
		}
	}
	t.Logf("same-cohort comparisons=%d; channel2 is a synthetic existing-location input", comparisons)
}

func collabRuntimeDecodedTunes(t *testing.T, payload []byte, snapshot *questDeliveryListSnapshot, offset int) (int, map[uint16]uint16) {
	t.Helper()
	returned := int(binary.BigEndian.Uint16(payload[:2]))
	if offset+returned > len(snapshot.entries) || int(binary.BigEndian.Uint16(payload[len(payload)-4:])) != len(snapshot.entries) || int(binary.BigEndian.Uint16(payload[len(payload)-2:])) != offset {
		t.Fatal("ACK total/offset/count mismatch")
	}
	frames := bytes.Join(snapshot.entries[offset:offset+returned], nil)
	if !bytes.Equal(payload[2:2+len(frames)], frames) {
		t.Fatal("ACK lost frozen frame bytes")
	}
	pos := 2 + len(frames)
	if len(payload) < pos+4 {
		t.Fatal("missing tune header")
	}
	count := int(binary.BigEndian.Uint16(payload[pos+2 : pos+4]))
	pos += 4
	result := map[uint16]uint16{}
	for i := 0; i < count; i++ {
		if len(payload) < pos+10 {
			t.Fatal("short tune record")
		}
		key := binary.BigEndian.Uint16(payload[pos:pos+2]) ^ binary.BigEndian.Uint16(payload[pos+2:pos+4])
		value := binary.BigEndian.Uint16(payload[pos+8:pos+10]) ^ binary.BigEndian.Uint16(payload[pos+2:pos+4])
		for _, known := range []uint16{1106, 1144, 1153, 1156, 1130} {
			if key == known {
				if _, duplicate := result[key]; duplicate {
					t.Fatal("duplicate Collab tune")
				}
				result[key] = value
			}
		}
		pos += 10
	}
	return returned, result
}

func TestCollabCommonRuntimeActualHandlerPagesFreezeRosterAndTunes(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	s := collabRuntimeSession(t, 400, catalog, cohort)
	before := collabRuntimeTime(t, "2099-01-03T23:59:59.999999999+09:00")
	after := collabRuntimeTime(t, "2099-01-04T00:00:00+09:00")
	clock, calls := before, 0
	s.server.questDeliveryClock = func() time.Time { calls++; return clock }
	var pinned *questDeliveryListSnapshot
	offset, pages := 0, 0
	for {
		handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 901, Offset: uint16(offset)})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || ack.AckHandle != 901 {
			t.Fatal("actual ACK failed", ack)
		}
		if pinned == nil {
			pinned = s.questDeliveryList
		}
		if pinned != s.questDeliveryList || len(pinned.entries) != 413 || !pinned.evaluatedAt.Equal(before) {
			t.Fatal("snapshot changed within pages")
		}
		returned, tunes := collabRuntimeDecodedTunes(t, ack.Payload, pinned, offset)
		if !reflect.DeepEqual(tunes, map[uint16]uint16{1144: 1}) {
			t.Fatal("later page mixed new NPC plan", tunes)
		}
		offset += returned
		pages++
		clock = after
		if offset == len(pinned.entries) {
			break
		}
		if returned == 0 || pages > 20 {
			t.Fatal("pages did not terminate")
		}
	}
	if pages < 2 || calls != pages {
		t.Fatal("clock seam not once per actual request", calls, pages)
	}
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 902})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || s.questDeliveryList == pinned || !s.questDeliveryList.evaluatedAt.Equal(after) {
		t.Fatal("new offset0 did not refresh both")
	}
	_, tunes := collabRuntimeDecodedTunes(t, ack.Payload, s.questDeliveryList, 0)
	if !reflect.DeepEqual(tunes, map[uint16]uint16{1153: 1}) {
		t.Fatal("new plan not encoded", tunes)
	}
	end := collabRuntimeTime(t, "2099-01-07T00:00:00+09:00")
	if s.deliveryFileAllowed("40221d0", end) || !s.deliveryFileAllowed("40221d0", before) {
		t.Fatal("cached listing extended entry time")
	}
	clock = end
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 903})
	ack = readAck(t, s)
	if ack.ErrorCode != 0 || len(s.questDeliveryList.entries) != 400 || len(s.questDeliveryList.collabTuneValues) != 0 {
		t.Fatal("expiry discarded ordinary roster or retained13")
	}
}

func collabAssertUnlocked(t *testing.T, s *Session, stage *Stage) {
	t.Helper()
	for name, mutex := range map[string]interface {
		TryLock() bool
		Unlock()
	}{"server": &s.server.Mutex, "session": &s.Mutex, "list": &s.questDeliveryListMu, "stage": &stage.RWMutex} {
		if !mutex.TryLock() {
			t.Fatalf("repository called under %s lock", name)
		}
		mutex.Unlock()
	}
}

type collabCommonTowerRepo struct {
	mockTowerRepo
	check func()
}

func (repo collabCommonTowerRepo) GetTowerData(uint32) (TowerData, error) {
	repo.check()
	return TowerData{Block1: 1, TRP: 1}, nil
}

func TestCollabCommonRuntimePreparationReadsOutsidePlayLocksAndReturnFailureIsLocal(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	s := collabRuntimeSession(t, 0, catalog, cohort)
	stage := NewStage("QsTest")
	var eventCalls, returnCalls, towerCalls int
	s.server.eventRepo = &collabCommonCallbackRepo{read: func() ([]EventQuest, error) {
		collabAssertUnlocked(t, s, stage)
		eventCalls++
		return []EventQuest{{ID: 1, QuestID: 50000, MaxPlayers: 4, Flags: -1}, {ID: 2, QuestID: towerQuestZone1, QuestType: 55, MaxPlayers: 4, Flags: -1}, {ID: 3, QuestID: 23352, QuestType: 28, Flags: -1}}, nil
	}}
	s.server.userRepo = returnCandidateReader{read: func(context.Context, uint32) (returnQuestExpiries, error) {
		collabAssertUnlocked(t, s, stage)
		returnCalls++
		return returnQuestExpiries{}, errors.New("Return account source unavailable")
	}}
	s.server.towerRepo = &collabCommonTowerRepo{check: func() { collabAssertUnlocked(t, s, stage); towerCalls++ }}
	for _, id := range []int{50000, towerQuestZone1} {
		s.server.questCache.Put(id, "en", make([]byte, 600))
	}
	snapshot, err := s.questDeliverySnapshot(0, collabRuntimeTime(t, "2099-01-02T00:00:00+09:00"))
	if err != nil || len(snapshot.entries) != 15 || eventCalls != 1 || returnCalls != 1 || towerCalls != 1 {
		t.Fatal("lock scope or Return failure discarded non-Return roster", err, eventCalls, returnCalls, towerCalls)
	}
}

func TestCollabCommonRuntimeOldRepositoryFailureCannotEraseNewCache(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	s := collabRuntimeSession(t, 0, catalog, cohort)
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	s.server.eventRepo = &collabCommonCallbackRepo{read: func() ([]EventQuest, error) {
		if reads.Add(1) == 1 {
			close(entered)
			<-release
			return nil, errors.New("slow old query failed")
		}
		return nil, nil
	}}
	at := collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")
	done := make(chan error, 1)
	go func() { _, err := s.questDeliverySnapshot(0, at); done <- err }()
	<-entered
	newest, err := s.questDeliverySnapshot(0, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if oldErr := <-done; oldErr == nil || !strings.Contains(oldErr.Error(), "superseded") {
		t.Fatal("old preparation unexpectedly committed", oldErr)
	}
	if s.questDeliveryList != newest || len(newest.entries) != 13 {
		t.Fatal("old failed query erased new cache")
	}
	old := s.beginQuestDeliveryPreparation(0)
	fresh := s.beginQuestDeliveryPreparation(0)
	last, err := s.questDeliverySnapshotPrepared(fresh, at)
	if err != nil {
		t.Fatal(err)
	}
	s.abortQuestDeliveryPreparation(old)
	s.abortQuestDeliveryPreparation(fresh)
	if s.questDeliveryList != last {
		t.Fatal("old or reused successful token erased current cache")
	}
}

func TestCollabCommonRuntimeOldAuthorityFailureCannotEraseNewHandlerSuccess(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	s := collabRuntimeSession(t, 0, catalog, cohort)
	s.server.questDeliveryClock = func() time.Time { return collabRuntimeTime(t, "2099-01-02T00:00:00+09:00") }
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	loader := gameplayAccessLoaderFunc(func(_ context.Context, user, char uint32, _ []int) (*QuestAccessSnapshot, error) {
		if reads.Add(1) == 1 {
			close(entered)
			<-release
			return nil, errors.New("slow authority failure")
		}
		return newQuestAccessSnapshot(user, char, time.Now(), nil, nil), nil
	})
	gameplay, err := NewQuestAccessGameplay(loader, []int{40142})
	if err != nil {
		t.Fatal(err)
	}
	s.server.questAccess = gameplay
	done := make(chan struct{})
	go func() { handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 701}); close(done) }()
	<-entered
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 702})
	newAck := readAck(t, s)
	newest := s.questDeliveryList
	if newAck.ErrorCode != 0 || newAck.AckHandle != 702 || newest == nil {
		t.Fatal("new handler did not publish")
	}
	close(release)
	<-done
	oldAck := readAck(t, s)
	if oldAck.ErrorCode == 0 || oldAck.AckHandle != 701 || s.questDeliveryList != newest || len(newest.entries) != 13 {
		t.Fatal("old authority failure erased new handler cache")
	}
}

func TestCollabCommonRuntimeManagedFailureInvalidatesOnlyOwnEpoch(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	s := collabRuntimeSession(t, 1, catalog, cohort)
	at := collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")
	if _, err := s.questDeliverySnapshot(0, at); err != nil {
		t.Fatal(err)
	}
	s.server.questCache = NewQuestCache(3600) // Missing managed body must fail whole ACK/list, never builtin recovery.
	if got, err := s.questDeliverySnapshot(0, at); err == nil || got != nil || s.questDeliveryList != nil {
		t.Fatal("managed frame failure returned a partial list")
	}
	for _, id := range runtimeExpectedCollabIDs {
		s.server.questCache.Put(id, "en", make([]byte, 600))
	}
	if got, err := s.questDeliverySnapshot(1, at); err != nil || got == nil || len(got.entries) != 13 {
		t.Fatal("orphan continuation resurrected failed list or lost valid managed rows", err)
	}
	// Ordinary missing frame still follows original unmanaged omission behavior.
	if _, err := s.questDeliverySnapshot(0, at); err != nil {
		t.Fatal("ordinary frame omission became global failure", err)
	}
}

func TestCollabCommonRuntimeMissingBindingNeverFallsBackToLegacyRandom(t *testing.T) {
	catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
	cohort := collabRuntimePrepare(t, catalog, nil)
	s := collabRuntimeSession(t, 0, catalog, cohort)
	s.server.commonCollab = nil
	s.server.worldType = 1
	s.server.collabEvent = collabNier
	at := collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")
	if got, err := s.encodeQuestDeliverySnapshotWithReturn(nil, at, false, deliveryTestEncode); err == nil || got != nil {
		t.Fatal("declared missing cohort used legacy fallback")
	}
	if s.deliveryFileAllowed("40221d0", at) || s.server.validateCommonCollabRuntime() == nil {
		t.Fatal("missing cohort still admitted source or passed startup validation")
	}
	s.server.commonCollab, s.server.questDelivery = cohort, nil
	if s.deliveryFileAllowed("40221d0", at) || s.server.validateCommonCollabRuntime() == nil {
		t.Fatal("stale nonnil cohort plus nil catalog inherited legacy-unmanaged allow")
	}
	s.server.questDelivery = catalog
	s.server.commonCollab = cohort
	s.server.worldName = "unconfigured"
	if s.server.validateCommonCollabRuntime() == nil {
		t.Fatal("wrong actual location accepted")
	}
}

func TestCollabCommonRuntimeDisabledOverrideDoesNotReviveBuiltin(t *testing.T) {
	fixture := collabRuntimeFixture(t)
	for _, raw := range fixture["quests"].([]any) {
		row := raw.(map[string]any)
		if int(row["quest_id"].(float64)) == 40215 {
			row["disabled"] = true
		}
	}
	catalog := collabRuntimeCatalog(t, fixture)
	override := EventQuest{ID: 77, QuestID: 40215, MaxPlayers: 1, CollabScope: "kaiji", Flags: 9}
	cohort := collabRuntimePrepare(t, catalog, []EventQuest{override})
	s := collabRuntimeSession(t, 0, catalog, cohort)
	at := collabRuntimeTime(t, "2099-01-02T00:00:00+09:00")
	seen := map[int]bool{}
	snapshot, err := s.encodeQuestDeliverySnapshotWithReturn([]EventQuest{override}, at, false, func(row EventQuest) ([]byte, error) { seen[row.QuestID] = true; return deliveryTestEncode(row) })
	if err != nil || seen[40215] || len(snapshot.entries) != 12 || s.deliveryFileAllowed("40215d0", at) {
		t.Fatal("disabled override was replaced by builtin or allowed fresh entry", err)
	}
}

func TestCollabCommonRuntimeFreshAuthorityPrecedesEnumerationClock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grant questAccessGrant
	}{
		{name: "owned_account_permanent", grant: questAccessGrant{accountPermanent: true}},
		{name: "owned_legacy_type9", grant: questAccessGrant{legacyExpiry: collabRuntimeTime(t, "2099-01-04T01:00:00+09:00")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := collabRuntimeCatalog(t, collabRuntimeFixture(t))
			cohort := collabRuntimePrepare(t, catalog, nil)
			s := collabRuntimeSession(t, 0, catalog, cohort)
			enteredAt := collabRuntimeTime(t, "2099-01-03T23:59:59.999999999+09:00")
			observedAt := collabRuntimeTime(t, "2099-01-04T00:00:00.000000001+09:00")
			evaluatedAt := observedAt.Add(time.Nanosecond)
			clock, clockCalls, loads := enteredAt, 0, 0
			s.server.questDeliveryClock = func() time.Time { clockCalls++; return clock }
			loader := gameplayAccessLoaderFunc(func(_ context.Context, user, char uint32, ids []int) (*QuestAccessSnapshot, error) {
				loads++
				if !reflect.DeepEqual(ids, []int{40142}) {
					t.Fatal("authority targets changed", ids)
				}
				// The real source observation occurs after handler entry; the slow
				// read also crosses the research Higanjima -> Nier boundary.
				clock = evaluatedAt
				return newQuestAccessSnapshot(user, char, observedAt, map[int]questAccessGrant{40142: tc.grant}, nil), nil
			})
			var err error
			s.server.questAccess, err = NewQuestAccessGameplay(loader, []int{40142})
			if err != nil {
				t.Fatal(err)
			}
			s.server.eventRepo = &mockEventRepo{eventQuests: []EventQuest{{ID: 1, QuestID: 40142, QuestType: QuestTypeSpecialTool, MaxPlayers: 4, Flags: -1}}}
			s.server.questCache.Put(40142, "en", make([]byte, 600))
			handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 991})
			ack := readAck(t, s)
			snapshot := s.questDeliveryList
			if ack.ErrorCode != 0 || ack.AckHandle != 991 || snapshot == nil || len(snapshot.entries) != 14 {
				t.Fatal("fresh authority enumerate failed", ack)
			}
			if !questAccessWiringEligibility(t, snapshot.entries[0]) || !questAccessWiringEligibility(t, ack.Payload[2:]) {
				t.Fatal("owned fresh authority serialized locked despite valid observation")
			}
			if loads != 1 || clockCalls != 1 || !snapshot.evaluatedAt.Equal(evaluatedAt) || !observedAt.After(enteredAt) || snapshot.evaluatedAt.Before(s.questAccessSnapshot.Load().ObservedAt()) {
				t.Fatal("enumeration clock did not follow the selected authority observation", loads, clockCalls, snapshot.evaluatedAt, observedAt)
			}
			_, tunes := collabRuntimeDecodedTunes(t, ack.Payload, snapshot, 0)
			if !reflect.DeepEqual(tunes, map[uint16]uint16{1153: 1}) {
				t.Fatal("post-read roster and Collab tuple used different boundary time", tunes)
			}
			if s.questAccessSnapshot.Load().Allows(40142, enteredAt) {
				t.Fatal("authority observation guard was weakened to grant before evidence")
			}
		})
	}
}

// No actual game, SQL, NAS, NPC ON/OFF, item delivery, client login or SAVE is
// exercised by this fixture. The2099 clock and generated600-byte bodies are
// transport/source-policy probes, not proof that these original quests run.
