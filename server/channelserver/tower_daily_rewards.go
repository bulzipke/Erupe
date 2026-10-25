package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// Native daily rewards use TINY_BIN 0/1/2, not PRESENT_BOX 260002.
// The receiver's row count is a byte and its communicator holds 0x400 bytes.
const towerDailyRewardPageSize = 255

type towerDailyItem struct {
	ItemID, Quantity uint16
}

type towerDailyAward struct {
	MissionSlot, ItemSlot int
	Item                  towerDailyItem
}

type TowerDailyRewardRow struct {
	ID   int64
	Item towerDailyItem
}

type TowerDailyRewardRepository interface {
	CommitTowerDailyRewards(uint32, int32, time.Time, []byte, []byte) error
	ListTowerDailyRewards(uint32, int32, int) ([]TowerDailyRewardRow, error)
}

type towerDailySubmission struct {
	Generation uint64
	EventID    int32
	Day        time.Time
	Before     []byte
	Progress   []byte
	Items      []towerDailyItem
}

type towerDailySession struct {
	sync.Mutex
	Submission *towerDailySubmission
	Offer      []TowerDailyRewardRow
	ClaimIDs   []int64
}

// The progress blob and item rows are native-memory data, hence little endian.
// Only newly completed slots are enqueued; existing completion flags are not
// backfilled. The mission table, not submitted item IDs, determines the award.
func towerDailyNewAwards(before, progress []byte, day time.Time) ([]towerDailyAward, error) {
	if len(progress) != towerDailyBinSize || !towerDailyStart(day).Equal(day) ||
		int64(binary.LittleEndian.Uint32(progress)) != day.Add(24*time.Hour).Unix() {
		return nil, errors.New("invalid Tower daily timetable")
	}
	bits := binary.LittleEndian.Uint32(progress[28:])
	if bits&^uint32(0x3f) != 0 {
		return nil, errors.New("invalid Tower daily completion flags")
	}
	var oldBits uint32
	if len(before) == towerDailyBinSize && bytes.Equal(before[:4], progress[:4]) {
		oldBits = binary.LittleEndian.Uint32(before[28:])
		if oldBits&^bits != 0 {
			return nil, errors.New("Tower daily completion regressed")
		}
	} else {
		before = nil
	}
	var awards []towerDailyAward
	for slot, mission := range towerDailyMissions(day) {
		goal := uint32(mission.Unk2)
		if mission.Unk1 == 2 {
			goal *= 100
		}
		value := binary.LittleEndian.Uint32(progress[4+slot*4:])
		complete := bits&(1<<slot) != 0
		if value > goal || complete != (value == goal) ||
			(before != nil && value < binary.LittleEndian.Uint32(before[4+slot*4:])) {
			return nil, errors.New("invalid Tower daily progress")
		}
		if complete && oldBits&(1<<slot) == 0 {
			for itemSlot, item := range []towerDailyItem{
				{mission.Reward1ID, uint16(mission.Reward1Quantity)},
				{mission.Reward2ID, uint16(mission.Reward2Quantity)},
			} {
				if item.ItemID != 0 && item.Quantity != 0 {
					awards = append(awards, towerDailyAward{slot, itemSlot, item})
				}
			}
		}
	}
	return awards, nil
}

func towerDailyItems(data []byte) ([]towerDailyItem, error) {
	if len(data)%4 != 0 || len(data) > towerDailyRewardPageSize*4 {
		return nil, errors.New("invalid Tower daily item list size")
	}
	items := make([]towerDailyItem, 0, len(data)/4)
	for len(data) != 0 {
		item := towerDailyItem{binary.LittleEndian.Uint16(data), binary.LittleEndian.Uint16(data[2:])}
		if item.ItemID == 0 || item.Quantity == 0 {
			return nil, errors.New("invalid Tower daily item")
		}
		items = append(items, item)
		data = data[4:]
	}
	return items, nil
}

func towerDailyItemBytes(rows []TowerDailyRewardRow) []byte {
	data := make([]byte, len(rows)*4)
	for i, row := range rows {
		binary.LittleEndian.PutUint16(data[i*4:], row.Item.ItemID)
		binary.LittleEndian.PutUint16(data[i*4+2:], row.Item.Quantity)
	}
	return data
}

func (s *Session) towerDailyResultContext() (TowerEvent, uint64, time.Time, error) {
	if s.server.erupeConfig.TowerRotation.Enabled {
		if _, err := s.server.towerEvent(TimeAdjusted()); err != nil {
			return TowerEvent{}, 0, time.Time{}, err
		}
	}
	event, valid := s.towerResultEvent()
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	gen := s.questWeaponGeneration
	if !valid || gen == 0 || s.towerProgressGeneration != gen || s.towerSettlementGeneration != gen {
		return event, 0, time.Time{}, errors.New("no settled Tower daily run")
	}
	day := s.towerMissionDayStart
	if s.towerDepartureGeneration == gen && !s.towerDepartureStarted.IsZero() {
		day = towerDailyStart(s.towerDepartureStarted)
	}
	return event, gen, day, nil
}

