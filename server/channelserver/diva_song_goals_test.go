package channelserver

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
	"github.com/jmoiron/sqlx"
)

func TestDivaSongTargets(t *testing.T) {
	for _, tt := range []struct {
		name                            string
		accounts, points, first, twenty int64
		last                            int64
	}{
		{"empty", 0, 0, 1000, 20000, 25000},
		{"just below minimum spacing", 1, 19999, 1000, 20000, 25000},
		{"low activity", 5, 5000, 3000, 60000, 75000},
		{"previous production round", 5, 104882, 5000, 104000, 131000},
		{"latest production round", 5, 158050, 7000, 158000, 197000},
		{"high outlier", 5, 1000000, 8000, 160000, 200000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targets := divaSongTargets(tt.accounts, tt.points)
			if len(targets) != 25 || targets[0] != tt.first ||
				targets[19] != tt.twenty || targets[24] != tt.last {
				t.Fatalf("unexpected targets: %v", targets)
			}
			for i, threshold := range targets {
				if threshold%1000 != 0 || divaBattleSongEarned(threshold-1, targets) != uint8(i) ||
					divaBattleSongEarned(threshold, targets) != uint8(i+1) {
					t.Fatalf("target %d is not a distinct multiple of 1,000: %d", i+1, threshold)
				}
			}
		})
	}
}

func insertDivaGoalTestRecord(t *testing.T, db *sqlx.DB, character uint32, event DivaEvent, at time.Time, color, points, premium int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, character, event.ID, divaNoon(at), color, points, premium, at); err != nil {
		t.Fatal(err)
	}
}

type divaSongGoalTestRepo struct {
	*DivaRepository
	event DivaEvent
}

func (r *divaSongGoalTestRepo) EnsureDivaEvent(time.Time, int) (DivaEvent, error) {
	return r.event, nil
}

