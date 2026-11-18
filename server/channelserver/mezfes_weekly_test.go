package channelserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestMezWeeklyNoonAndNotices(t *testing.T) {
	monday := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ now, want time.Time }{
		{monday.Add(-time.Nanosecond), monday.Add(-mezWeek)},
		{monday, monday}, {monday.Add(mezWeek - time.Nanosecond), monday},
	} {
		if got := mezWeekStart(tc.now); !got.Equal(tc.want) {
			t.Fatalf("start at %v: %v", tc.now, got)
		}
	}
	lines, total := mezAwardNotice([]mezAward{{467, 2, 800}, {464, 4, 100}, {465, 1, 3000}})
	if total != 3900 || len(lines) != 4 ||
		lines[0] != "메제포르타 페스타 참여로 길드 공헌도를 3900 받았습니다." ||
		!strings.HasPrefix(lines[1], " ▷ 다같이 볼뻐끔") || !strings.HasSuffix(lines[1], "1위: 3000") ||
		!strings.HasPrefix(lines[2], " ▶ 울키 새총") || !strings.HasSuffix(lines[2], "참가상: 100") ||
		!strings.HasPrefix(lines[3], " ▷ 냥련도") || !strings.HasSuffix(lines[3], "2위: 800") {
		t.Fatalf("award %v: %q", total, lines)
	}
	if lines, _ := mezAwardNotice(nil); len(lines) != 0 {
		t.Fatal(lines)
	}
}

