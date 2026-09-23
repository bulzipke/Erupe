package channelserver

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestRepoDivaMelodyPreviewDoesNotUnlockSpending(t *testing.T) {
	r, db, char, guild, event, battle := setupDivaGuildRewardRepoTest(t, 0)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	// An existing positive wallet must not let a preview bypass the actual
	// welcome period or the guild's one-area entitlement.
	if _, err := db.Exec(`INSERT INTO diva_melody_wallets(user_id,event_id,earned,spent) VALUES($1,$2,3,1)`, user, event.ID); err != nil {
		t.Fatal(err)
	}
	s := createMockSession(char, createMockServer())
	s.userID = user
	s.server.divaRepo = r
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	s.server.erupeConfig.DebugOptions.DivaOverride = 2
	for range 2 {
		handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 1, ShopType: 9, Limit: 512})
		a := readAck(t, s)
		if a.ErrorCode != 0 || len(a.Payload) != 4+76*30 || binary.BigEndian.Uint16(a.Payload[:2]) != 76 {
			t.Fatal(a)
		}
	}
	if _, err := r.useDivaMelodyAt(user, char, 1, 2, battle); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal("preview permitted battle-phase spending", err)
	}
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude)*time.Second + time.Minute)
	if _, err := r.useDivaMelodyAt(user, char, 1, 3, welcome); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal("preview granted unearned hall access", err)
	}
	var earned, spent int
	if err := db.QueryRow(`SELECT earned,spent FROM diva_melody_wallets WHERE user_id=$1 AND event_id=$2`, user, event.ID).Scan(&earned, &spent); err != nil || earned != 3 || spent != 1 {
		t.Fatal("preview or rejected purchase changed wallet", earned, spent, err)
	}
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, battle, 1)
	if got, err := r.useDivaMelodyAt(user, char, 1, 3, welcome); err != nil || got != 1 {
		t.Fatal("eligible purchase no longer works", got, err)
	}
	if _, err := db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if _, err := r.useDivaMelodyAt(user, char, 1, 3, welcome); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal("preview permitted spending after leaving guild", err)
	}
	if err := db.QueryRow(`SELECT earned,spent FROM diva_melody_wallets WHERE user_id=$1 AND event_id=$2`, user, event.ID).Scan(&earned, &spent); err != nil || earned != 3 || spent != 2 {
		t.Fatal("unexpected final wallet", earned, spent, err)
	}
}
