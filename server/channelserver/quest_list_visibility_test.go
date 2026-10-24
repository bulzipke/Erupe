package channelserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

type questListVisibilityRepo struct {
	mockEventRepo
	rows          []EventQuest
	reads, writes int
}

func (r *questListVisibilityRepo) GetEventQuests() ([]EventQuest, error) {
	r.reads++
	return append([]EventQuest(nil), r.rows...), nil
}

func (r *questListVisibilityRepo) UpdateEventQuestStartTimes([]EventQuestUpdate) error {
	r.writes++
	return nil
}

func questListVisibilitySession(t *testing.T, rows []EventQuest, manifest string) (*Session, *questListVisibilityRepo) {
	t.Helper()
	s := newEnumerateQuestSession(0)
	s.logger = zap.NewNop()
	s.userID = 77
	s.server.worldName, s.server.channelNumber = "Brave", 1
	s.server.questDelivery = deliveryTestSession(t, manifest).server.questDelivery
	s.server.erupeConfig.QuestDelivery.Enabled = true
	s.server.userRepo = returnWorldUserMock{allowed: false}
	r := &questListVisibilityRepo{rows: append([]EventQuest(nil), rows...)}
	s.server.eventRepo = r
	for _, row := range rows {
		// A valid cached native body, including its file ID and string pointers,
		// goes through the production event encoder and QCH2 handler below.
		frame := cacheTestFrame(uint16(row.QuestID), fmt.Sprintf("visibility original definition %05d", row.QuestID))
		s.server.questCache.Put(row.QuestID, "", frame[22:len(frame)-2])
	}
	return s, r
}

func questListVisibilityIDs(t *testing.T, snapshot *questDeliveryListSnapshot) map[int]bool {
	t.Helper()
	if snapshot == nil {
		t.Fatal("missing quest snapshot")
	}
	ids := make(map[int]bool, len(snapshot.entries))
	for _, frame := range snapshot.entries {
		if len(frame) < 70 {
			t.Fatal("short native frame")
		}
		id := int(binary.LittleEndian.Uint16(frame[68:70]))
		if id == 0 || ids[id] {
			t.Fatal("zero or repeated native quest ID", id)
		}
		ids[id] = true
	}
	return ids
}

func questListVisibilityNativePage(t *testing.T, s *Session, offset uint16) (int, int) {
	t.Helper()
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 810, Offset: offset})
	ack := licenseHandlerAck(t, s)
	if ack.AckHandle != 810 || ack.ErrorCode != 0 || !ack.IsBufferResponse || len(ack.AckData) < 6 {
		t.Fatalf("native enumeration failed: %#v", ack)
	}
	data := ack.AckData
	returned := int(binary.BigEndian.Uint16(data))
	total := int(binary.BigEndian.Uint16(data[len(data)-4:]))
	if binary.BigEndian.Uint16(data[len(data)-2:]) != offset || s.questDeliveryList == nil || total != len(s.questDeliveryList.entries) || int(offset)+returned > total {
		t.Fatal("invalid native page count, offset or snapshot")
	}
	want := bytes.Join(s.questDeliveryList.entries[int(offset):int(offset)+returned], nil)
	if len(data) < 2+len(want)+4 || !bytes.Equal(data[2:2+len(want)], want) {
		t.Fatal("native ACK changed the frozen frames")
	}
	return returned, total
}

func questListVisibilityProbe(t *testing.T, s *Session) *questClientCacheTransfer {
	t.Helper()
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 811, World: questCacheProbe, Offset: 512})
	ack := licenseHandlerAck(t, s)
	transfer := s.questClientCache
	if ack.AckHandle != 811 || ack.ErrorCode != 0 || transfer == nil || len(ack.AckData) < 56 || string(ack.AckData[:4]) != "QCH2" || !bytes.Equal(ack.AckData[12:44], transfer.root[:]) {
		t.Fatalf("cache probe failed: %#v", ack)
	}
	return transfer
}

