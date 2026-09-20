package channelserver

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/config"
	"erupe-ce/network/clientctx"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func TestDailyCoinIntervalUnion(t *testing.T) {
	var spans []dailyCoinInterval
	var total int64
	// Reports deliberately arrive out of order, overlap, repeat and have gaps.
	for _, span := range []dailyCoinInterval{{500, 1500}, {0, 1000}, {0, 1000}, {2000, 2500}, {1500, 2000}} {
		spans, total = mergeDailyCoinIntervals(spans, span)
	}
	if total != 2500 || !reflect.DeepEqual(spans, []dailyCoinInterval{{0, 2500}}) {
		t.Fatalf("union = %v, %d", spans, total)
	}
	_, total = mergeDailyCoinIntervals(spans, dailyCoinInterval{4000, 5000})
	if total != 3500 {
		t.Fatalf("offline gap was counted: %d", total)
	}
}

func TestDailyCoinMessagesAndCalendar(t *testing.T) {
	midnight := dailyCoinMidnight(time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC))
	if midnight.Format(time.RFC3339) != "2026-09-21T00:00:00+09:00" {
		t.Fatal(midnight)
	}
	messages := dailyCoinMessages(dailyCoinResult{OnlineMS: 30*60000 + 1}, true)
	if len(messages) != 2 || !strings.HasPrefix(messages[0], "30분") {
		t.Fatal(messages)
	}
	if got := dailyCoinMessages(dailyCoinResult{OnlineMS: 3599999}, true); !strings.HasPrefix(got[0], "1분") {
		t.Fatal(got)
	}
	if got := dailyCoinMessages(dailyCoinResult{}, false); len(got) != 0 {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		name     string
		result   dailyCoinResult
		entering bool
		want     []string
	}{
		{"first", dailyCoinResult{First: true, Balance: 130}, true, []string{
			"오늘의 첫 접속으로 뽑기 코인 10개를 획득했습니다!",
			"60분 더 접속하면 뽑기 코인 10개를 추가로 획득할 수 있습니다.",
			"뽑기 코인 보유: 130개",
		}},
		{"midnight", dailyCoinResult{First: true, Balance: 20}, false, []string{
			"오늘의 첫 접속으로 뽑기 코인 10개를 획득했습니다!",
			"60분 더 접속하면 뽑기 코인 10개를 추가로 획득할 수 있습니다.",
			"뽑기 코인 보유: 20개",
		}},
		{"retry_both", dailyCoinResult{First: true, Bonus: true, Complete: true, Balance: 20}, true, []string{
			"오늘의 첫 접속으로 뽑기 코인 10개를 획득했습니다!",
			"뽑기 코인 10개를 추가 획득했습니다!",
			"뽑기 코인 보유: 20개",
		}},
		{"bonus", dailyCoinResult{Bonus: true, Complete: true, Balance: 140}, false, []string{
			"뽑기 코인 10개를 추가 획득했습니다!", "뽑기 코인 보유: 140개",
		}},
		{"returning", dailyCoinResult{OnlineMS: 30 * 60000, Balance: 110}, true, []string{
			"30분 더 접속하면 뽑기 코인 10개를 추가로 획득할 수 있습니다.", "뽑기 코인 보유: 110개",
		}},
		{"complete", dailyCoinResult{Complete: true, Balance: 120}, true, []string{"뽑기 코인 보유: 120개"}},
		{"complete_idle", dailyCoinResult{Complete: true, Balance: 120}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dailyCoinMessages(tc.result, tc.entering); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func assertDailyCoinChatBatch(t *testing.T, data []byte, messages []string) {
	t.Helper()
	bf := byteframe.NewByteFrameFromBytes(data)
	for _, message := range messages {
		var pkt mhfpacket.MsgSysCastedBinary
		if opcode := bf.ReadUint16(); opcode != uint16(pkt.Opcode()) {
			t.Fatalf("unexpected opcode %x", opcode)
		}
		if err := pkt.Parse(bf, nil); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pkt.RawDataPayload, makeServerChatMessage(message).RawDataPayload) {
			t.Fatalf("out-of-order/missing message: %q", message)
		}
	}
	if bf.Err() != nil || len(bf.DataFromCurrent()) != 0 {
		t.Fatalf("malformed/trailing batch data: %v", bf.Err())
	}
}

func TestDailyCoinNoticeBatchBackpressure(t *testing.T) {
	s := &Session{
		server: &Server{erupeConfig: &config.Config{}}, logger: zap.NewNop(),
		sendPackets: make(chan packet, 1), done: make(chan struct{}),
	}
	messages := dailyCoinMessages(dailyCoinResult{First: true, Balance: 10}, true)
	s.sendPackets <- packet{} // Full queue must not drop any of the three lines.
	finished := make(chan struct{})
	go func() { sendServerChatMessages(s, messages); close(finished) }()
	select {
	case <-finished:
		t.Fatal("notice did not wait for queue space")
	case <-time.After(20 * time.Millisecond):
	}
	<-s.sendPackets
	select {
	case <-finished:
	case <-time.After(time.Second):
		s.markClosed()
		t.Fatal("notice stuck after freeing queue space")
	}
	assertDailyCoinChatBatch(t, (<-s.sendPackets).data, messages)
	if len(s.sendPackets) != 0 {
		t.Fatal("notice was not a single queue entry")
	}

	// A disconnected client must also unblock a saturated queue.
	s.sendPackets <- packet{}
	finished = make(chan struct{})
	go func() { sendServerChatMessages(s, messages); close(finished) }()
	s.markClosed()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not unblock notice")
	}
}

