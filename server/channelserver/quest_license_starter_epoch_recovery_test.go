package channelserver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func TestQuestLicenseStarterEpochRecoveryDoesNotRestorePreGiftInventory(t *testing.T) {
	_, db, user, char, _ := setupStarterRepo(t)
	CreateTestUserBinary(t, db, char)
	compress := func(marker byte) []byte {
		t.Helper()
		blob := characterSaveBlob(t, "Hunter")
		blob[len(blob)-1] = marker
		data, err := nullcomp.Compress(blob)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	current, old := compress(1), compress(2)
	db.MustExec(`UPDATE characters SET name='Hunter',savedata=$1,savedata_hash=$2,starter_inventory_epoch=1 WHERE id=$3`, compress(3), []byte("wrong-checksum"), char)
	db.MustExec(`INSERT INTO savedata_backups(char_id,slot,savedata,saved_at,starter_inventory_epoch) VALUES
		($1,0,$2,now()+interval '2 hours',NULL),($1,1,$2,now()+interval '1 hour',0),($1,2,$3,now(),1)`, char, old, current)
	srv := createMockServer()
	srv.charRepo = NewCharacterRepository(db)
	srv.erupeConfig.QuestLicenses.Enabled = true
	s := createMockSession(char, srv)
	s.userID = user
	save, err := GetCharacterSaveData(s, char)
	if err != nil || save.starterEpoch == nil || save.starterEpoch.Epoch != 1 || !bytes.Equal(save.compSave, current) {
		t.Fatal("recovery borrowed the current generation for a pre-gift backup", err)
	}
	if err := save.Save(s); err != nil {
		t.Fatal("same-generation recovered server blob failed to save", err)
	}
	db.MustExec(`DELETE FROM savedata_backups WHERE char_id=$1 AND starter_inventory_epoch=1`, char)
	db.MustExec(`UPDATE characters SET savedata=$1,savedata_hash=$2 WHERE id=$3`, compress(3), []byte("wrong-checksum"), char)
	if _, err := GetCharacterSaveData(s, char); err == nil {
		t.Fatal("unknown or pre-gift backup was accepted after current backup was removed")
	}
}

func TestQuestLicenseStarterEpochOverrideCannotInventClientBaseline(t *testing.T) {
	s, repo := newStarterEpochWiringSession(t)
	if err := os.WriteFile(filepath.Join(s.server.erupeConfig.BinPath, "save_override.bin"), []byte("unverified-inventory"), 0600); err != nil {
		t.Fatal(err)
	}
	handleMsgMhfLoaddata(s, &mhfpacket.MsgMhfLoaddata{AckHandle: 61})
	if !s.closed.Load() || repo.loadCalls != 0 || s.questLicenseClientInventory.Load() != nil {
		t.Fatal("debug override acquired an authenticated inventory generation")
	}
}
