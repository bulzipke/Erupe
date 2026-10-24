package channelserver

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func deliveryTestSession(t *testing.T, quests string) *Session {
	t.Helper()
	catalog, err := questdelivery.Parse([]byte(`{"version":"test","profiles":[{"id":"normal","worlds":["Brave"],"channels":[1]}],"schedules":[{"id":"always"},{"id":"once","starts_at":"2026-10-23T12:00:00+09:00","ends_at":"2026-10-26T12:00:00+09:00"}],"quests":` + quests + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return &Session{server: &Server{erupeConfig: &cfg.Config{RealClientMode: cfg.ZZ},
		worldName: "Brave", channelNumber: 1, worldType: 1, questDelivery: catalog,
		eventRepo: &mockEventRepo{}, questCache: NewQuestCache(300)}, logger: zap.NewNop(), clientLang: "en"}
}

func deliveryTestTime() time.Time { return time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC) }

func deliveryTestEncode(eq EventQuest) ([]byte, error) {
	data := make([]byte, questDataMinLen)
	binary.LittleEndian.PutUint32(data, uint32(eq.QuestID))
	return data, nil
}

type deliveryCountingRepo struct {
	mockEventRepo
	reads, writes int
}

func (r *deliveryCountingRepo) GetEventQuests() ([]EventQuest, error) {
	r.reads++
	return r.mockEventRepo.GetEventQuests()
}

func (r *deliveryCountingRepo) UpdateEventQuestStartTimes([]EventQuestUpdate) error {
	r.writes++
	return nil
}

func TestDeliveryEnumerationDoesNotWriteScheduleState(t *testing.T) {
	s := deliveryTestSession(t, `[]`)
	r := &deliveryCountingRepo{mockEventRepo: mockEventRepo{eventQuests: []EventQuest{{QuestID: 53142, ActiveDays: 3, InactiveDays: 1, StartTime: deliveryTestTime().Add(-96 * 1000 * time.Hour), Flags: -1}}}}
	s.server.eventRepo = r
	s.server.questCache.Put(53142, "en", make([]byte, 400))
	for _, offset := range []uint16{0, 1, 2, 0} {
		if _, err := s.questDeliverySnapshot(offset, deliveryTestTime()); err != nil {
			t.Fatal(err)
		}
	}
	if r.reads != 2 || r.writes != 0 {
		t.Fatalf("reads=%d writes=%d", r.reads, r.writes)
	}
}

func TestDeliverySnapshotFiltersBeforeCapacityAndKeepsLegacy(t *testing.T) {
	s := deliveryTestSession(t, `[{"quest_id":53140,"profiles":["normal"],"schedule":"once"},{"quest_id":53141,"disabled":true}]`)
	rows := make([]EventQuest, 0, 520)
	for i := 0; i < 518; i++ {
		rows = append(rows, EventQuest{QuestID: 53141})
	}
	rows = append(rows, EventQuest{QuestID: 53140}, EventQuest{QuestID: 53142})
	got, err := s.encodeQuestDeliverySnapshot(rows, deliveryTestTime(), deliveryTestEncode)
	if err != nil || len(got.entries) != 2 {
		t.Fatalf("filtered snapshot: %v, %v", got, err)
	}
	s.server.worldName = "Grace"
	got, err = s.encodeQuestDeliverySnapshot(rows, deliveryTestTime(), deliveryTestEncode)
	if err != nil || len(got.entries) != 1 {
		t.Fatalf("wrong-world snapshot: %v, %v", got, err)
	}
}

func TestDeliveryCapacityNeverSilentlyTruncates(t *testing.T) {
	s := deliveryTestSession(t, `[]`)
	s.server.erupeConfig.EventQuestCapacity = 513
	rows := make([]EventQuest, 514)
	for i := range rows {
		rows[i] = EventQuest{QuestID: 1000 + i}
	}
	for _, count := range []int{512, 513} {
		got, err := s.encodeQuestDeliverySnapshot(rows[:count], deliveryTestTime(), deliveryTestEncode)
		if err != nil || len(got.entries) != count {
			t.Fatalf("%d: count=%v err=%v", count, got, err)
		}
	}
	if got, err := s.encodeQuestDeliverySnapshot(rows, deliveryTestTime(), deliveryTestEncode); err == nil || got != nil || !strings.Contains(err.Error(), "514 valid quests") || !strings.Contains(err.Error(), "by 1") {
		t.Fatalf("514 must reject the entire snapshot and report the count: %v", err)
	}
	more := append(append([]EventQuest(nil), rows...), EventQuest{QuestID: 1514}, EventQuest{QuestID: 1514}, EventQuest{QuestID: 1515})
	_, err := s.encodeQuestDeliverySnapshot(more, deliveryTestTime(), func(eq EventQuest) ([]byte, error) {
		if eq.QuestID == 1515 {
			return nil, errors.New("missing body")
		}
		return deliveryTestEncode(eq)
	})
	if err == nil || !strings.Contains(err.Error(), "515 valid quests") || !strings.Contains(err.Error(), "by 2") {
		t.Fatalf("overflow must count valid unique IDs beyond the first extra row: %v", err)
	}
	rows[513] = rows[0]
	got, err := s.encodeQuestDeliverySnapshot(rows, deliveryTestTime(), deliveryTestEncode)
	if err != nil || len(got.entries) != 513 {
		t.Fatal("duplicate must not consume another slot", err)
	}
}

func TestDeliverySnapshotPagesStayFrozenAndFailedRefreshClears(t *testing.T) {
	s := deliveryTestSession(t, `[{"quest_id":53140,"profiles":["normal"],"schedule":"once"}]`)
	repo := s.server.eventRepo.(*mockEventRepo)
	repo.eventQuests = []EventQuest{{ID: 1, QuestID: 53140, Flags: -1}, {ID: 2, QuestID: 53142, Flags: -1}}
	for _, id := range []int{53140, 53142} {
		s.server.questCache.Put(id, "en", make([]byte, 400))
	}
	first, err := s.questDeliverySnapshot(0, deliveryTestTime())
	if err != nil || len(first.entries) != 2 {
		t.Fatalf("first page: %v, %v", first, err)
	}
	repo.eventQuests = nil
	late := deliveryTestTime().Add(96 * time.Hour)
	page, err := s.questDeliverySnapshot(1, late)
	if err != nil || page != first {
		t.Fatal("later page changed the serialized enumeration", err)
	}
	if s.deliveryFileAllowed("53140d0", late) {
		t.Fatal("cached listing must not permit a fresh expired file request")
	}
	repo.eventQuestErr = errors.New("database unavailable")
	if _, err := s.questDeliverySnapshot(0, late); err == nil || s.questDeliveryList != nil {
		t.Fatal("failed refresh retained old snapshot")
	}
	if _, err := s.questDeliverySnapshot(1, late); err == nil {
		t.Fatal("follow-up page reused stale snapshot")
	}
	repo.eventQuestErr = nil
	got, err := s.questDeliverySnapshot(0, late)
	if err != nil || len(got.entries) != 0 {
		t.Fatal("fresh enumeration not rebuilt", err)
	}
}

func TestDeliveryFileAndTowerAliases(t *testing.T) {
	s := deliveryTestSession(t, `[{"quest_id":21731,"disabled":true},{"quest_id":53140,"profiles":["normal"],"schedule":"once"}]`)
	for _, id := range []int{21731, 21749} {
		if s.allowsQuestDelivery(id, deliveryTestTime()) {
			t.Fatalf("tower alias %d bypassed policy", id)
		}
	}
	for _, name := range []string{"53140d0", "quests/53140n2", "53140d12", "53140D0"} {
		id, ok := deliveryQuestIDFromFilename(name)
		if !ok || id != 53140 {
			t.Fatalf("valid quest filename %q: %d %v", name, id, ok)
		}
		if !s.deliveryFileAllowed(name, deliveryTestTime()) || s.deliveryFileAllowed(name, deliveryTestTime().Add(96*time.Hour)) {
			t.Fatalf("file window %q", name)
		}
	}
	for _, name := range []string{"00000d0", "99999d0", "53140x0", "53140d0.bin", "5314d0"} {
		if _, ok := deliveryQuestIDFromFilename(name); ok {
			t.Fatalf("accepted %q", name)
		}
	}
	if !s.deliveryFileAllowed("53142d0", deliveryTestTime()) {
		t.Fatal("unmanaged quest lost its legacy access")
	}
	s.server.questDelivery = nil
	if !s.deliveryFileAllowed("21749d0", deliveryTestTime()) {
		t.Fatal("disabled feature changed legacy file behavior")
	}
}

func TestDeliveryChecksSeasonResolvedFile(t *testing.T) {
	base := deliveryTestSession(t, `[{"quest_id":53140,"disabled":true}]`)
	s := createTestSession(&MockCryptConn{})
	s.server = base.server
	s.server.erupeConfig.GameplayOptions.SeasonOverride = true
	s.server.erupeConfig.BinPath = t.TempDir()
	dir := filepath.Join(s.server.erupeConfig.BinPath, "quests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "53140d0.bin"), []byte{1, 2, 3}, 0644); err != nil {
		t.Fatal(err)
	}
	pkt := &mhfpacket.MsgSysGetFile{AckHandle: 42, Filename: "53140x0"}
	handleMsgSysGetFile(s, pkt)
	if pkt.Filename != "53140d0" {
		t.Fatalf("fixture did not resolve to a quest: %q", pkt.Filename)
	}
	if got := parseAckFromChannel(t, s); got != 1 {
		t.Fatalf("season-converted hidden file returned ACK %d", got)
	}
}

func TestLegacyDeliveryScheduleHasImmutableBoundaries(t *testing.T) {
	start := deliveryTestTime().Add(123 * time.Nanosecond)
	eq := EventQuest{StartTime: start, ActiveDays: 3, InactiveDays: 1}
	for _, c := range []struct {
		at   time.Time
		want bool
	}{
		{start.Add(-time.Nanosecond), false}, {start, true}, {start.Add(72*time.Hour - time.Nanosecond), true},
		{start.Add(72 * time.Hour), false}, {start.Add(96 * time.Hour), true}, {start.Add(96 * 1000 * time.Hour), true},
	} {
		if got := legacyQuestActiveAt(eq, c.at); got != c.want {
			t.Fatalf("%s got %v want %v", c.at, got, c.want)
		}
	}
	if eq.StartTime != start {
		t.Fatal("schedule evaluation changed the anchor")
	}
	eq.InactiveDays = -1
	if legacyQuestActiveAt(eq, start) {
		t.Fatal("negative interval accepted")
	}
}
