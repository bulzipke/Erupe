package channelserver

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type guildMissionRepoFixture struct {
	repo     *GuildMissionRepository
	guildID  uint32
	leaderID uint32
	memberID uint32
}

func setupGuildMissionRepo(t *testing.T) guildMissionRepoFixture {
	t.Helper()
	db := SetupTestDB(t)
	leaderUserID := CreateTestUser(t, db, "mission_leader")
	memberUserID := CreateTestUser(t, db, "mission_member")
	leaderID := CreateTestCharacter(t, db, leaderUserID, "MissionLeader")
	memberID := CreateTestCharacter(t, db, memberUserID, "MissionMember")
	guildID := CreateTestGuild(t, db, leaderID, "MissionGuild")
	if _, err := db.Exec(`
		INSERT INTO guild_characters (guild_id, character_id, order_index)
		VALUES ($1, $2, 2)
	`, guildID, memberID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return guildMissionRepoFixture{
		repo:     NewGuildMissionRepository(db),
		guildID:  guildID,
		leaderID: leaderID,
		memberID: memberID,
	}
}

func TestGuildMissionRepositoryLifecycle(t *testing.T) {
	f := setupGuildMissionRepo(t)
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	def := guildMissionDefinitionsByID[431201]

	run, err := f.repo.Start(f.leaderID, def, now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.GuildID != f.guildID || run.Progress != 0 || run.State != GuildMissionRunActive {
		t.Fatalf("unexpected run: %+v", run)
	}
	if !run.TargetExpiresAt.Equal(now.Add(guildMissionTargetLifetime)) {
		t.Fatalf("target expiry = %v", run.TargetExpiresAt)
	}

	// A retransmitted SET for the same target is idempotent.
	retry, err := f.repo.Start(f.memberID, def, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("idempotent Start: %v", err)
	}
	if retry.ID != run.ID {
		t.Fatalf("idempotent Start created run %d, want %d", retry.ID, run.ID)
	}

	if _, err := f.repo.Start(f.memberID, guildMissionDefinitionsByID[431202], now); !errors.Is(err, ErrGuildMissionAlreadyActive) {
		t.Fatalf("second Start error = %v, want ErrGuildMissionAlreadyActive", err)
	}

	progress, err := f.repo.AddProgress(f.memberID, def.ID, 10, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("AddProgress: %v", err)
	}
	if progress.Applied != 10 || progress.Run.Progress != 10 || progress.Completed {
		t.Fatalf("unexpected partial progress: %+v", progress)
	}

	// A stale/oversized report is rejected in full so the client can roll back
	// everything it pre-consumed.
	if _, err = f.repo.AddProgress(f.leaderID, def.ID, ^uint32(0), now.Add(3*time.Minute)); !errors.Is(err, ErrGuildMissionTooMuchProgress) {
		t.Fatalf("oversized AddProgress error = %v, want ErrGuildMissionTooMuchProgress", err)
	}
	progress, err = f.repo.AddProgress(
		f.leaderID,
		def.ID,
		uint32(def.Quantity)-10,
		now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("completing AddProgress: %v", err)
	}
	if progress.Applied != uint32(def.Quantity)-10 || progress.Run.Progress != uint32(def.Quantity) || !progress.Completed {
		t.Fatalf("unexpected completion: %+v", progress)
	}
	if progress.Run.EffectExpiresAt == nil || !progress.Run.EffectExpiresAt.Equal(now.Add(3*time.Minute).Add(guildMissionEffectLifetime)) {
		t.Fatalf("effect expiry = %v", progress.Run.EffectExpiresAt)
	}

	snapshot, err := f.repo.GetSnapshot(f.memberID, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if snapshot.Active != nil {
		t.Fatalf("completed target still active: %+v", snapshot.Active)
	}
	if len(snapshot.Effects) != 1 || snapshot.Effects[0].ID != run.ID {
		t.Fatalf("unexpected effects: %+v", snapshot.Effects)
	}

	nextDef := guildMissionDefinitionsByID[431202]
	next, err := f.repo.Start(f.memberID, nextDef, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("Start after completion: %v", err)
	}
	if err := f.repo.Cancel(f.leaderID, nextDef.ID, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	var state GuildMissionRunState
	if err := f.repo.db.Get(&state, `SELECT state FROM guild_mission_runs WHERE id = $1`, next.ID); err != nil {
		t.Fatalf("read cancelled state: %v", err)
	}
	if state != GuildMissionRunCancelled {
		t.Fatalf("cancelled state = %q", state)
	}
}

func TestGuildMissionRepositoryExpiresAndRejectsApplicants(t *testing.T) {
	f := setupGuildMissionRepo(t)
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	def := guildMissionDefinitionsByID[431201]
	run, err := f.repo.Start(f.leaderID, def, now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	snapshot, err := f.repo.GetSnapshot(f.memberID, now.Add(guildMissionTargetLifetime))
	if err != nil {
		t.Fatalf("GetSnapshot at expiry: %v", err)
	}
	if snapshot.Active != nil {
		t.Fatalf("expired target still active: %+v", snapshot.Active)
	}
	var state GuildMissionRunState
	if err := f.repo.db.Get(&state, `SELECT state FROM guild_mission_runs WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("read expired state: %v", err)
	}
	if state != GuildMissionRunExpired {
		t.Fatalf("expired state = %q", state)
	}

	applicantUserID := CreateTestUser(t, f.repo.db, "mission_applicant")
	applicantID := CreateTestCharacter(t, f.repo.db, applicantUserID, "MissionApply")
	if _, err := f.repo.db.Exec(`
		INSERT INTO guild_applications (guild_id, character_id, actor_id, application_type)
		VALUES ($1, $2, $2, 'applied')
	`, f.guildID, applicantID); err != nil {
		t.Fatalf("insert application: %v", err)
	}
	if _, err := f.repo.Start(applicantID, def, now); !errors.Is(err, ErrGuildMissionNotMember) {
		t.Fatalf("applicant Start error = %v, want ErrGuildMissionNotMember", err)
	}
}

func TestGuildMissionRepositoryConcurrentCompletionAppliesOnce(t *testing.T) {
	f := setupGuildMissionRepo(t)
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	def := guildMissionDefinitionsByID[431201]
	if _, err := f.repo.Start(f.leaderID, def, now); err != nil {
		t.Fatalf("Start: %v", err)
	}

	type result struct {
		value GuildMissionProgressResult
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, charID := range []uint32{f.leaderID, f.memberID} {
		wg.Add(1)
		go func(id uint32) {
			defer wg.Done()
			value, err := f.repo.AddProgress(id, def.ID, uint32(def.Quantity), now.Add(time.Minute))
			results <- result{value: value, err: err}
		}(charID)
	}
	wg.Wait()
	close(results)

	var applied uint32
	var completed int
	for result := range results {
		if result.err != nil && !errors.Is(result.err, ErrGuildMissionNoActiveTarget) {
			t.Fatalf("concurrent AddProgress: %v", result.err)
		}
		if result.err == nil {
			applied += result.value.Applied
			if result.value.Completed {
				completed++
			}
		}
	}
	if applied != uint32(def.Quantity) {
		t.Fatalf("total applied = %d, want %d", applied, def.Quantity)
	}
	if completed != 1 {
		t.Fatalf("completion count = %d, want 1", completed)
	}

	var progress uint32
	var state GuildMissionRunState
	if err := f.repo.db.QueryRow(`
		SELECT progress, state
		FROM guild_mission_runs
		WHERE guild_id = $1
		ORDER BY id DESC
		LIMIT 1
	`, f.guildID).Scan(&progress, &state); err != nil {
		t.Fatalf("read final run: %v", err)
	}
	if progress != uint32(def.Quantity) || state != GuildMissionRunCompleted {
		t.Fatalf("final progress/state = %d/%q", progress, state)
	}
}
