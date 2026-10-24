package channelserver

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
)

// Observe atomic repository calls and guard against a legacy split fallback.
type dailyPointIntegrityRepo struct {
	*mockCharacterRepo
	readDailyErr, adjustErr                  error
	readDailyCalls, adjustCalls, legacyCalls int
}

func (r *dailyPointIntegrityRepo) ReadTime(id uint32, column string, fallback time.Time) (time.Time, error) {
	r.legacyCalls++
	return r.mockCharacterRepo.ReadTime(id, column, fallback)
}

func (r *dailyPointIntegrityRepo) ResetDailyQuests(id uint32) error {
	r.legacyCalls++
	return r.mockCharacterRepo.ResetDailyQuests(id)
}

func (r *dailyPointIntegrityRepo) ReadEtcPoints(id uint32) (uint32, uint32, uint32, error) {
	r.legacyCalls++
	return r.mockCharacterRepo.ReadEtcPoints(id)
}

func (r *dailyPointIntegrityRepo) ReadInt(id uint32, column string) (int, error) {
	r.legacyCalls++
	return r.mockCharacterRepo.ReadInt(id, column)
}

func (r *dailyPointIntegrityRepo) SaveInt(id uint32, column string, value int) error {
	r.legacyCalls++
	return r.mockCharacterRepo.SaveInt(id, column, value)
}

func (r *dailyPointIntegrityRepo) ReadDailyEtcPoints(id uint32, now time.Time) (uint32, uint32, uint32, error) {
	r.readDailyCalls++
	if r.readDailyErr != nil {
		return 0, 0, 0, r.readDailyErr
	}
	return r.mockCharacterRepo.ReadDailyEtcPoints(id, now)
}

func (r *dailyPointIntegrityRepo) AdjustEtcPoint(id uint32, pointType uint8, delta int16) error {
	r.adjustCalls++
	if r.adjustErr != nil {
		return r.adjustErr
	}
	return r.mockCharacterRepo.AdjustEtcPoint(id, pointType, delta)
}

func dailyPointIntegritySession() (*Session, *dailyPointIntegrityRepo) {
	srv := createMockServer()
	r := &dailyPointIntegrityRepo{mockCharacterRepo: newMockCharacterRepo()}
	r.times["daily_time"] = TimeAdjusted().AddDate(0, 0, 1)
	srv.charRepo = r
	return createMockSession(100, srv), r
}

func assertDailyPointIntegrityAck(t *testing.T, s *Session, handle uint32, buffered, failed bool, payload []byte) {
	t.Helper()
	ack := licenseHandlerAck(t, s)
	wantCode := uint8(0)
	if failed {
		wantCode = 1
	}
	if ack.AckHandle != handle || ack.IsBufferResponse != buffered || ack.ErrorCode != wantCode || !bytes.Equal(ack.AckData, payload) {
		t.Fatalf("unexpected ACK: handle=%d buffered=%v code=%d payload=%x", ack.AckHandle, ack.IsBufferResponse, ack.ErrorCode, ack.AckData)
	}
	select {
	case <-s.sendPackets:
		t.Fatal("more than one ACK queued")
	default:
	}
}

func TestEtcPointGetFailureDoesNotAcknowledgeState(t *testing.T) {
	s, r := dailyPointIntegritySession()
	r.readDailyErr = errors.New("injected atomic daily read failure")
	handleMsgMhfGetEtcPoints(s, &mhfpacket.MsgMhfGetEtcPoints{AckHandle: 713})
	assertDailyPointIntegrityAck(t, s, 713, true, true, nil)
	if r.readDailyCalls != 1 || r.legacyCalls != 0 {
		t.Fatal("atomic read failed or fell back to split operations")
	}
}

func TestEtcPointUpdateFailureDoesNotAcknowledgeState(t *testing.T) {
	for pointType, column := range []string{"bonus_quests", "daily_quests", "promo_points"} {
		t.Run(column, func(t *testing.T) {
			s, r := dailyPointIntegritySession()
			r.ints[column] = 7
			r.adjustErr = errors.New("injected atomic point mutation failure")
			handleMsgMhfUpdateEtcPoint(s, &mhfpacket.MsgMhfUpdateEtcPoint{AckHandle: 719, PointType: uint8(pointType), Delta: -1})
			assertDailyPointIntegrityAck(t, s, 719, false, true, make([]byte, 4))
			if r.adjustCalls != 1 || r.legacyCalls != 0 || r.ints[column] != 7 {
				t.Fatal("atomic update failed or fell back to split operations")
			}
		})
	}
}

func TestEtcPointReplyCompatibility(t *testing.T) {
	t.Run("get_counts", func(t *testing.T) {
		s, r := dailyPointIntegritySession()
		r.etcBonusQuests, r.etcDailyQuests, r.etcPromoPoints = 3, 7, 11
		handleMsgMhfGetEtcPoints(s, &mhfpacket.MsgMhfGetEtcPoints{AckHandle: 727})
		bf := byteframe.NewByteFrame()
		bf.WriteUint8(3)
		bf.WriteUint32(3)
		bf.WriteUint32(7)
		bf.WriteUint32(11)
		assertDailyPointIntegrityAck(t, s, 727, true, false, bf.Data())
		if r.readDailyCalls != 1 || r.legacyCalls != 0 {
			t.Fatal("unexpected atomic read count or split fallback")
		}
	})
	for pointType, column := range []string{"bonus_quests", "daily_quests", "promo_points"} {
		t.Run(column, func(t *testing.T) {
			s, r := dailyPointIntegritySession()
			r.ints[column] = 3
			handleMsgMhfUpdateEtcPoint(s, &mhfpacket.MsgMhfUpdateEtcPoint{AckHandle: 733, PointType: uint8(pointType), Delta: -5})
			assertDailyPointIntegrityAck(t, s, 733, false, false, make([]byte, 4))
			if r.ints[column] != 0 || r.adjustCalls != 1 || r.legacyCalls != 0 {
				t.Fatal("legacy clamp changed")
			}
		})
	}
	t.Run("unsupported_type_legacy_noop", func(t *testing.T) {
		s, r := dailyPointIntegritySession()
		handleMsgMhfUpdateEtcPoint(s, &mhfpacket.MsgMhfUpdateEtcPoint{AckHandle: 739, PointType: 99, Delta: 1})
		assertDailyPointIntegrityAck(t, s, 739, false, false, make([]byte, 4))
		if r.adjustCalls != 0 || r.legacyCalls != 0 {
			t.Fatal("unsupported type accessed store")
		}
	})
}
