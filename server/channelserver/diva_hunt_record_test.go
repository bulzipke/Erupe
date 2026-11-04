package channelserver

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

type divaHuntRecordLevelRepo struct {
	*divaBattleSongTestRepo
	colors []byte
}

func (r *divaHuntRecordLevelRepo) GetDivaWinningColors(uint32, time.Time, time.Time) ([]byte, error) {
	return append([]byte(nil), r.colors...), nil
}

func TestDivaHuntRecordLevelsMatchNativeSlotsAndCaps(t *testing.T) {
	if got := DivaBeadLevels([]int{17, 23, 18, 3}, []byte{1, 2, 2, 3, 4, 4, 4, 0}); !reflect.DeepEqual(got, map[int]int{17: 1, 23: 2, 18: 1, 3: 1}) {
		t.Fatalf("winning colors must map to slots, not effect IDs: %v", got)
	}
	for _, bead := range divaBeadCatalog {
		got := DivaBeadLevels([]int{bead.ID}, []byte{1, 1, 1, 1, 1, 1, 1, 1})
		if got[bead.ID] != bead.MaxLevel {
			t.Fatalf("effect %d level=%d cap=%d", bead.ID, got[bead.ID], bead.MaxLevel)
		}
		got = DivaBeadLevels([]int{bead.ID}, make([]byte, 8))
		if level, known := got[bead.ID]; !known || level != 0 {
			t.Fatalf("effect %d without wins must retain known zero: %v", bead.ID, got)
		}
	}
	for _, effects := range [][]int{nil, {17, 17}, {2}, {16}, {26}, {1, 3, 4, 8, 9}} {
		if got := DivaBeadLevels(effects, make([]byte, 8)); got != nil {
			t.Fatalf("invalid slots produced levels: %v => %v", effects, got)
		}
	}
	if DivaBeadLevels([]int{17}, nil) != nil || DivaBeadLevels([]int{17}, []byte{1}) != nil {
		t.Fatal("missing winning colors fabricated a level")
	}
	state := DivaBattleSongState{Effects: []DivaBattleSongEffect{{ID: 17}, {ID: 23}}}
	if got := divaBattleSongRecordBeads(5, state, map[int]int{17: 1}); got == nil || got.Levels != nil {
		t.Fatalf("partial level evidence should keep names only: %+v", got)
	}
}

func TestDivaHuntRecordRetainsDepartureBeads(t *testing.T) {
	s, repo := newDivaBattleSongSession()
	repo.state.Effects = []DivaBattleSongEffect{{ID: 17}, {ID: 23}, {ID: 18}, {ID: 3}}
	repo.beads = []int{17, 23, 18, 3}
	levelsRepo := &divaHuntRecordLevelRepo{divaBattleSongTestRepo: repo, colors: []byte{1, 2, 2, 3, 4, 4, 4, 0}}
	s.server.divaRepo = levelsRepo
	s.questWeaponGeneration = 1
	s.divaBattleRun = divaBattleSongRun{Generation: 1, StartedAt: TimeAdjusted()}
	s.captureDivaBattleSongDeparture(123, 1)
	// Returning to town and expiring or replacing the live activation must not
	// change the receipt used for the already-completed hunt.
	repo.state.StartedAt = uint32(TimeAdjusted().Add(-2 * time.Hour).Unix())
	repo.state.Effects[0].ID = 1
	levelsRepo.colors[0] = 2
	repo.beads[0] = 1
	s.stage = NewStage("sl1Ns200p0a0u1")
	got := s.divaBeadsForHuntRecord(123)
	if got == nil || got.Round != 42 || !reflect.DeepEqual(got.Beads, []string{"결집", "봉뢰", "가호", "단력"}) ||
		!reflect.DeepEqual(got.Levels, []int{1, 2, 1, 1}) {
		t.Fatalf("departure snapshot lost: %+v", got)
	}
	got.Beads[0] = "changed"
	got.Levels[0] = 3
	if snapshot := s.divaBeadsForHuntRecord(123); snapshot.Beads[0] != "결집" || snapshot.Levels[0] != 1 {
		t.Fatal("result processing mutated the departure snapshot")
	}
	if s.divaBeadsForHuntRecord(124) != nil {
		t.Fatal("another quest received old beads")
	}
	s.questWeaponGeneration++
	if s.divaBeadsForHuntRecord(123) != nil {
		t.Fatal("another departure received old beads")
	}
}

func TestDivaHuntRecordMetadataReachesPersonalBest(t *testing.T) {
	s, _ := newDivaBattleSongSession()
	records := &mockHuntRecordRepo{}
	s.server.huntRecordRepo = records
	s.server.guildRepo = &supportKillLogRepo{}
	s.stage = NewStage("sl1Ns200p0a0u1")
	s.questWeaponGeneration = 1
	s.divaBattleRun = divaBattleSongRun{Generation: 1, QuestID: 50001, Key: divaRunKeyOne,
		Beads: &HuntDivaBeads{Round: 5, Beads: []string{"결집", "봉뢰", "가호", "단력"}, Levels: []int{1, 2, 1, 1}}}
	handleMsgSysRecordLog(s, &mhfpacket.MsgSysRecordLog{AckHandle: 1, Data: supportRecordFixture(false, 6)})
	if len(records.records) != 1 || records.records[0].ClientSettings.DivaBeads == nil ||
		records.records[0].ClientSettings.DivaBeads.Round != 5 ||
		!reflect.DeepEqual(records.records[0].ClientSettings.DivaBeads.Levels, []int{1, 2, 1, 1}) {
		t.Fatalf("personal best lost server-captured Diva metadata: %+v", records.records)
	}
	raw, err := json.Marshal(records.records[0].ClientSettings)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		DivaBeads struct {
			Levels []int `json:"levels"`
		} `json:"divaBeads"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil || !reflect.DeepEqual(saved.DivaBeads.Levels, []int{1, 2, 1, 1}) {
		t.Fatalf("stored levels are not a JSON number array: %s (%v)", raw, err)
	}
}
