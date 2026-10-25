package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func towerDailyTestProgress(day time.Time, bits uint32) []byte {
	data := make([]byte, towerDailyBinSize)
	binary.LittleEndian.PutUint32(data, uint32(day.Add(24*time.Hour).Unix()))
	for i, mission := range towerDailyMissions(day) {
		if bits&(1<<i) != 0 {
			goal := uint32(mission.Unk2)
			if mission.Unk1 == 2 {
				goal *= 100
			}
			binary.LittleEndian.PutUint32(data[4+i*4:], goal)
		}
	}
	binary.LittleEndian.PutUint32(data[28:], bits)
	return data
}

type towerDailyTestRepo struct {
	mockTowerRepo
	rows      []TowerDailyRewardRow
	commitErr error
	listErr   error
	commits   int
}

func (r *towerDailyTestRepo) CommitTowerDailyRewards(_ uint32, _ int32, day time.Time, before, data []byte) error {
	r.commits++
	if r.commitErr != nil {
		return r.commitErr
	}
	awards, err := towerDailyNewAwards(before, data, day)
	if err != nil {
		return err
	}
	if !bytes.Equal(r.savedBin, data) {
		for _, award := range awards {
			r.rows = append(r.rows, TowerDailyRewardRow{int64(len(r.rows) + 1), award.Item})
		}
	}
	r.savedBin = bytes.Clone(data)
	return nil
}

func (r *towerDailyTestRepo) ListTowerDailyRewards(_ uint32, _ int32, limit int) ([]TowerDailyRewardRow, error) {
	return slices.Clone(r.rows[:min(len(r.rows), limit)]), r.listErr
}

func towerDailyTestSession() (*Session, *towerDailyTestRepo, time.Time) {
	repo := &towerDailyTestRepo{}
	server := createMockServer()
	server.towerRepo = repo
	s := createMockSession(42, server)
	s.questWeaponGeneration, s.towerProgressGeneration, s.towerSettlementGeneration = 1, 1, 1
	s.towerMissionDayStart = towerDailyStart(TimeAdjusted())
	return s, repo, s.towerMissionDayStart
}

func towerDailyTestPost(t *testing.T, s *Session, bin, op uint8, data []byte, fail bool) {
	t.Helper()
	handleMsgMhfPostTinyBin(s, &mhfpacket.MsgMhfPostTinyBin{
		AckHandle: 1, Unk1: 1, Unk2: bin, Unk3: op, Data: data,
	})
	code, _ := parseAckSimpleValue(t, (<-s.sendPackets).data)
	if (code != 0) != fail {
		t.Fatalf("POST 0/1/%d/%d: error=%d, want failure=%v", bin, op, code, fail)
	}
}

func TestTowerDailyNewAwardsValidateAndExcludeHistory(t *testing.T) {
	day := towerDailyStart(time.Date(2026, 10, 25, 17, 0, 0, 0, divaLocation))
	before := towerDailyTestProgress(day, 1)
	after := towerDailyTestProgress(day, 3)
	awards, err := towerDailyNewAwards(before, after, day)
	if err != nil || len(awards) != 1 || awards[0].MissionSlot != 1 {
		t.Fatalf("new slot only: %+v %v", awards, err)
	}
	if awards, err = towerDailyNewAwards(after, after, day); err != nil || len(awards) != 0 {
		t.Fatalf("old completed tasks were backfilled: %+v %v", awards, err)
	}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"truncated", func(b []byte) []byte { return b[:31] }},
		{"wrong timetable", func(b []byte) []byte { b[0]++; return b }},
		{"invalid flags", func(b []byte) []byte { b[28] |= 0x80; return b }},
		{"completion without progress", func(b []byte) []byte { clear(b[8:12]); return b }},
		{"counter overflow", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[8:], ^uint32(0)); return b }},
		{"regressed completion", func(b []byte) []byte { b[28] &^= 1; return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := towerDailyNewAwards(before, tc.edit(bytes.Clone(after)), day); err == nil {
				t.Fatal("invalid progress accepted")
			}
		})
	}
	// Crossing noon resets the baseline, not the old day's pending items.
	next := day.Add(24 * time.Hour)
	if got, err := towerDailyNewAwards(after, towerDailyTestProgress(next, 1), next); err != nil || len(got) != 1 {
		t.Fatalf("new timetable: %+v %v", got, err)
	}
}

func TestTowerDailyNativeDoublePostCommitAndRetry(t *testing.T) {
	s, repo, day := towerDailyTestSession()
	progress := towerDailyTestProgress(day, 3)
	towerDailyTestPost(t, s, 1, 1, progress, false)
	if len(repo.savedBin) != 0 || len(repo.rows) != 0 {
		t.Fatal("completion committed before its award request")
	}
	items := make([]TowerDailyRewardRow, 0)
	for _, m := range towerDailyMissions(day)[:2] {
		items = append(items, TowerDailyRewardRow{Item: towerDailyItem{m.Reward1ID, uint16(m.Reward1Quantity)}})
	}
	data := towerDailyItemBytes(items)
	wrong := bytes.Clone(data)
	wrong[0] ^= 1
	towerDailyTestPost(t, s, 2, 3, wrong, true)
	if repo.commits != 0 {
		t.Fatal("forged item list reached storage")
	}
	repo.commitErr = errors.New("database unavailable")
	towerDailyTestPost(t, s, 2, 3, data, true)
	if len(repo.savedBin) != 0 {
		t.Fatal("failed enqueue persisted completion flags")
	}
	repo.commitErr = nil
	towerDailyTestPost(t, s, 1, 1, progress, false)
	towerDailyTestPost(t, s, 2, 3, data, false)
	towerDailyTestPost(t, s, 1, 1, progress, false)
	towerDailyTestPost(t, s, 2, 3, data, false)
	if !bytes.Equal(repo.savedBin, progress) || len(repo.rows) != 2 {
		t.Fatal("lost-ACK retry changed or duplicated the award")
	}
	s.questWeaponGeneration++
	towerDailyTestPost(t, s, 2, 3, data, true)
}

