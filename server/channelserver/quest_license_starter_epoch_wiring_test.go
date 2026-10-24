package channelserver

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"erupe-ce/network/mhfpacket"
)

func TestQuestLicenseStarterEpochAtomicPublicSaveRejectsOldEmptyReceipts(t *testing.T) {
	starters, db, user, char, policy := setupStarterRepo(t)
	CreateTestUserBinary(t, db, char)
	queued, err := starters.Ensure(context.Background(), user, char, policy)
	if err != nil {
		t.Fatal(err)
	}
	characters := NewCharacterRepository(db)
	before, err := characters.LoadSaveDataWithStarterEpoch(context.Background(), user, char)
	if err != nil {
		t.Fatal(err)
	}
	baseline := QuestLicenseStarterEpochBaseline{UserID: user, Epoch: before.Epoch}
	params := SaveAtomicParams{CharID: char, Name: "EpochSaved", CompSave: []byte("with-starter"),
		HouseData: []byte("house"), StarterUserID: user, StarterRewardIDs: []uint32{queued.RewardID},
		BackupSlot: 1, BackupData: []byte("before-starter"), BackupStarterEpoch: &before.Epoch}
	next, err := characters.SaveCharacterDataAtomicWithStarterEpoch(params, baseline)
	if err != nil || next != 1 {
		t.Fatal("public atomic save did not return its committed generation", next, err)
	}
	assertStarterState(t, db, user, 0, 1, 0)
	for _, source := range []string{"old-client-empty-receipts", "old-server-derived-blob"} {
		stale := SaveAtomicParams{CharID: char, Name: "OldSnapshot", CompSave: []byte(source), HouseData: []byte("old-house")}
		if got, err := characters.SaveCharacterDataAtomicWithStarterEpoch(stale, baseline); got != 0 || !errors.Is(err, ErrQuestLicenseStarterStaleSave) {
			t.Fatal("old empty-receipt snapshot borrowed the current generation", source, got, err)
		}
	}
	current, err := characters.LoadSaveDataWithStarterEpoch(context.Background(), user, char)
	if err != nil || current.Epoch != next || !bytes.Equal(current.Savedata, params.CompSave) {
		t.Fatal("rejected public save changed inventory", current, err)
	}
	var backupEpoch sql.NullInt64
	if err := db.Get(&backupEpoch, `SELECT starter_inventory_epoch FROM savedata_backups WHERE char_id=$1 AND slot=1`, char); err != nil || !backupEpoch.Valid || backupEpoch.Int64 != before.Epoch {
		t.Fatal("backup was labeled with the post-claim epoch", backupEpoch, err)
	}
	currentParams := params
	currentParams.StarterRewardIDs, currentParams.StarterUserID = nil, 0
	currentParams.BackupData, currentParams.BackupStarterEpoch = nil, nil
	if got, err := characters.SaveCharacterDataAtomicWithStarterEpoch(currentParams, QuestLicenseStarterEpochBaseline{UserID: user, Epoch: next}); err != nil || got != next {
		t.Fatal("current empty-receipt save advanced epoch", got, err)
	}
}

