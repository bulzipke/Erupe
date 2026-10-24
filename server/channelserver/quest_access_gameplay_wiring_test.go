package channelserver

import (
	"context"
	"errors"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// The serialized event row starts with ID:u32, reserved:u32, reserved:u8,
// max_players:u8, quest_type:u8, then the native eligibility boolean.
const questAccessWiringFlagOffset = 4 + 4 + 1 + 1 + 1

func questAccessWiringEligibility(t *testing.T, data []byte) bool {
	t.Helper()
	if len(data) <= questAccessWiringFlagOffset {
		t.Fatalf("event row is too short: %d", len(data))
	}
	if data[questAccessWiringFlagOffset] > 1 {
		t.Fatalf("invalid eligibility boolean: %d", data[questAccessWiringFlagOffset])
	}
	return data[questAccessWiringFlagOffset] == 1
}

func TestQuestAccessGameplayWiringPageZeroRefreshFailureClearsCachedList(t *testing.T) {
	fail := true
	loads := 0
	loader := gameplayAccessLoaderFunc(func(_ context.Context, user, char uint32, _ []int) (*QuestAccessSnapshot, error) {
		loads++
		if fail {
			return nil, errors.New("authority source unavailable")
		}
		return newQuestAccessSnapshot(user, char, time.Now().Add(-time.Second), map[int]questAccessGrant{}, nil), nil
	})
	s := gameplayAccessTestSession(t, loader, 40142)
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.questCache = NewQuestCache(3600)
	s.server.questCache.Put(40142, "", make([]byte, 600))
	repo := &deliveryCountingRepo{mockEventRepo: mockEventRepo{eventQuests: []EventQuest{
		{ID: 1, QuestID: 40142, QuestType: QuestTypeSpecialTool, MaxPlayers: 4, Flags: -1},
	}}}
	s.server.eventRepo = repo
	oldAuthority := newQuestAccessSnapshot(s.userID, s.charID, time.Now().Add(-time.Minute), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	s.questAccessSnapshot.Store(oldAuthority)
	oldList := &questDeliveryListSnapshot{entries: [][]byte{{0xaa, 0xbb}}}
	s.questDeliveryList = oldList

	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 701, Offset: 0})
	if loads != 1 || repo.reads != 0 || s.questDeliveryList != nil {
		t.Fatalf("failed refresh loads=%d quest_reads=%d cached_list=%p", loads, repo.reads, s.questDeliveryList)
	}
	if len(s.sendPackets) != 1 {
		t.Fatalf("failed refresh queued %d responses, want one", len(s.sendPackets))
	}
	ack := readAck(t, s)
	if ack.AckHandle != 701 || !ack.IsBufferResponse || ack.ErrorCode != 1 || len(ack.Payload) != 0 {
		t.Fatalf("failed refresh did not send an empty failure buffer ACK: %#v", ack)
	}

	// A continuation after recovery must build current data. It cannot resurrect
	// the previous successful enumeration or its old allowed eligibility flag.
	fail = false
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 702, Offset: 1})
	ack = readAck(t, s)
	if ack.AckHandle != 702 || !ack.IsBufferResponse || ack.ErrorCode != 0 {
		t.Fatalf("recovery continuation failed: %#v", ack)
	}
	if loads != 2 || repo.reads != 1 || s.questDeliveryList == nil || s.questDeliveryList == oldList || len(s.questDeliveryList.entries) != 1 {
		t.Fatalf("continuation reused stale enumeration: loads=%d reads=%d cached=%p", loads, repo.reads, s.questDeliveryList)
	}
	if questAccessWiringEligibility(t, s.questDeliveryList.entries[0]) {
		t.Fatal("recovery reused the old allowed flag instead of current denied authority")
	}
}

func TestQuestAccessGameplayWiringEventRowUsesPassedSnapshot(t *testing.T) {
	loads := 0
	s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		loads++
		return nil, errors.New("row encoding must not perform an authority read")
	}), 40142)
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.questCache = NewQuestCache(3600)
	s.server.questCache.Put(40142, "", make([]byte, 600))
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	allowed := newQuestAccessSnapshot(s.userID, s.charID, at.Add(-time.Second), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	denied := newQuestAccessSnapshot(s.userID, s.charID, at.Add(-time.Second), map[int]questAccessGrant{}, nil)
	future := newQuestAccessSnapshot(s.userID, s.charID, at.Add(time.Nanosecond), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	foreign := newQuestAccessSnapshot(s.userID+1, s.charID, at.Add(-time.Second), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	for _, tc := range []struct {
		name    string
		current *QuestAccessSnapshot
		passed  *QuestAccessSnapshot
		want    bool
	}{
		{"passed allowed overrides current denied", denied, allowed, true},
		{"passed denied overrides current allowed", allowed, denied, false},
		{"passed nil is denied despite current allowed", allowed, nil, false},
		{"passed future cannot backdate", allowed, future, false},
		{"passed foreign identity cannot grant", allowed, foreign, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.questAccessSnapshot.Store(tc.current)
			data, err := makeEventQuestWithAccess(s, EventQuest{ID: 9, QuestID: 40142, QuestType: QuestTypeSpecialTool, MaxPlayers: 4, Flags: -1}, tc.passed, at)
			if err != nil {
				t.Fatal(err)
			}
			if got := questAccessWiringEligibility(t, data); got != tc.want {
				t.Fatalf("event eligibility=%t want=%t", got, tc.want)
			}
		})
	}
	if loads != 0 {
		t.Fatalf("row encoding performed %d authority reads", loads)
	}
}

func TestQuestAccessGameplayWiringUnknownLiveTypeNineStaysLocked(t *testing.T) {
	s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		t.Fatal("unknown Type9 encoding performed a database read")
		return nil, nil
	}), 40142)
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.questCache = NewQuestCache(3600)
	s.server.questCache.Put(40143, "", make([]byte, 600))
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	// Even forged/stale extra authority cannot expand the frozen server roster.
	extra := newQuestAccessSnapshot(s.userID, s.charID, at.Add(-time.Second), map[int]questAccessGrant{40143: {accountPermanent: true}}, nil)
	s.questAccessSnapshot.Store(extra)
	for _, tc := range []struct {
		name   string
		typeID uint8
		want   bool
	}{
		{"new Type9 is locked", QuestTypeSpecialTool, false},
		{"unprotected regular row keeps legacy eligibility", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := makeEventQuestWithAccess(s, EventQuest{ID: 10, QuestID: 40143, QuestType: tc.typeID, MaxPlayers: 4, Flags: -1}, extra, at)
			if err != nil {
				t.Fatal(err)
			}
			if got := questAccessWiringEligibility(t, data); got != tc.want {
				t.Fatalf("eligibility=%t want=%t", got, tc.want)
			}
		})
	}
}