func TestMezWeeklyBoothAndWalletPackets(t *testing.T) {
	db := SetupTestDB(t)
	server := createMockServer()
	server.db = db
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.charRepo = NewCharacterRepository(db)
	server.mercenaryRepo = NewMercenaryRepository(db)
	uid := CreateTestUser(t, db, "mez_protocol")
	liveID := CreateTestCharacter(t, db, uid, "Live")
	offlineID := CreateTestCharacter(t, db, uid, "Offline")
	live := createMockSession(liveID, server)
	live.userID = uid
	query := func(s *Session, op uint32) []byte {
		t.Helper()
		handleMsgMhfEnumerateItem(s, &mhfpacket.MsgMhfEnumerateItem{AckHandle: 7, CampaignID: mezWeeklyRequest | op})
		data := assertMercenaryAck(t, s, 0).AckData
		if len(data) < 12 || string(data[:4]) != "MZW1" {
			t.Fatalf("bad weekly response %x", data)
		}
		return data
	}
	week := mezWeekStart(time.Now())
	for i, id := range []uint32{liveID, offlineID} {
		if _, err := db.Exec(`INSERT INTO mezfes_plays(character_id,event_id,ticket_kind,stage_id,points,raw_score,scored_at)
			VALUES ($1,1,0,464,1,17,$2)`, id, week.Add(-time.Minute).Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if board := query(live, 465); len(board) != 16 || binary.BigEndian.Uint32(board[8:]) != 0 || binary.BigEndian.Uint32(board[12:]) != 500 {
		t.Fatalf("empty board %x", board)
	}
	for i, id := range []uint32{liveID, offlineID} {
		if _, err := db.Exec(`INSERT INTO mezfes_plays(character_id,event_id,ticket_kind,stage_id,points,raw_score,scored_at)
			VALUES ($1,1,0,464,1,$2,$3)`, id, i+1, week); err != nil {
			t.Fatal(err)
		}
	}
	board := query(live, 464)
	if len(board) != 104 || binary.BigEndian.Uint32(board[8:]) != 2 || binary.BigEndian.Uint32(board[12:]) != 1 ||
		binary.BigEndian.Uint32(board[16:]) != 2 || !bytes.HasPrefix(board[24:], []byte("Offline\x00")) ||
		binary.BigEndian.Uint32(board[100:]) != 100 {
		t.Fatalf("ranked board %x", board)
	}
	// This session already loaded its wallet before the weekly cutoff.
	live.mezWalletLoaded = true
	for i := 0; i < 2; i++ {
		if delta := binary.BigEndian.Uint32(query(live, 0x8000|mezMaxWeeklyGP)[8:]); delta != 1000 {
			t.Fatalf("unacknowledged delta %d", delta)
		}
	}
	save := func(gp uint32) {
		handleMsgMhfSaveMercenary(live, &mhfpacket.MsgMhfSaveMercenary{GCP: gp})
		assertMercenaryAck(t, live, 0)
	}
	save(13)
	balance, err := readCharacterInt(live, "gcp")
	if err != nil || balance != 1013 {
		t.Fatalf("stale save lost reward: %d %v", balance, err)
	}
	query(live, 1)
	if !bytes.Contains((<-live.sendPackets).data, stringsupport.UTF8ToSJIS("울키 새총")) {
		t.Fatal("missing live chat")
	}
	save(1011)
	if binary.BigEndian.Uint32(query(live, 0x8000|mezMaxWeeklyGP)[8:]) != 0 {
		t.Fatal("repeated credit")
	}
	balance, err = readCharacterInt(live, "gcp")
	if err != nil || balance != 1011 {
		t.Fatalf("post-ack save added the reward again: %d %v", balance, err)
	}
	offline := createMockSession(offlineID, server)
	offline.userID = uid
	login := func() {
		handleMsgMhfReadMercenaryW(offline, &mhfpacket.MsgMhfReadMercenaryW{})
		data := assertMercenaryAck(t, offline, 0).AckData
		if len(data) < 16 || !bytes.HasSuffix(data, []byte("MZW1RAP1RGE1")) || binary.BigEndian.Uint32(data[len(data)-16:]) != 800 {
			t.Fatalf("login wallet %x", data)
		}
	}
	login()
	offline.mezChatReady = true
	offline.sendMezLoginNotice()
	if !bytes.Contains((<-offline.sendPackets).data, stringsupport.UTF8ToSJIS("울키 새총")) {
		t.Fatal("missing login chat")
	}
	login()
	if len(offline.sendPackets) != 0 {
		t.Fatal("duplicate login chat")
	}
}

func TestMezWeeklyPostgres(t *testing.T) {
	db := SetupTestDB(t)
	uid := CreateTestUser(t, db, "mez_weekly")
	ids := make([]uint32, 5)
	for i, name := range []string{"First", "Second", "Third", "Fourth", "Missing"} {
		ids[i] = CreateTestCharacter(t, db, uid, name)
	}
	now := time.Now()
	start := mezWeekStart(now).Add(-mezWeek)
	play := func(idx, stage int, score interface{}, when time.Time, cancelled bool) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO mezfes_plays(character_id,event_id,ticket_kind,stage_id,points,raw_score,scored_at,cancelled)
			VALUES($1,1,0,$2,1,$3,$4,$5)`, ids[idx], stage, score, when, cancelled); err != nil {
			t.Fatal(err)
		}
	}
	for _, stage := range []int{463, 464, 465, 466, 467, 468, 469} {
		play(0, stage, 150, start.Add(time.Minute), false)
		play(0, stage, 10, start, false)
		play(1, stage, 150, start.Add(2*time.Minute), false)
		play(2, stage, 20, start, false)
		play(3, stage, 0, start, false)
		play(4, stage, nil, start, false)
		play(3, stage, 900, start.Add(mezWeek), false)
		play(3, stage, 700, start, true)
	}
	for i, want := range []int{11000, 8000, 4500, 1500, 1500} {
		s := &Session{server: &Server{db: db}, charID: ids[i], userID: uid}
		ctx := context.Background()
		if i == 0 {
			balance, rows, err := s.creditMezWeek(ctx, now, want-1)
			if err != errMezWalletFull || balance != 0 || len(rows) != 0 {
				t.Fatalf("live wallet cap: %d %v %v", balance, rows, err)
			}
			if _, err = db.Exec("UPDATE characters SET gcp=$1 WHERE id=$2", mezWalletCap, ids[i]); err != nil {
				t.Fatal(err)
			}
			balance, rows, err = s.creditMezWeek(ctx, now, want)
			if err != errMezWalletFull || balance != mezWalletCap || len(rows) != 0 {
				t.Fatalf("login wallet cap: %d %v %v", balance, rows, err)
			}
			if _, err = db.Exec("UPDATE characters SET gcp=0 WHERE id=$1", ids[i]); err != nil {
				t.Fatal(err)
			}
			// A later import into a closed round must not change its standings.
			play(4, 465, 999, start, false)
		}
		if i == 4 {
			balance, rows, err := s.creditMezWeek(ctx, start.Add(2*mezWeek), mezMaxWeeklyGP)
			if err != nil || balance != 0 || len(rows) != 0 {
				t.Fatalf("expired round: %d %v %v", balance, rows, err)
			}
		}
		balance, rows, err := s.creditMezWeek(ctx, now, mezMaxWeeklyGP)
		if err != nil || balance != want || len(rows) != 7 {
			t.Fatalf("hunter %d: %d %+v %v", i, balance, rows, err)
		}
		balance, rows, err = s.creditMezWeek(ctx, now, mezMaxWeeklyGP)
		if err != nil || balance != want || len(rows) != 0 {
			t.Fatalf("duplicate %d: %d %+v %v", i, balance, rows, err)
		}
	}
}