func TestQuestLicenseStarterEpochAtomicPublicSaveRollsBackAllLaterFailures(t *testing.T) {
	for _, failure := range []string{"house", "backup", "diva"} {
		t.Run(failure, func(t *testing.T) {
			starters, db, user, char, policy := setupStarterRepo(t)
			CreateTestUserBinary(t, db, char)
			queued, err := starters.Ensure(context.Background(), user, char, policy)
			if err != nil {
				t.Fatal(err)
			}
			oldBlob, oldHouse := []byte("old-inventory"), []byte("old-house")
			db.MustExec(`UPDATE characters SET savedata=$1 WHERE id=$2`, oldBlob, char)
			db.MustExec(`UPDATE user_binary SET house_data=$1 WHERE id=$2`, oldHouse, char)
			epoch := int64(0)
			params := SaveAtomicParams{CharID: char, Name: "NotCommitted", CompSave: []byte("new-inventory"), HouseData: []byte("new-house"),
				StarterUserID: user, StarterRewardIDs: []uint32{queued.RewardID}, BackupSlot: 1, BackupData: oldBlob, BackupStarterEpoch: &epoch}
			switch failure {
			case "house":
				db.MustExec(`ALTER TABLE user_binary ADD CONSTRAINT starter_epoch_house_failure CHECK(house_data<>decode('6e65772d686f757365','hex'))`)
				t.Cleanup(func() { db.MustExec(`ALTER TABLE user_binary DROP CONSTRAINT IF EXISTS starter_epoch_house_failure`) })
			case "backup":
				params.BackupSlot = 99 // The real backup table permits only slots 0..2.
			case "diva":
				params.DivaRewardIDs = []uint32{0} // Validated after the backup write.
			}
			characters := NewCharacterRepository(db)
			next, err := characters.SaveCharacterDataAtomicWithStarterEpoch(params, QuestLicenseStarterEpochBaseline{UserID: user, Epoch: epoch})
			if next != 0 || err == nil || !strings.Contains(err.Error(), "save "+failure) {
				t.Fatal("later failure returned a committed generation", next, err)
			}
			assertStarterState(t, db, user, 1, 0, 0)
			snapshot, err := characters.LoadSaveDataWithStarterEpoch(context.Background(), user, char)
			if err != nil || snapshot.Epoch != epoch || !bytes.Equal(snapshot.Savedata, oldBlob) {
				t.Fatal("failed public transaction persisted inventory or epoch", snapshot, err)
			}
			var house []byte
			var backups int
			if err := db.Get(&house, `SELECT house_data FROM user_binary WHERE id=$1`, char); err != nil || !bytes.Equal(house, oldHouse) {
				t.Fatal("failed public save persisted house", house, err)
			}
			if err := db.Get(&backups, `SELECT count(*) FROM savedata_backups WHERE char_id=$1`, char); err != nil || backups != 0 {
				t.Fatal("failed public save persisted backup", backups, err)
			}
		})
	}
}

func TestQuestLicenseStarterEpochFeatureOffBackupClearsKnownMetadata(t *testing.T) {
	_, db, _, char, _ := setupStarterRepo(t)
	CreateTestUserBinary(t, db, char)
	db.MustExec(`UPDATE characters SET starter_inventory_epoch=3 WHERE id=$1`, char)
	db.MustExec(`INSERT INTO savedata_backups(char_id,slot,savedata,starter_inventory_epoch) VALUES($1,1,$2,3)`, char, []byte("known-old-backup"))
	// Supplying a value to a feature-off legacy call must not label the new
	// backup with it or preserve metadata from the previously occupied slot.
	forged := int64(3)
	params := SaveAtomicParams{CharID: char, Name: "FeatureOff", CompSave: []byte("off-save"), HouseData: []byte("house"),
		BackupSlot: 1, BackupData: []byte("off-backup"), BackupStarterEpoch: &forged}
	if err := NewCharacterRepository(db).SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	var epoch sql.NullInt64
	var blob []byte
	if err := db.QueryRow(`SELECT starter_inventory_epoch,savedata FROM savedata_backups WHERE char_id=$1 AND slot=1`, char).Scan(&epoch, &blob); err != nil || epoch.Valid || !bytes.Equal(blob, params.BackupData) {
		t.Fatal("legacy backup kept an unverified generation", epoch, blob, err)
	}
}

type starterEpochWiringRepo struct {
	*mockCharacterRepo
	dbEpoch     int64
	loadCalls   int
	baselines   []QuestLicenseStarterEpochBaseline
	guardedSave []SaveAtomicParams
	commitErr   error
}

