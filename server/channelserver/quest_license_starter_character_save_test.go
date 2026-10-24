package channelserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func TestQuestLicenseStarterCharacterSaveAtomicCommitsReceipt(t *testing.T) {
	starters, db, user, char, policy := setupStarterRepo(t)
	CreateTestUserBinary(t, db, char)
	queued, err := starters.Ensure(context.Background(), user, char, policy)
	if err != nil || queued.Status != StarterPending {
		t.Fatal("starter was not queued", queued, err)
	}
	if offers, err := starters.PrepareSave(context.Background(), user, char, []uint32{queued.RewardID}); err != nil || len(offers) != 1 {
		t.Fatal("starter was not prepared", offers, err)
	}
	assertStarterState(t, db, user, 1, 0, 0)

	// The repository treats this as an opaque client snapshot. This tests DB
	// atomicity, not that an actual native inventory contains item1859.
	hash := sha256.Sum256([]byte("decompressed-client-save"))
	params := SaveAtomicParams{
		CharID: char, Name: "StarterSaved", CompSave: []byte{1, 2, 3}, Hash: hash[:],
		HouseData: []byte{4, 5, 6}, BackupSlot: 1, BackupData: []byte{7, 8, 9},
		StarterUserID: user, StarterRewardIDs: []uint32{queued.RewardID},
	}
	characters := NewCharacterRepository(db)
	if err := characters.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal("actual character save failed", err)
	}
	assertStarterState(t, db, user, 0, 1, 0)
	var blob, savedHash, house, backup []byte
	var name string
	if err := db.QueryRow(`SELECT savedata,savedata_hash,name FROM characters WHERE id=$1`, char).Scan(&blob, &savedHash, &name); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&house, `SELECT house_data FROM user_binary WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&backup, `SELECT savedata FROM savedata_backups WHERE char_id=$1 AND slot=1`, char); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blob, params.CompSave) || !bytes.Equal(savedHash, params.Hash) || name != params.Name ||
		!bytes.Equal(house, params.HouseData) || !bytes.Equal(backup, params.BackupData) {
		t.Fatal("receipt committed without the full character-save transaction")
	}
	var claimed sql.NullTime
	if err := db.Get(&claimed, `SELECT claimed_at FROM quest_license_starters WHERE id=$1`, queued.RewardID); err != nil || !claimed.Valid {
		t.Fatal("missing committed starter timestamp", claimed, err)
	}
	if err := characters.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal("committed client save retry failed", err)
	}
	var again sql.NullTime
	if err := db.Get(&again, `SELECT claimed_at FROM quest_license_starters WHERE id=$1`, queued.RewardID); err != nil || !again.Valid || !again.Time.Equal(claimed.Time) {
		t.Fatal("retry replaced the original claim timestamp", again, err)
	}
	if offers, err := starters.PrepareSave(context.Background(), user, char, params.StarterRewardIDs); err != nil || len(offers) != 0 {
		t.Fatal("committed receipt offered another starter", offers, err)
	}
}

func TestQuestLicenseStarterCharacterSaveAtomicRollsBackReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, constraint, cleanup, errorPart string
	}{
		{
			name:       "character update failure",
			constraint: `ALTER TABLE characters ADD CONSTRAINT starter_character_save_failure CHECK(savedata<>decode('010203','hex'))`,
			cleanup:    `ALTER TABLE characters DROP CONSTRAINT IF EXISTS starter_character_save_failure`,
			errorPart:  "save character data",
		},
		{
			name:       "house failure after character update",
			constraint: `ALTER TABLE user_binary ADD CONSTRAINT starter_house_save_failure CHECK(house_data<>decode('040506','hex'))`,
			cleanup:    `ALTER TABLE user_binary DROP CONSTRAINT IF EXISTS starter_house_save_failure`,
			errorPart:  "save house data",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			starters, db, user, char, policy := setupStarterRepo(t)
			CreateTestUserBinary(t, db, char)
			queued, err := starters.Ensure(context.Background(), user, char, policy)
			if err != nil || queued.Status != StarterPending {
				t.Fatal(queued, err)
			}
			oldBlob, oldHash, oldHouse := []byte{9}, []byte{8}, []byte{7}
			db.MustExec(`UPDATE characters SET savedata=$1,savedata_hash=$2 WHERE id=$3`, oldBlob, oldHash, char)
			db.MustExec(`UPDATE user_binary SET house_data=$1 WHERE id=$2`, oldHouse, char)
			db.MustExec(tc.constraint)
			t.Cleanup(func() { db.MustExec(tc.cleanup) })
			params := SaveAtomicParams{
				CharID: char, Name: "Uncommitted", CompSave: []byte{1, 2, 3}, Hash: []byte{4},
				HouseData: []byte{4, 5, 6}, BackupSlot: 1, BackupData: []byte{5},
				StarterUserID: user, StarterRewardIDs: []uint32{queued.RewardID},
			}
			if err := NewCharacterRepository(db).SaveCharacterDataAtomic(params); err == nil || !strings.Contains(err.Error(), tc.errorPart) {
				t.Fatal("save did not fail at the injected write", err)
			}
			assertStarterState(t, db, user, 1, 0, 0)
			var blob, savedHash, house []byte
			var name string
			if err := db.QueryRow(`SELECT savedata,savedata_hash,name FROM characters WHERE id=$1`, char).Scan(&blob, &savedHash, &name); err != nil {
				t.Fatal(err)
			}
			if err := db.Get(&house, `SELECT house_data FROM user_binary WHERE id=$1`, char); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(blob, oldBlob) || !bytes.Equal(savedHash, oldHash) || !bytes.Equal(house, oldHouse) || name == params.Name {
				t.Fatal("failed transaction persisted part of the character save")
			}
			var claimed sql.NullTime
			if err := db.Get(&claimed, `SELECT claimed_at FROM quest_license_starters WHERE id=$1`, queued.RewardID); err != nil || claimed.Valid {
				t.Fatal("failed save consumed starter receipt", claimed, err)
			}
			var backups int
			if err := db.Get(&backups, `SELECT count(*) FROM savedata_backups WHERE char_id=$1`, char); err != nil || backups != 0 {
				t.Fatal("failed save persisted a backup", backups, err)
			}
			if offers, err := starters.PrepareSave(context.Background(), user, char, params.StarterRewardIDs); err != nil || len(offers) != 1 {
				t.Fatal("failed save lost the durable pending starter", offers, err)
			}
		})
	}
}

func newStarterCharacterSaveSession(t *testing.T) (*Session, *mockCharacterRepo) {
	t.Helper()
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := newMockCharacterRepo()
	repo.loadSaveDataID, repo.loadSaveDataName = 42, "Hunter"
	repo.strings["name"] = "Hunter"
	var err error
	repo.loadSaveDataData, err = nullcomp.Compress(characterSaveBlob(t, "Hunter"))
	if err != nil {
		t.Fatal(err)
	}
	server.charRepo = repo
	s := createMockSession(42, server)
	s.userID, s.Name = 1, "Hunter"
	return s, repo
}

func TestQuestLicenseStarterServerDerivedSavePreservesPending(t *testing.T) {
	s, repo := newStarterCharacterSaveSession(t)
	item := starterPendingTestItem(1)
	if err := s.questLicenseStarterPending.Stage(s.userID, s.charID, []CampaignReward{item}); err != nil {
		t.Fatal(err)
	}
	save, err := GetCharacterSaveData(s, s.charID)
	if err != nil {
		t.Fatal(err)
	}
	if err := save.Save(s); err != nil {
		t.Fatal("server-derived save failed", err)
	}
	if len(repo.saveAtomicParams) != 1 || len(repo.saveAtomicParams[0].StarterRewardIDs) != 0 || repo.saveAtomicParams[0].StarterUserID != 0 {
		t.Fatal("server-derived blob received client starter claims", repo.saveAtomicParams)
	}
	if ids, err := s.questLicenseStarterPending.SnapshotForClientSave(s.userID, s.charID); err != nil || !slices.Equal(ids, []uint32{item.ID}) {
		t.Fatal("server-derived save consumed pending starter", ids, err)
	}
}

func TestQuestLicenseStarterClientSaveCompletesOnlyAfterAtomicSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			s, repo := newStarterCharacterSaveSession(t)
			item := starterPendingTestItem(1)
			if err := s.questLicenseStarterPending.Stage(s.userID, s.charID, []CampaignReward{item}); err != nil {
				t.Fatal(err)
			}
			if fail {
				repo.saveAtomicErr = errors.New("atomic savedata failed")
			}
			handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 19, RawDataPayload: characterSaveBlob(t, "Hunter")})
			ack := readAck(t, s)
			if ack.AckHandle != 19 || (ack.ErrorCode != 0) != fail {
				t.Fatal("incorrect client-save acknowledgement", ack)
			}
			if len(repo.saveAtomicParams) != 1 || repo.saveAtomicParams[0].StarterUserID != s.userID ||
				!slices.Equal(repo.saveAtomicParams[0].StarterRewardIDs, []uint32{item.ID}) {
				t.Fatal("client snapshot did not reach authenticated atomic save", repo.saveAtomicParams)
			}
			if s.questLicenseStarterPending.HasPending() != fail {
				t.Fatal("session completion did not follow DB success")
			}
		})
	}
}

type starterCharacterSaveProbe struct {
	*mockCharacterRepo
	loadCalls int
}

func (r *starterCharacterSaveProbe) LoadSaveDataWithHash(char uint32) (uint32, []byte, bool, string, []byte, error) {
	r.loadCalls++
	return r.mockCharacterRepo.LoadSaveDataWithHash(char)
}

func TestQuestLicenseStarterRejectedClientSaveFailsBeforeRepository(t *testing.T) {
	for _, stageItem := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected without staged item", true: "rejected staged claim"}[stageItem], func(t *testing.T) {
			s, base := newStarterCharacterSaveSession(t)
			probe := &starterCharacterSaveProbe{mockCharacterRepo: base}
			s.server.charRepo = probe
			if stageItem {
				if err := s.questLicenseStarterPending.Stage(s.userID, s.charID, []CampaignReward{starterPendingTestItem(1)}); err != nil {
					t.Fatal(err)
				}
			}
			s.questLicenseStarterPending.Reject(s.userID, s.charID)
			// Invalid payload is intentional: the rejection must be checked before
			// loading/decompressing a blob or attempting any character write.
			handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 23, RawDataPayload: []byte{0xff}})
			ack := readAck(t, s)
			if ack.AckHandle != 23 || ack.ErrorCode == 0 {
				t.Fatal("rejected claim's queued save was acknowledged", ack)
			}
			if probe.loadCalls != 0 || len(base.saveAtomicParams) != 0 {
				t.Fatal("rejected claim's queued save reached character repository")
			}
			if !s.questLicenseStarterPending.HasPending() {
				t.Fatal("failed client save cleared rejection barrier")
			}
		})
	}
}
