package channelserver

import (
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "erupe-ce/config"
)

type returnWorldUserMock struct {
	UserRepo
	allowed bool
	err     error
}

func (r returnWorldUserMock) ReturnWorldEligible(uint32, time.Time) (bool, error) {
	return r.allowed, r.err
}

func TestReturnWorldQuestDelivery(t *testing.T) {
	for _, world := range []uint8{0, 1, 2, 3, 4, 5, 6} {
		for _, eligible := range []bool{false, true} {
			t.Run(fmt.Sprintf("world%d/eligible%t", world, eligible), func(t *testing.T) {
				s := createMockSession(1, createMockServer())
				s.server.worldType = world
				s.server.erupeConfig.RealClientMode = cfg.ZZ
				s.server.userRepo = returnWorldUserMock{allowed: eligible}
				ordinary := EventQuest{ID: 90, QuestID: 50000, QuestType: 18, Flags: 9}
				override := EventQuest{ID: 99, QuestID: 23352, QuestType: 28, MaxPlayers: 2, Mark: 1, Flags: 17}
				input := []EventQuest{ordinary, override}
				got := s.withReturnWorldQuests(input)
				want := 1
				if world == 5 && eligible {
					want += 17
				}
				if len(got) != want || input[0] != ordinary || input[1] != override {
					t.Fatalf("list count=%d want=%d input=%v", len(got), want, input)
				}
				seen := map[int]bool{}
				for _, quest := range got {
					if seen[quest.QuestID] {
						t.Fatalf("duplicate quest %d", quest.QuestID)
					}
					seen[quest.QuestID] = true
					if quest.QuestID == 50000 && quest != ordinary || quest.QuestID == 23352 && quest != override {
						t.Fatalf("DB row changed: %+v", quest)
					}
					if quest.QuestID != 50000 && quest.QuestID != 23352 && (quest.ID <= 99 || quest.QuestType != 28 || quest.MaxPlayers != 4 || quest.Flags != -1) {
						t.Fatalf("invalid synthetic row: %+v", quest)
					}
				}
			})
		}
	}
}

func TestReturnWorldQuestFailClosed(t *testing.T) {
	s := createMockSession(1, createMockServer())
	s.server.worldType = 5
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.userRepo = returnWorldUserMock{allowed: true, err: errors.New("lookup failed")}
	if got := s.withReturnWorldQuests([]EventQuest{{QuestID: 23352}, {QuestID: 50000}}); len(got) != 1 || got[0].QuestID != 50000 {
		t.Fatalf("lookup failure exposed Return quest: %v", got)
	}
	s.server.userRepo = returnWorldUserMock{allowed: true}
	s.server.erupeConfig.RealClientMode = cfg.G9
	if got := s.withReturnWorldQuests(nil); len(got) != 0 {
		t.Fatalf("legacy mode received final catalog: %v", got)
	}
}

func TestRepoReturnWorldEligible(t *testing.T) {
	repo, db, uid := setupUserRepo(t)
	now := TimeAdjusted().Truncate(time.Microsecond)
	for _, expiry := range []any{nil, now.Add(-time.Second), now, now.Add(time.Second)} {
		if _, err := db.Exec("UPDATE users SET return_expires=$1 WHERE id=$2", expiry, uid); err != nil {
			t.Fatal(err)
		}
		got, err := repo.ReturnWorldEligible(uid, now)
		want := expiry != nil && expiry.(time.Time).After(now)
		if err != nil || got != want {
			t.Fatalf("expiry=%v allowed=%t want=%t err=%v", expiry, got, want, err)
		}
	}
	if _, err := repo.ReturnWorldEligible(uid+999999, now); err == nil {
		t.Fatal("unknown account did not fail closed")
	}
}