func (r *starterEpochWiringRepo) LoadSaveDataWithStarterEpoch(_ context.Context, user, char uint32) (QuestLicenseStarterEpochSnapshot, error) {
	r.loadCalls++
	if user != 1 || char != 42 {
		return QuestLicenseStarterEpochSnapshot{}, sql.ErrNoRows
	}
	return QuestLicenseStarterEpochSnapshot{ID: char, Savedata: append([]byte(nil), r.loadSaveDataData...),
		IsNew: r.loadSaveDataNew, Name: r.loadSaveDataName, Hash: append([]byte(nil), r.loadSaveDataHash...), Epoch: r.dbEpoch}, r.loadSaveDataErr
}

func (r *starterEpochWiringRepo) LoadBackupsWithStarterEpoch(_ context.Context, user, char uint32, expected int64) ([]SavedataBackup, error) {
	if user != 1 || char != 42 || expected != r.dbEpoch {
		return nil, ErrQuestLicenseStarterStaleSave
	}
	return r.loadBackups, r.loadBackupsErr
}

func (r *starterEpochWiringRepo) SaveCharacterDataAtomicWithStarterEpoch(params SaveAtomicParams, baseline QuestLicenseStarterEpochBaseline) (int64, error) {
	r.baselines = append(r.baselines, baseline)
	r.guardedSave = append(r.guardedSave, params)
	if baseline.UserID != 1 || params.CharID != 42 || baseline.Epoch != r.dbEpoch {
		return 0, ErrQuestLicenseStarterStaleSave
	}
	if r.commitErr != nil {
		return 0, r.commitErr
	}
	if len(params.StarterRewardIDs) > 0 {
		r.dbEpoch++
	}
	r.loadSaveDataData = append([]byte(nil), params.CompSave...)
	r.loadSaveDataHash = append([]byte(nil), params.Hash...)
	return r.dbEpoch, nil
}

func newStarterEpochWiringSession(t *testing.T) (*Session, *starterEpochWiringRepo) {
	t.Helper()
	s, base := newStarterCharacterSaveSession(t)
	s.server.erupeConfig.QuestLicenses.Enabled = true
	s.server.erupeConfig.BinPath = t.TempDir()
	s.server.userBinary = NewUserBinaryStore()
	repo := &starterEpochWiringRepo{mockCharacterRepo: base}
	s.server.charRepo = repo
	return s, repo
}

func loadStarterEpochWiringClient(t *testing.T, s *Session) {
	t.Helper()
	handleMsgMhfLoaddata(s, &mhfpacket.MsgMhfLoaddata{AckHandle: 51})
	ack := readAck(t, s)
	if ack.AckHandle != 51 || ack.ErrorCode != 0 || !ack.IsBufferResponse {
		t.Fatal("client did not receive its inventory load", ack)
	}
	if baseline, err := s.clientStarterEpochBaseline(); err != nil || baseline.Epoch != 0 || baseline.UserID != s.userID {
		t.Fatal("LOADDATA did not publish its exact inventory baseline", baseline, err)
	}
}

func TestQuestLicenseStarterEpochWiringClientSaveCannotBorrowNewDatabaseEpoch(t *testing.T) {
	s, repo := newStarterEpochWiringSession(t)
	loadStarterEpochWiringClient(t, s)
	// Another connection commits a starter while this client still holds epoch0.
	repo.dbEpoch = 1
	handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 52, RawDataPayload: characterSaveBlob(t, "Hunter")})
	ack := readAck(t, s)
	if ack.AckHandle != 52 || ack.ErrorCode == 0 || len(repo.baselines) != 1 || repo.baselines[0].Epoch != 0 {
		t.Fatal("client save borrowed the freshly read DB generation", ack, repo.baselines)
	}
	if baseline, err := s.clientStarterEpochBaseline(); err != nil || baseline.Epoch != 0 {
		t.Fatal("failed save advanced client baseline", baseline, err)
	}
	if len(repo.saveAtomicParams) != 0 {
		t.Fatal("guarded client save fell through to legacy writer")
	}
}