func TestQuestListVisibilityOnlyHidesOrdinaryReceipts(t *testing.T) {
	rows := []EventQuest{
		{ID: 1, QuestID: 50018, QuestType: 18, MaxPlayers: 4, Flags: -1}, // false default
		{ID: 2, QuestID: 50028, QuestType: 28, MaxPlayers: 4, Flags: -1, ListHidden: true},
		{ID: 3, QuestID: 50054, QuestType: 54, MaxPlayers: 4, Flags: -1, ListHidden: true},
		{ID: 4, QuestID: 50118, QuestType: 18, MaxPlayers: 4, Flags: -1, ListHidden: true},
	}
	for i, kind := range []uint8{9, 24, 26, 30, 46, 55} {
		rows = append(rows, EventQuest{ID: uint32(5 + i), QuestID: 50100 + int(kind), QuestType: kind, MaxPlayers: 4, Flags: -1, ListHidden: true})
	}
	for _, mode := range []struct {
		name    string
		client  cfg.Mode
		enabled bool
		hidden  int
	}{
		{"expanded_ZZ", cfg.ZZ, true, 3},
		{"disabled_rollout", cfg.ZZ, false, 0},
		{"other_client", cfg.Z1, true, 0},
	} {
		t.Run(mode.name, func(t *testing.T) {
			s, repo := questListVisibilitySession(t, rows, `[]`)
			s.server.erupeConfig.RealClientMode = mode.client
			s.server.erupeConfig.QuestDelivery.Enabled = mode.enabled
			// Retain the original code-right check while exercising a flagged
			// type9 definition. Its listing must survive, still locked.
			var err error
			s.server.questAccess, err = NewQuestAccessGameplay(gameplayAccessLoaderFunc(func(_ context.Context, user, char uint32, _ []int) (*QuestAccessSnapshot, error) {
				return newQuestAccessSnapshot(user, char, time.Now().Add(-time.Second), nil, nil), nil
			}), []int{50109})
			if err != nil {
				t.Fatal(err)
			}
			_, total := questListVisibilityNativePage(t, s, 0)
			if total != len(rows)-mode.hidden || repo.reads != 1 || repo.writes != 0 {
				t.Fatalf("total=%d reads=%d writes=%d", total, repo.reads, repo.writes)
			}
			ids := questListVisibilityIDs(t, s.questDeliveryList)
			if !ids[50018] {
				t.Fatal("false/default visibility flag hid an ordinary quest")
			}
			for _, id := range []int{50028, 50054, 50118} {
				if ids[id] != (mode.hidden == 0) {
					t.Fatalf("ordinary quest%d visibility differs", id)
				}
			}
			for _, kind := range []uint8{9, 24, 26, 30, 46, 55} {
				if !ids[50100+int(kind)] {
					t.Fatal("flag hid a dedicated definition", kind)
				}
			}
			for _, frame := range s.questDeliveryList.entries {
				if binary.LittleEndian.Uint16(frame[68:70]) == 50109 && frame[11] != 0 {
					t.Fatal("list visibility granted an unowned code right")
				}
			}
		})
	}
}

func TestQuestListVisibilityPreservesFileAndDepartureAuthority(t *testing.T) {
	rows := []EventQuest{
		{ID: 1, QuestID: 53189, QuestType: 18, Flags: -1, ListHidden: true},
		{ID: 2, QuestID: 53211, QuestType: 18, Flags: -1, ListHidden: true},
	}
	s, _ := questListVisibilitySession(t, rows, `[{"quest_id":53189,"profiles":["normal"],"schedule":"always"},{"quest_id":53211,"disabled":true}]`)
	if _, total := questListVisibilityNativePage(t, s, 0); total != 0 {
		t.Fatal("list-hidden ordinary quests were transmitted")
	}
	if !s.allowsQuestDelivery(53189, deliveryTestTime()) || !s.deliveryFileAllowed("53189d0", deliveryTestTime()) {
		t.Fatal("hiding an event listing blocked its original file/departure authority")
	}
	if s.allowsQuestDelivery(53211, deliveryTestTime()) || s.deliveryFileAllowed("53211d0", deliveryTestTime()) {
		t.Fatal("list flag bypassed the disabled publication rule")
	}
}