// A completion sends two POSTs together (PostDoubleTinyBinCommunicator):
// progress 0/1/1/1, followed by new reward rows 0/1/2/3. Stage completions
// until the second POST so a failed enqueue cannot persist a paid-looking flag.
func (s *Session) postTowerDailyProgress(data []byte) error {
	event, gen, day, err := s.towerDailyResultContext()
	if err != nil {
		return err
	}
	d := &s.towerDaily
	d.Lock()
	defer d.Unlock()
	if p := d.Submission; p != nil && p.Generation == gen {
		if p.EventID == event.ID && bytes.Equal(p.Progress, data) {
			return nil // The double communicator retries both POSTs after a lost ACK.
		}
		return errors.New("Tower daily result retry differs")
	}
	before, err := s.server.towerRepo.GetTowerDailyBin(s.charID)
	if err != nil {
		return err
	}
	awards, err := towerDailyNewAwards(before, data, day)
	if err != nil {
		return err
	}
	if len(awards) == 0 {
		return s.towerRoundRepo(event.ID).SaveTowerDailyBin(s.charID, data)
	}
	items := make([]towerDailyItem, len(awards))
	for i, award := range awards {
		items[i] = award.Item
	}
	d.Submission = &towerDailySubmission{gen, event.ID, day,
		bytes.Clone(before), bytes.Clone(data), items}
	return nil
}

func (s *Session) enqueueTowerDailyRewards(data []byte) error {
	items, err := towerDailyItems(data)
	if err != nil {
		return err
	}
	event, gen, _, err := s.towerDailyResultContext()
	if err != nil {
		return err
	}
	d := &s.towerDaily
	d.Lock()
	defer d.Unlock()
	p := d.Submission
	if p == nil || p.Generation != gen || p.EventID != event.ID || !slices.Equal(items, p.Items) {
		return errors.New("Tower daily award differs from completed missions")
	}
	repo, ok := s.towerRoundRepo(event.ID).(TowerDailyRewardRepository)
	if !ok {
		return errors.New("Tower daily reward repository unavailable")
	}
	return repo.CommitTowerDailyRewards(s.charID, event.ID, p.Day, p.Before, p.Progress)
}

func (s *Session) listTowerDailyRewards() ([]byte, error) {
	event, err := s.server.towerEvent(TimeAdjusted())
	if err != nil {
		return nil, err
	}
	d := &s.towerDaily
	d.Lock()
	defer d.Unlock()
	if len(d.ClaimIDs) != 0 {
		return nil, errors.New("Tower daily inventory save pending")
	}
	if s.server.erupeConfig.TowerRotation.Enabled && !event.Claimable(TimeAdjusted()) {
		d.Offer = nil
		return nil, nil
	}
	repo, ok := s.towerRoundRepo(event.ID).(TowerDailyRewardRepository)
	if !ok {
		return nil, errors.New("Tower daily reward repository unavailable")
	}
	rows, err := repo.ListTowerDailyRewards(s.charID, event.ID, towerDailyRewardPageSize)
	if err != nil {
		return nil, err
	}
	d.Offer = slices.Clone(rows)
	return towerDailyItemBytes(rows), nil
}

// The receiver puts fitting rows in its own inventory, then sends either
// 0/1/2/2 (all received) or 0/1/2/1 (rows that did not fit), alongside SAVEDATA.
// Consume just this offer's received rows with that save, preserving new rows
// enqueued after GET and the rows the hunter could not carry.
func (s *Session) stageTowerDailyClaims(op uint8, data []byte) error {
	if (op == 2 && len(data) != 0) || (op == 1 && len(data) == 0) {
		return errors.New("invalid Tower daily receipt")
	}
	remaining, err := towerDailyItems(data)
	if err != nil {
		return err
	}
	d := &s.towerDaily
	d.Lock()
	defer d.Unlock()
	if len(d.Offer) == 0 {
		return errors.New("no Tower daily reward offer")
	}
	var claims []int64
	next := 0
	for _, row := range d.Offer {
		if next < len(remaining) && row.Item == remaining[next] {
			next++
		} else {
			claims = append(claims, row.ID)
		}
	}
	if next != len(remaining) || (len(d.ClaimIDs) != 0 && !slices.Equal(d.ClaimIDs, claims)) {
		return errors.New("Tower daily remaining rows differ from offer")
	}
	d.ClaimIDs = claims
	return nil
}

func (s *Session) pendingTowerDailyClaims() []int64 {
	s.towerDaily.Lock()
	defer s.towerDaily.Unlock()
	return slices.Clone(s.towerDaily.ClaimIDs)
}

func (s *Session) completeTowerDailyClaims(ids []int64) {
	s.towerDaily.Lock()
	defer s.towerDaily.Unlock()
	if slices.Equal(s.towerDaily.ClaimIDs, ids) {
		s.towerDaily.ClaimIDs = nil
	}
}

func handleTowerDailyTinyPost(s *Session, pkt *mhfpacket.MsgMhfPostTinyBin) bool {
	if pkt.Unk0 != 0 || pkt.Unk1 != 1 || (pkt.Unk2 != 1 && pkt.Unk2 != 2) {
		return false
	}
	var err error
	switch {
	case s.server.towerRepo == nil || s.charID == 0:
		err = errors.New("Tower daily repository unavailable")
	case pkt.Unk2 == 1 && pkt.Unk3 == 1:
		err = s.postTowerDailyProgress(pkt.Data)
	case pkt.Unk2 == 2 && pkt.Unk3 == 3:
		err = s.enqueueTowerDailyRewards(pkt.Data)
	case pkt.Unk2 == 2 && (pkt.Unk3 == 1 || pkt.Unk3 == 2):
		err = s.stageTowerDailyClaims(pkt.Unk3, pkt.Data)
	default:
		err = fmt.Errorf("unsupported Tower daily operation %d/%d", pkt.Unk2, pkt.Unk3)
	}
	if err != nil {
		s.logger.Warn("Tower daily request failed", zap.Uint32("charID", s.charID), zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
	} else {
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
	}
	return true
}
