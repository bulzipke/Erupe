package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"testing"
	"time"
)

func TestTowerMilestoneListingAndSharedCache(t *testing.T) {
	for _, c := range []struct {
		id       int
		category uint8
		mode     cfg.Mode
		want     int
	}{
		{21731, 55, cfg.ZZ, 21749}, {21746, 55, cfg.ZZ, 21750}, {21749, 55, cfg.ZZ, 21749},
		{21731, 1, cfg.ZZ, 21731}, {21749, 1, cfg.ZZ, 21749}, {21731, 55, cfg.G101, 21731},
	} {
		got := towerMilestoneListing(EventQuest{QuestID: c.id, QuestType: c.category}, c.mode)
		if got.QuestID != c.want {
			t.Fatalf("%+v => %d", c, got.QuestID)
		}
	}
	original := make([]byte, 380)
	binary.LittleEndian.PutUint32(original[40:], 320)
	binary.LittleEndian.PutUint32(original[320:], 352)
	copy(original[352:], "shared Road title")
	before := append([]byte(nil), original...)
	for _, id := range []int{21749, 21750} {
		out := towerMilestoneTitle(original, id)
		ptr := binary.LittleEndian.Uint32(out[320:])
		if ptr != uint32(len(original)) || out[len(out)-1] != 0 || !bytes.Equal(original, before) {
			t.Fatal("cache mutation or broken title pointer")
		}
		title := stringsupport.SJISToUTF8Lossy(out[ptr : len(out)-1])
		if !bytes.Contains([]byte(title), []byte("긴급조사의뢰")) {
			t.Fatalf("title=%q", title)
		}
	}
	for _, data := range [][]byte{nil, make([]byte, 20), bytes.Repeat([]byte{0xff}, 50)} {
		if !bytes.Equal(towerMilestoneTitle(data, 21749), data) {
			t.Fatal("malformed body rewritten")
		}
	}
}

func TestTowerTwoFloorMilestoneAndRoadSeparation(t *testing.T) {
	for _, qid := range []uint16{21749, 21750} {
		for _, road := range []bool{false, true} {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.EarthStatus = 21
			r := &mockTowerRepo{towerData: TowerData{Block1: 10, Block2: 10}}
			srv.towerRepo = r
			s := createMockSession(100, srv)
			stage := NewStage("sl1Qs100p0a0u0")
			stage.host = s
			stage.reservedClientSlots[s.charID] = true
			s.reservationStage = stage
			setup := questRunStagePayloadWithLevel(qid, 0, 10)
			if road {
				setup[0x4b] |= 2
			}
			stage.rawBinaryData[stageBinaryKey{1, 3}] = setup
			block := int32(1)
			if qid == 21750 {
				block = 2
			}
			handled, err := s.postTowerMilestone(&mhfpacket.MsgMhfPostTowerInfo{InfoType: 6, Unk1: 1, Unk6: block, Block1: 10})
			if err != nil || handled == road {
				t.Fatalf("qid=%d road=%v handled=%v err=%v", qid, road, handled, err)
			}
			var cache *TowerData
			if !s.allowsTowerQuest(EventQuest{QuestID: int(qid), QuestType: 1}, &cache) {
				t.Fatal("Road filtered by Tower progress")
			}
			if !road && s.allowsTowerQuest(EventQuest{QuestID: int(qid), QuestType: 55}, &cache) {
				t.Fatal("passed Tower milestone remains visible")
			}
			if r.towerData.Block1 != 10 || r.towerData.Block2 != 10 {
				t.Fatal("departure advanced a floor")
			}
		}
	}
}

type towerNoticeTestRepo struct {
	mockTowerRepo
	latest, seen int64
	history      []GemHistory
	err          error
}

func (r *towerNoticeTestRepo) GetGemHistory(uint32) ([]GemHistory, error) { return r.history, r.err }
func (r *towerNoticeTestRepo) GetGemNotice(uint32) (int64, int64, error) {
	return r.latest, r.seen, r.err
}
func (r *towerNoticeTestRepo) ReadGemNotice(_ uint32, n int64) error {
	if r.err == nil && n > r.seen {
		r.seen = n
	}
	return r.err
}
func TestTowerGiftNoticeOnlyClearsReturnedHistory(t *testing.T) {
	srv := createMockServer()
	r := &towerNoticeTestRepo{latest: 10, history: []GemHistory{{ID: 10, Gem: 1, Timestamp: time.Now(), Sender: "hunter"}}}
	srv.towerRepo = r
	s := createMockSession(100, srv)
	read := &mhfpacket.MsgMhfPostNotice{Unk0: 1, Unk2: 1}
	handleMsgMhfPostNotice(s, read)
	if settlementAckError(t, s) != 0 || r.seen != 0 {
		t.Fatal("unseen history marked read")
	}
	handleMsgMhfGetGemInfo(s, &mhfpacket.MsgMhfGetGemInfo{QueryType: 2})
	<-s.sendPackets
	r.latest = 11 // another gift between displaying history and acknowledging it
	handleMsgMhfPostNotice(s, read)
	if settlementAckError(t, s) != 0 || r.seen != 10 {
		t.Fatal("concurrent gift marked read")
	}
	handleMsgMhfGetNotice(s, &mhfpacket.MsgMhfGetNotice{Unk0: 1, Unk2: 1})
	p := (<-s.sendPackets).data
	if len(p) < 12 || binary.BigEndian.Uint32(p[len(p)-4:]) != 1 {
		t.Fatalf("new gift notification missing: %x", p)
	}
	r.err = errors.New("db failure")
	handleMsgMhfPostNotice(s, read)
	if settlementAckError(t, s) == 0 {
		t.Fatal("notice failure ACKed success")
	}
}

func TestTowerReadFailureDoesNotReturnZeroProgress(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{towerDataErr: errors.New("db unavailable")}
	s := createMockSession(100, srv)
	for _, kind := range []uint32{1, 2, 5} {
		handleMsgMhfGetTowerInfo(s, &mhfpacket.MsgMhfGetTowerInfo{AckHandle: 1, InfoType: kind})
		if settlementAckError(t, s) == 0 {
			t.Fatalf("IT%d failed read returned success", kind)
		}
	}
}