func TestRepoDivaSongGoalsRoundSnapshotAndActivation(t *testing.T) {
	repo, db := setupDivaRepo(t)
	start := rotationMondayNoon(divaTestTime(21, 12, 0))
	previous, err := repo.EnsureDivaEvent(start, -1)
	if err != nil {
		t.Fatal(err)
	}
	user := CreateTestUser(t, db, "song_goals")
	first := CreateTestCharacter(t, db, user, "SongGoalMain")
	alt := CreateTestCharacter(t, db, user, "SongGoalAlt")
	other := CreateTestCharacter(t, db, CreateTestUser(t, db, "song_goals_other"), "SongGoalOther")
	prayer := start.Add(time.Hour)
	if err := repo.RecordDivaPoints(first, previous.ID, 12000, 0, prayer); !errors.Is(err, errDivaBeadUnselected) {
		t.Fatalf("points without a selected bead: %v", err)
	}
	insertDivaGoalTestRecord(t, db, first, previous, prayer, 1, 12000, 100000)
	insertDivaGoalTestRecord(t, db, alt, previous, prayer, 2, 6000, 0)
	insertDivaGoalTestRecord(t, db, other, previous, prayer, 4, 13001, 0)
	insertDivaGoalTestRecord(t, db, other, previous, prayer, 0, 200000, 0)
	insertDivaGoalTestRecord(t, db, other, previous, previous.phaseTime(1), 4, 200000, 0)
	debug, err := repo.EnsureDivaEvent(previous.phaseTime(5).Add(-time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	insertDivaGoalTestRecord(t, db, other, debug, debug.phaseTime(0), 2, 200000, 0)

	event, err := repo.EnsureDivaEvent(previous.phaseTime(5), -1)
	if err != nil || event.ID == previous.ID {
		t.Fatalf("rollover: %+v %v", event, err)
	}
	var basis struct {
		Previous uint32 `db:"previous_event_id"`
		Accounts int64  `db:"previous_accounts"`
		Points   int64  `db:"previous_points"`
	}
	if err = db.Get(&basis, `SELECT previous_event_id,previous_accounts,previous_points
		FROM diva_song_goal_rounds WHERE event_id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if basis.Previous != previous.ID || basis.Accounts != 2 || basis.Points != 31001 {
		t.Fatalf("wrong previous round basis: %+v", basis)
	}
	empty, err := repo.GetDivaSongProgress(event)
	if err != nil || empty.Total != 0 || !slices.Equal(empty.Targets, divaSongTargets(2, 31001)) {
		t.Fatalf("new round progress: %+v %v", empty, err)
	}

	battle := event.phaseTime(2)
	if _, err := repo.EnsureDivaEvent(battle, -1); err != nil {
		t.Fatal(err)
	}
	prayer = event.phaseTime(0).Add(time.Hour)
	insertDivaGoalTestRecord(t, db, first, previous, start.Add(time.Hour), 1, 90000, 0)
	insertDivaGoalTestRecord(t, db, first, event, prayer, 4, 999, 8000)
	insertDivaGoalTestRecord(t, db, other, event, prayer, 0, 8000, 0)
	insertDivaGoalTestRecord(t, db, other, event, battle, 3, 8000, 0)
	if _, err := repo.useDivaBattleSongAt(first, event.ID, battle); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatalf("allowed a song below its first threshold: %v", err)
	}
	insertDivaGoalTestRecord(t, db, first, event, prayer, 4, 1, 0)
	progress, err := NewDivaRepository(db).GetDivaSongProgress(event)
	if err != nil || progress.Total != 1000 || !slices.Equal(progress.Targets, empty.Targets) {
		t.Fatalf("battle progress: %+v %v", progress, err)
	}
	srv := createMockServer()
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	srv.divaRepo = &divaSongGoalTestRepo{DivaRepository: repo, event: event}
	session := createMockSession(1, srv)
	handleMsgMhfGetUdTotalPointInfo(session, &mhfpacket.MsgMhfGetUdTotalPointInfo{AckHandle: 1})
	ack := readAck(t, session)
	payload := ack.Payload
	if ack.ErrorCode != 0 || len(payload) != 585 || binary.BigEndian.Uint64(payload[577:]) != 1000 {
		t.Fatalf("wrong progress packet: %x", payload)
	}
	for i, target := range progress.Targets {
		if binary.BigEndian.Uint64(payload[1+i*8:]) != uint64(target) || payload[513+i] != 0 {
			t.Fatalf("wrong song goal in slot %d", i)
		}
	}
	for i, target := range udShopUnlockMilestones {
		slot := len(progress.Targets) + i
		if binary.BigEndian.Uint64(payload[1+slot*8:]) != target || payload[513+slot] != byte(i+1) {
			t.Fatalf("wrong shop goal in slot %d", slot)
		}
	}
	if state, err := repo.useDivaBattleSongAt(first, event.ID, battle); err != nil || state.Used != 1 {
		t.Fatalf("displayed first goal did not grant a use: %+v %v", state, err)
	}
	if _, err := repo.useDivaBattleSongAt(first, event.ID, battle.Add(time.Hour)); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatalf("exhausted use allowed: %v", err)
	}
	if _, err := repo.useDivaBattleSongAt(other, event.ID, battle); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatalf("unqualified contributions granted a use: %v", err)
	}
}

func TestRepoDivaSongGoalsAdoptActiveBattleWithNoPriorUsage(t *testing.T) {
	for _, alreadyUsed := range []bool{false, true} {
		name := "unused"
		if alreadyUsed {
			name = "used"
		}
		t.Run(name, func(t *testing.T) {
			repo, db := setupDivaRepo(t)
			start := rotationMondayNoon(divaTestTime(21, 12, 0))
			event := DivaEvent{StartTime: uint32(start.Unix()), Continuous: true}
			if err := db.QueryRow(`INSERT INTO events(event_type,start_time,diva_continuous)
				VALUES('diva',$1,true) RETURNING id`, start).Scan(&event.ID); err != nil {
				t.Fatal(err)
			}
			if alreadyUsed {
				user := CreateTestUser(t, db, "legacy_used")
				char := CreateTestCharacter(t, db, user, "LegacyUsed")
				if _, err := db.Exec(`INSERT INTO diva_battle_songs(char_id,event_id,used_count,activation_id,activated_at)
					VALUES($1,$2,1,nextval('diva_battle_song_activation_ids'),$3)`, char, event.ID, event.phaseTime(2)); err != nil {
					t.Fatal(err)
				}
			}
			if actual, err := repo.EnsureDivaEvent(event.phaseTime(2), -1); err != nil || actual != event {
				t.Fatalf("active round replaced: %+v %v", actual, err)
			}
			progress, err := repo.GetDivaSongProgress(event)
			want := divaSongTargets(0, 0)
			if alreadyUsed {
				want = udMilestones
			}
			if err != nil || !slices.Equal(progress.Targets, want) {
				t.Fatalf("wrong upgrade targets: %+v %v", progress, err)
			}
		})
	}
}

func TestHandleMsgMhfAddUdPointOutsidePrayer(t *testing.T) {
	srv := createMockServer()
	event := DivaEvent{ID: 1, StartTime: uint32(TimeAdjusted().Unix() - secsPerWeek), Continuous: true}
	repo := &mockDivaRepo{events: []DivaEvent{event}}
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	s.charID = 100
	handleMsgMhfAddUdPoint(s, &mhfpacket.MsgMhfAddUdPoint{AckHandle: 1, QuestPoints: 99})
	if ack := readAck(t, s); ack.ErrorCode == 0 || len(repo.points) != 0 {
		t.Fatalf("battle-week points accepted: %+v", ack)
	}
}

func TestRepoDivaBeadAtMaxLevelClosesAtNoon(t *testing.T) {
	repo, db := setupDivaRepo(t)
	start := rotationMondayNoon(divaTestTime(21, 12, 0))
	event, err := repo.EnsureDivaEvent(start, -1)
	if err != nil {
		t.Fatal(err)
	}
	user := CreateTestUser(t, db, "diva_cap")
	char := CreateTestCharacter(t, db, user, "DivaCapped")
	if err := repo.AssignBead(char, event.ID, 1, start); err != nil {
		t.Fatal(err)
	}
	// Red wins the first three days. On day three it still accepts points
	// right up to the noon boundary that publishes its third win.
	for _, at := range []time.Time{start, start.Add(24 * time.Hour), start.Add(72*time.Hour - time.Second)} {
		if err := repo.RecordDivaPoints(char, event.ID, 10, 0, at); err != nil {
			t.Fatal(err)
		}
	}
	fourthDay := start.Add(72 * time.Hour)
	if err := repo.AssignBead(char, event.ID, 1, fourthDay); !errors.Is(err, errDivaBeadLocked) {
		t.Fatalf("red could be selected after reaching level 3: %v", err)
	}
	if err := repo.RecordDivaPoints(char, event.ID, 500, 0, fourthDay); !errors.Is(err, errDivaBeadLocked) {
		t.Fatalf("stale red choice still received points: %v", err)
	}
	if err := repo.AssignBead(char, event.ID, 4, fourthDay); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordDivaPoints(char, event.ID, 20, 0, fourthDay); err != nil {
		t.Fatal(err)
	}
	colors, err := repo.GetDivaWinningColors(event.ID, start, fourthDay.Add(24*time.Hour))
	if err != nil || !slices.Equal(colors, []byte{1, 1, 1, 4, 0, 0, 0, 0}) {
		t.Fatalf("incorrect winners after red caps: %v %v", colors, err)
	}
	progress, err := repo.GetDivaSongProgress(event)
	if err != nil || progress.Total != 50 {
		t.Fatalf("capped contribution leaked into total: %+v %v", progress, err)
	}
}