func TestDailyCoinPremiumBalanceSnapshot(t *testing.T) {
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "daily_coin_balance")
	if _, err := db.Exec("UPDATE users SET gacha_premium=123,gacha_trial=77 WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, dailyCoinLocation)
	result, err := settleDailyCoins(context.Background(), db, user, now, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Balance != 133 {
		t.Fatal(result)
	}
	var trial int
	if err := db.Get(&trial, "SELECT gacha_trial FROM users WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	if trial != 77 {
		t.Fatalf("unrelated trial balance changed: %d", trial)
	}
	// Spending coins does not reset daily eligibility; return notices use the
	// current account balance, not today's earned amount or a cached total.
	if _, err := db.Exec("UPDATE users SET gacha_premium=gacha_premium-100 WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	result, err = settleDailyCoins(context.Background(), db, user, now, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].Balance != 33 || result[0].First {
		t.Fatal(result)
	}
}

func TestDailyCoinSessionWorker(t *testing.T) {
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "daily_coin_worker")
	newSession := func() *Session {
		return &Session{
			server: &Server{db: db, erupeConfig: &config.Config{}},
			logger: zap.NewNop(), userID: user, done: make(chan struct{}),
			sendPackets:   make(chan packet, 20),
			clientContext: &clientctx.ClientContext{RealClientMode: config.ZZ},
		}
	}
	s := newSession()
	s.startDailyCoins()
	firstWorker := s.dailyCoinsDone
	s.startDailyCoins()
	if s.dailyCoinsDone != firstWorker {
		t.Fatal("stage entry started a second worker")
	}
	// Disconnect before the first scheduled checkpoint: final settlement must
	// still grant first login and preserve the short connected interval.
	time.Sleep(20 * time.Millisecond)
	s.markClosed()
	s.waitDailyCoins()
	var balance, online int
	if err := db.Get(&balance, "SELECT gacha_premium FROM users WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	if balance != 10 {
		t.Fatalf("final grant = %d", balance)
	}
	if err := db.Get(&online, "SELECT SUM(online_ms) FROM daily_gacha_coins WHERE user_id=$1", user); err != nil {
		t.Fatal(err)
	}
	if online < 1 {
		t.Fatal("disconnect lost the last partial interval")
	}

	// Seed 59m59s, then use the real worker to cross the threshold and emit chat.
	now := time.Now()
	if now.Sub(dailyCoinMidnight(now)) < time.Hour || time.Until(dailyCoinMidnight(now).AddDate(0, 0, 1)) < 5*time.Second {
		return // Avoid a wall-clock-dependent midnight edge in this runtime test.
	}
	if _, err := settleDailyCoins(context.Background(), db, user, now.Add(-time.Hour), now.Add(-time.Second), false); err != nil {
		t.Fatal(err)
	}
	s = newSession()
	s.startDailyCoins()
	defer func() { s.markClosed(); s.waitDailyCoins() }()
	select {
	case sent := <-s.sendPackets:
		assertDailyCoinChatBatch(t, sent.data, []string{"뽑기 코인 10개를 추가 획득했습니다!", "뽑기 코인 보유: 20개"})
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not emit the bonus and balance notifications")
	}
	if err := db.Get(&balance, "SELECT gacha_premium FROM users WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	if balance != 20 {
		t.Fatalf("worker bonus balance = %d", balance)
	}
	// Disable switch prevents a new worker.
	disabled := newSession()
	disabled.server.erupeConfig.GameplayOptions.DisableDailyGachaCoins = true
	disabled.startDailyCoins()
	if disabled.dailyCoinsDone != nil {
		t.Fatal("disabled reward started a worker")
	}
}