func TestQuestListVisibilityReturnProviderDoesNotReinjectHiddenDBRow(t *testing.T) {
	rows := make([]EventQuest, 0, len(returnWorldQuestIDs))
	for i, id := range returnWorldQuestIDs {
		rows = append(rows, EventQuest{ID: uint32(i + 1), QuestID: id, QuestType: 28, MaxPlayers: 4, Flags: -1, ListHidden: i == 0})
	}
	s, repo := questListVisibilitySession(t, rows, `[]`)
	s.server.userRepo = returnWorldUserMock{allowed: true}
	_, total := questListVisibilityNativePage(t, s, 0)
	ids := questListVisibilityIDs(t, s.questDeliveryList)
	if total != len(returnWorldQuestIDs)-1 || ids[returnWorldQuestIDs[0]] || repo.reads != 1 {
		t.Fatal("Return provider reinserted a hidden DB definition", total, ids)
	}
	for _, id := range returnWorldQuestIDs[1:] {
		if !ids[id] {
			t.Fatal("unrelated Return quest disappeared", id)
		}
	}
}

func TestQuestListVisibilityRefreshesDBAndCacheWithoutChangingPagedSnapshot(t *testing.T) {
	const count, changed = 240, 200
	rows := make([]EventQuest, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, EventQuest{ID: uint32(i + 1), QuestID: 50000 + i, QuestType: 18, MaxPlayers: 4, Flags: -1})
	}
	s, repo := questListVisibilitySession(t, rows, `[]`)
	initialCache := questListVisibilityProbe(t, s)
	if len(initialCache.bodies) != count || repo.reads != 1 {
		t.Fatal("initial cache did not read the complete DB roster")
	}
	returned, total := questListVisibilityNativePage(t, s, 0)
	first := s.questDeliveryList
	if total != count || returned <= 0 || returned >= total || repo.reads != 2 {
		t.Fatal("test did not exercise a real multi-page native enumeration")
	}
	frozen := bytes.Join(first.entries, nil)
	repo.rows[changed].ListHidden = true
	_, total = questListVisibilityNativePage(t, s, uint16(returned))
	if total != count || s.questDeliveryList != first || repo.reads != 2 || !bytes.Equal(frozen, bytes.Join(first.entries, nil)) {
		t.Fatal("a DB update changed an enumeration already being paged")
	}

	hiddenCache := questListVisibilityProbe(t, s)
	hiddenList := s.questDeliveryList
	if len(hiddenCache.bodies) != count-1 || repo.reads != 3 || hiddenCache.root == initialCache.root || questListVisibilityIDs(t, hiddenList)[50000+changed] {
		t.Fatal("fresh probe retained stale visibility or an unchanged cache root")
	}
	warm := make(map[[32]byte]bool, len(initialCache.bodies))
	for _, body := range initialCache.bodies {
		warm[sha256.Sum256(body)] = true
	}
	for _, body := range hiddenCache.bodies {
		if !warm[sha256.Sum256(body)] {
			t.Fatal("a visibility-only DB update changed a retained quest definition")
		}
	}

	// The client may finish fetching the previous manifest after a DB edit.
	// Only a new probe can replace that transfer; its continuation stays frozen.
	repo.rows[changed].ListHidden = false
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 812, World: questCacheManifest})
	ack := licenseHandlerAck(t, s)
	if ack.ErrorCode != 0 || len(ack.AckData) < 56 || !bytes.Equal(ack.AckData[12:44], hiddenCache.root[:]) || s.questClientCache != hiddenCache || s.questDeliveryList != hiddenList || repo.reads != 3 {
		t.Fatal("a cache continuation reread DB visibility or switched transfer")
	}
	restored := questListVisibilityProbe(t, s)
	if repo.reads != 4 || repo.writes != 0 || len(restored.bodies) != count || restored.root != initialCache.root || !questListVisibilityIDs(t, s.questDeliveryList)[50000+changed] {
		t.Fatal("next probe did not restore the DB-selected original roster")
	}
	if len(hiddenCache.bodies) != count-1 || len(hiddenList.entries) != count-1 || !bytes.Equal(frozen, bytes.Join(first.entries, nil)) {
		t.Fatal("refresh mutated a prior native snapshot or cache transfer")
	}
}