func TestQuestLicenseStarterEpochWiringClientBaselineChangesOnlyAfterCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "commit failure"}[fail], func(t *testing.T) {
			s, repo := newStarterEpochWiringSession(t)
			loadStarterEpochWiringClient(t, s)
			item := starterPendingTestItem(1)
			if err := s.questLicenseStarterPending.Stage(s.userID, s.charID, []CampaignReward{item}); err != nil {
				t.Fatal(err)
			}
			if fail {
				repo.commitErr = errors.New("injected COMMIT failure")
			}
			handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 53, RawDataPayload: characterSaveBlob(t, "Hunter")})
			ack := readAck(t, s)
			if ack.AckHandle != 53 || (ack.ErrorCode != 0) != fail || len(repo.baselines) != 1 || repo.baselines[0].Epoch != 0 {
				t.Fatal("client did not use its loaded baseline", ack, repo.baselines)
			}
			want := int64(1)
			if fail {
				want = 0
			}
			if baseline, err := s.clientStarterEpochBaseline(); err != nil || baseline.Epoch != want || repo.dbEpoch != want {
				t.Fatal("baseline publication did not follow COMMIT", baseline, repo.dbEpoch, err)
			}
			if s.questLicenseStarterPending.HasPending() != fail {
				t.Fatal("receipt completion did not follow COMMIT")
			}
		})
	}
}

func TestQuestLicenseStarterEpochWiringOldServerBlobCannotBorrowClientBaseline(t *testing.T) {
	s, repo := newStarterEpochWiringSession(t)
	loadStarterEpochWiringClient(t, s)
	old, err := GetCharacterSaveData(s, s.charID)
	if err != nil {
		t.Fatal(err)
	}
	repo.dbEpoch = 1
	// Even a newer client baseline must not bless this DB-derived epoch0 blob.
	s.questLicenseClientInventory.Store(&questLicenseClientInventory{characterID: s.charID,
		baseline: QuestLicenseStarterEpochBaseline{UserID: s.userID, Epoch: 1}})
	if err := old.Save(s); !errors.Is(err, ErrQuestLicenseStarterStaleSave) || len(repo.baselines) != 1 || repo.baselines[0].Epoch != 0 {
		t.Fatal("server-derived save borrowed the client baseline", err, repo.baselines)
	}
	if baseline, err := s.clientStarterEpochBaseline(); err != nil || baseline.Epoch != 1 {
		t.Fatal("server-derived failure changed client baseline", baseline, err)
	}
}

func TestQuestLicenseStarterEpochWiringUnloadedClientFailsBeforeSnapshotRead(t *testing.T) {
	s, repo := newStarterEpochWiringSession(t)
	handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 54, RawDataPayload: characterSaveBlob(t, "Hunter")})
	ack := readAck(t, s)
	if ack.AckHandle != 54 || ack.ErrorCode == 0 || repo.loadCalls != 0 || len(repo.baselines) != 0 {
		t.Fatal("client invented an inventory baseline before LOADDATA", ack, repo.loadCalls, repo.baselines)
	}
}

func TestQuestLicenseStarterEpochWiringUnresolvedClaimCannotReloadBaseline(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending item", true: "rejected claim"}[rejected], func(t *testing.T) {
			s, repo := newStarterEpochWiringSession(t)
			loadStarterEpochWiringClient(t, s)
			if rejected {
				s.questLicenseStarterPending.Reject(s.userID, s.charID)
			} else if err := s.questLicenseStarterPending.Stage(s.userID, s.charID, []CampaignReward{starterPendingTestItem(1)}); err != nil {
				t.Fatal(err)
			}
			repo.dbEpoch = 1
			handleMsgMhfLoaddata(s, &mhfpacket.MsgMhfLoaddata{AckHandle: 55})
			ack := readAck(t, s)
			if ack.AckHandle != 55 || ack.ErrorCode == 0 || !ack.IsBufferResponse || repo.loadCalls != 1 {
				t.Fatal("unresolved claim reloaded inventory", ack, repo.loadCalls)
			}
			if baseline, err := s.clientStarterEpochBaseline(); err != nil || baseline.Epoch != 0 || !s.questLicenseStarterPending.HasPending() {
				t.Fatal("failed reload replaced baseline or cleared claim", baseline, err)
			}
		})
	}
}