func TestDailyCoinDatabaseLifecycle(t *testing.T) {
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "daily_coin_lifecycle")
	other := CreateTestUser(t, db, "daily_coin_other")
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, dailyCoinLocation)
	settle := func(user uint32, from, until time.Time, live bool) []dailyCoinResult {
		t.Helper()
		result, err := settleDailyCoins(context.Background(), db, user, from, until, live)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	balance := func(user uint32, want int) {
		t.Helper()
		var got int
		if err := db.Get(&got, "SELECT COALESCE(gacha_premium,0) FROM users WHERE id=$1", user); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("user %d balance %d, want %d", user, got, want)
		}
	}
	if got := settle(user, start, start, true); len(got) != 1 || !got[0].First {
		t.Fatal(got)
	}
	balance(user, 10)
	settle(user, start, start.Add(30*time.Minute), false)
	// Two hours offline must not count. A new session resumes the same account.
	rejoin := start.Add(150 * time.Minute)
	got := settle(user, rejoin, rejoin, true)
	if got[0].OnlineMS != 30*60000 || got[0].First || got[0].Bonus {
		t.Fatal(got)
	}
	balance(user, 10)
	// Replays and overlapping characters must not double the time or grant.
	settle(user, start, start.Add(30*time.Minute), true)
	settle(user, rejoin, rejoin.Add(29*time.Minute), true)
	balance(user, 10)
	got = settle(user, rejoin, rejoin.Add(30*time.Minute), true)
	if !got[0].Bonus || !got[0].Complete {
		t.Fatal(got)
	}
	balance(user, 20)
	settle(user, start, rejoin.Add(time.Hour), true)
	balance(user, 20)
	settle(other, start, start, true)
	balance(other, 10)

	// Continuous connection: midnight starts a fresh day even after completion.
	midnight := dailyCoinMidnight(start).AddDate(0, 0, 1)
	got = settle(user, rejoin.Add(time.Hour), midnight, true)
	if len(got) != 2 || !got[1].First || got[1].OnlineMS != 0 {
		t.Fatal(got)
	}
	if oldMessages := dailyCoinMessages(got[0], false); len(oldMessages) != 0 {
		t.Fatalf("completed yesterday emitted another balance: %v", oldMessages)
	}
	wantMidnight := []string{
		"오늘의 첫 접속으로 뽑기 코인 10개를 획득했습니다!",
		"60분 더 접속하면 뽑기 코인 10개를 추가로 획득할 수 있습니다.",
		"뽑기 코인 보유: 30개",
	}
	if messages := dailyCoinMessages(got[1], false); !reflect.DeepEqual(messages, wantMidnight) {
		t.Fatalf("midnight notice = %v", messages)
	}
	balance(user, 30)
	for _, result := range settle(user, midnight, midnight.Add(15*time.Second), true) {
		if messages := dailyCoinMessages(result, false); len(messages) != 0 {
			t.Fatalf("next checkpoint repeated the midnight notice: %v", messages)
		}
	}
	settle(user, midnight, midnight.Add(time.Hour), true)
	balance(user, 40)

	// Crossing midnight must not transfer yesterday's partial hour.
	cross := CreateTestUser(t, db, "daily_coin_midnight")
	got = settle(cross, midnight.Add(-30*time.Minute), midnight.Add(30*time.Minute), true)
	if len(got) != 2 || got[0].OnlineMS != 30*60000 || got[1].OnlineMS != 30*60000 {
		t.Fatal(got)
	}
	balance(cross, 20) // First-login reward on each date, no one-hour bonus yet.
	settle(cross, midnight.Add(30*time.Minute), midnight.Add(time.Hour), true)
	balance(cross, 30)

	// Disconnect exactly at midnight: do not grant the next day's first login.
	edge := CreateTestUser(t, db, "daily_coin_edge")
	got = settle(edge, midnight.Add(-time.Minute), midnight, false)
	if len(got) != 1 {
		t.Fatal(got)
	}
	balance(edge, 10)
}

func TestDailyCoinDatabaseConcurrentAndRollback(t *testing.T) {
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "daily_coin_concurrent")
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, dailyCoinLocation)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := settleDailyCoins(context.Background(), db, user, start, start.Add(30*time.Minute), true)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got int
	if err := db.Get(&got, "SELECT gacha_premium FROM users WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("concurrent first grants: %d", got)
	}
	if err := db.Get(&got, "SELECT online_ms FROM daily_gacha_coins WHERE user_id=$1", user); err != nil {
		t.Fatal(err)
	}
	if got != 1800000 {
		t.Fatalf("overlapping time multiplied: %d", got)
	}

	// Fail the balance write after computing the bonus: all state must roll back.
	if _, err := db.Exec("UPDATE users SET gacha_premium=2147483647 WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	_, err := settleDailyCoins(context.Background(), db, user, start, start.Add(time.Hour), true)
	if err == nil {
		t.Fatal("expected integer overflow")
	}
	var claimed bool
	if err := db.Get(&claimed, "SELECT bonus_claimed FROM daily_gacha_coins WHERE user_id=$1", user); err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("failed credit consumed the bonus")
	}
	if _, err := db.Exec("UPDATE users SET gacha_premium=10 WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = settleDailyCoins(context.Background(), db, user, start, start.Add(time.Hour), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Get(&got, "SELECT gacha_premium FROM users WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	if got != 20 {
		t.Fatalf("retry balance %d", got)
	}
}