func TestTowerDailyNativeReceiptKeepsUnreceivedAndNewRows(t *testing.T) {
	s, repo, _ := towerDailyTestSession()
	repo.rows = []TowerDailyRewardRow{{1, towerDailyItem{0x2b99, 2}}, {2, towerDailyItem{0x2b96, 1}}}
	handleMsgMhfGetTinyBin(s, &mhfpacket.MsgMhfGetTinyBin{AckHandle: 1, Unk1: 1, Unk2: 2})
	_, code, data := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || !bytes.Equal(data, []byte{0x99, 0x2b, 2, 0, 0x96, 0x2b, 1, 0}) {
		t.Fatalf("native pending rows: code=%d data=%x", code, data)
	}
	// Another result can enqueue items while this receiver is open.
	repo.rows = append(repo.rows, TowerDailyRewardRow{3, towerDailyItem{0x2ba5, 1}})
	towerDailyTestPost(t, s, 2, 1, []byte{0xff, 0xff, 1, 0}, true)
	towerDailyTestPost(t, s, 2, 1, data[4:], false)
	if !slices.Equal(s.pendingTowerDailyClaims(), []int64{1}) {
		t.Fatalf("receipt consumed unreceived or newly enqueued items: %v", s.pendingTowerDailyClaims())
	}
	handleMsgMhfGetTinyBin(s, &mhfpacket.MsgMhfGetTinyBin{AckHandle: 1, Unk1: 1, Unk2: 2})
	_, code, _ = parseAckBufData(t, (<-s.sendPackets).data)
	if code == 0 {
		t.Fatal("inventory can be re-granted before its save commits")
	}
	// A retry preserves the staged receipt. A different remainder cannot replace it.
	towerDailyTestPost(t, s, 2, 1, data[4:], false)
	towerDailyTestPost(t, s, 2, 2, nil, true)
	s.completeTowerDailyClaims([]int64{1})
	if len(s.pendingTowerDailyClaims()) != 0 {
		t.Fatal("committed snapshot was retained")
	}
}

func TestTowerDailyNativeRejectsUnboundAndMalformedRequests(t *testing.T) {
	s, _, day := towerDailyTestSession()
	towerDailyTestPost(t, s, 2, 3, []byte{1, 0, 1, 0}, true)
	towerDailyTestPost(t, s, 2, 2, nil, true)
	towerDailyTestPost(t, s, 1, 1, []byte{1}, true)
	s.towerProgressGeneration = 0
	towerDailyTestPost(t, s, 1, 1, towerDailyTestProgress(day, 1), true)
	for _, data := range [][]byte{{1}, {0, 0, 1, 0}, {1, 0, 0, 0}, make([]byte, 1024)} {
		if _, err := towerDailyItems(data); err == nil {
			t.Fatalf("malformed native item list accepted: %x", data)
		}
	}
}

func TestTowerDailyNativeListBoundAndReadFailure(t *testing.T) {
	s, repo, _ := towerDailyTestSession()
	for i := 0; i < 300; i++ {
		repo.rows = append(repo.rows, TowerDailyRewardRow{int64(i + 1), towerDailyItem{1, 1}})
	}
	data, err := s.listTowerDailyRewards()
	if err != nil || len(data) != 255*4 {
		t.Fatalf("native byte-count limit: %d %v", len(data), err)
	}
	repo.listErr = errors.New("read failed")
	handleMsgMhfGetTinyBin(s, &mhfpacket.MsgMhfGetTinyBin{AckHandle: 1, Unk1: 1, Unk2: 2})
	_, code, _ := parseAckBufData(t, (<-s.sendPackets).data)
	if code == 0 {
		t.Fatal("failed listing was acknowledged as an empty queue")
	}
}

func TestTowerDailyClaimsCommitOnlyWithClientSave(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			repo := newMockCharacterRepo()
			repo.loadSaveDataID, repo.loadSaveDataName = 42, "Hunter"
			repo.strings["name"] = "Hunter"
			repo.loadSaveDataData, _ = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
			server.charRepo = repo
			s := createMockSession(42, server)
			s.towerDaily.ClaimIDs = []int64{7}
			// A safety save must not consume a client inventory receipt.
			save, err := GetCharacterSaveData(s, 42)
			if err != nil {
				t.Fatal(err)
			}
			if err = save.Save(s); err != nil {
				t.Fatal(err)
			}
			if len(repo.saveAtomicParams[0].TowerDailyRewardIDs) != 0 || len(s.pendingTowerDailyClaims()) != 1 {
				t.Fatal("server-side save consumed client receipts")
			}
			if fail {
				repo.saveAtomicErr = errors.New("save failed")
			}
			handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 9, RawDataPayload: characterSaveBlob(t, "Hunter")})
			if ack := readAck(t, s); (ack.ErrorCode != 0) != fail {
				t.Fatalf("save ACK: %+v", ack)
			}
			if len(repo.saveAtomicParams) != 2 || !slices.Equal(repo.saveAtomicParams[1].TowerDailyRewardIDs, []int64{7}) {
				t.Fatal("daily receipts missing from atomic save")
			}
			if (len(s.pendingTowerDailyClaims()) != 0) != fail {
				t.Fatal("failed save lost pending daily receipts")
			}
		})
	}
}
