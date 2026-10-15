package channelserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"erupe-ce/network/clientctx"
	"erupe-ce/server/dashboarddelivery"
	"github.com/jmoiron/sqlx"
)

const coinLease1 = "10000000-0000-4000-8000-000000000001"
const coinLease2 = "10000000-0000-4000-8000-000000000002"

func TestDashboardCoinNoticeMessage(t *testing.T) {
	if got := dashboarddelivery.CoinNotice(50); got != "보상으로 금빛 금화G가 50개 지급되었습니다." {
		t.Fatal(got)
	}
}

func queueSessionTestCoins(t *testing.T, db *sqlx.DB, userID, charID uint32) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO dashboard_item_deliveries
		(request_id,delivery_kind,user_id,character_id,character_name,character_code,item_id,item_name,quantity,mail_ids,pending)
		VALUES('20000000-0000-4000-8000-000000000001','gacha_premium',$1,$2,'SessionChar','111111',0,'',50,'{}',true)`, userID, charID); err != nil {
		t.Fatal(err)
	}
}

func sessionCoinBalance(t *testing.T, db *sqlx.DB, userID uint32) int {
	t.Helper()
	var balance int
	if err := db.Get(&balance, `SELECT COALESCE(gacha_premium,0) FROM users WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	return balance
}

func TestDashboardCoinSessionWaitsForAllSavers(t *testing.T) {
	repo, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	CreateTestServer(t, db, 2)
	if err := repo.BindTrackedSession(token, 1, charID, coinLease1); err != nil {
		t.Fatal(err)
	}
	if err := repo.BindTrackedSession(token, 2, charID, coinLease2); err != nil {
		t.Fatal(err)
	}
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	if err := repo.FinishTrackedSession(context.Background(), coinLease1, userID, charID, token, true); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before {
		t.Fatal("credited during channel transfer", after, before)
	}
	var bound int
	if err := db.Get(&bound, `SELECT server_id FROM sign_sessions WHERE token=$1`, token); err != nil || bound != 2 {
		t.Fatal("old connection cleared new binding", bound, err)
	}
	if err := repo.FinishTrackedSession(context.Background(), coinLease2, userID, charID, token, true); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishTrackedSession(context.Background(), coinLease2, userID, charID, token, true); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before+50 {
		t.Fatal(after, before)
	}
	var messages []string
	for i := 0; i < 2; i++ {
		more, err := dashboarddelivery.SendNextNotice(context.Background(), db, userID, charID, func(message string) error { messages = append(messages, message); return nil })
		if err != nil || more != (i == 0) {
			t.Fatal(more, err)
		}
	}
	if len(messages) != 1 || messages[0] != dashboarddelivery.CoinNotice(50) {
		t.Fatal(messages)
	}
}

func TestDashboardCoinSessionFailedSaveDefersUntilSuccessfulSave(t *testing.T) {
	repo, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	if err := repo.BindTrackedSession(token, 1, charID, coinLease1); err != nil {
		t.Fatal(err)
	}
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	if err := repo.FinishTrackedSession(context.Background(), coinLease1, userID, charID, token, false); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before {
		t.Fatal("failed save credited", after)
	}
	if err := repo.BindTrackedSession(token, 1, charID, coinLease2); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before {
		t.Fatal("login discarded failed-save guard", after)
	}
	if err := repo.FinishTrackedSession(context.Background(), coinLease2, userID, charID, token, true); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before+50 {
		t.Fatal(after, before)
	}
}

func TestDashboardCoinSessionSiblingDefersGrant(t *testing.T) {
	repo, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	sibling := CreateTestCharacter(t, db, userID, "Sibling")
	CreateTestSignSession(t, db, userID, "sibling_token")
	if err := repo.BindTrackedSession(token, 1, charID, coinLease1); err != nil {
		t.Fatal(err)
	}
	if err := repo.BindTrackedSession("sibling_token", 1, sibling, coinLease2); err != nil {
		t.Fatal(err)
	}
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	if err := repo.FinishTrackedSession(context.Background(), coinLease1, userID, charID, token, true); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before {
		t.Fatal(after)
	}
	if err := repo.FinishTrackedSession(context.Background(), coinLease2, userID, sibling, "sibling_token", true); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before+50 {
		t.Fatal(after)
	}
	if _, err := dashboarddelivery.SendNextNotice(context.Background(), db, userID, sibling, func(string) error { return errors.New("sibling must not get notice") }); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardCoinSessionRestartRecovery(t *testing.T) {
	repo, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	CreateTestServer(t, db, 2)
	if err := repo.BindTrackedSession(token, 1, charID, coinLease1); err != nil {
		t.Fatal(err)
	}
	if err := repo.BindTrackedSession(token, 2, charID, coinLease2); err != nil {
		t.Fatal(err)
	}
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	if err := repo.RecoverDashboardCoinSessions(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before {
		t.Fatal("other channel lease discarded", after)
	}
	if err := repo.RecoverDashboardCoinSessions(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecoverDashboardCoinSessions(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before+50 {
		t.Fatal(after, before)
	}
}

func TestDashboardCoinSessionBindSettlesOfflineQueue(t *testing.T) {
	repo, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	if err := repo.BindTrackedSession(token, 1, charID, coinLease1); err != nil {
		t.Fatal(err)
	}
	if after := sessionCoinBalance(t, db, userID); after != before+50 {
		t.Fatal(after, before)
	}
}

func TestDashboardCoinNoticeWaitsForSocketWrite(t *testing.T) {
	mock := &MockCryptConn{}
	s := createTestSession(mock)
	s.done = make(chan struct{})
	s.clientContext = &clientctx.ClientContext{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.sendDashboardCoinNotice(ctx, dashboarddelivery.CoinNotice(50)) }()
	deadline := time.Now().Add(time.Second)
	for len(s.sendPackets) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.sendPackets) != 1 {
		t.Fatal("notice not queued")
	}
	select {
	case err := <-result:
		t.Fatalf("notice consumed before socket write: %v", err)
	default:
	}
	go s.sendLoop()
	defer s.markClosed()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if mock.PacketCount() != 1 {
		t.Fatal(mock.PacketCount())
	}
}

func TestDashboardCoinNoticeExpiredQueueNotSent(t *testing.T) {
	mock := &MockCryptConn{}
	s := createTestSession(mock)
	s.done = make(chan struct{})
	s.clientContext = &clientctx.ClientContext{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.sendDashboardCoinNotice(ctx, dashboarddelivery.CoinNotice(50)); err == nil {
		t.Fatal("unsent notice was marked sent")
	}
	go s.sendLoop()
	defer s.markClosed()
	deadline := time.Now().Add(time.Second)
	for len(s.sendPackets) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if mock.PacketCount() != 0 {
		t.Fatal("expired notice sent on later retry", mock.PacketCount())
	}
}

func TestDashboardCoinLoginWorkerOnlyOnce(t *testing.T) {
	_, db, userID, charID, _, _ := setupSessionRepo(t)
	queueSessionTestCoins(t, db, userID, charID)
	if _, err := db.Exec(`UPDATE dashboard_item_deliveries SET pending=false,coin_balance=50,credited_at=NOW()`); err != nil {
		t.Fatal(err)
	}
	mock := &MockCryptConn{}
	s := createTestSession(mock)
	s.server.db = db
	s.userID = userID
	s.charID = charID
	s.done = make(chan struct{})
	s.clientContext = &clientctx.ClientContext{}
	go s.sendLoop()
	defer s.markClosed()
	s.startDashboardCoinNotices()
	s.startDashboardCoinNotices()
	select {
	case <-s.dashboardNoticesDone:
	case <-time.After(4 * time.Second):
		t.Fatal("notice worker did not finish")
	}
	s.startDashboardCoinNotices()
	if mock.PacketCount() != 1 {
		t.Fatal("stage reentry repeated notice", mock.PacketCount())
	}
	var read bool
	if err := db.Get(&read, `SELECT notified_at IS NOT NULL FROM dashboard_item_deliveries`); err != nil || !read {
		t.Fatal(read, err)
	}
}

func TestDashboardCoinActualLogoutWaitsForSave(t *testing.T) {
	_, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	server := createTestServerWithDB(t, db)
	server.ID = 1
	s := createTestSessionForServerWithChar(server, charID, "SessionChar")
	s.userID = userID
	s.token = token
	s.done = make(chan struct{})
	if err := s.bindDashboardCoinSession(); err != nil {
		t.Fatal(err)
	}
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	unlock := server.charSaveLocks.Lock(charID)
	finished := make(chan struct{})
	go func() { logoutPlayer(s); close(finished) }()
	select {
	case <-finished:
		unlock()
		t.Fatal("logout skipped final save lock")
	case <-time.After(50 * time.Millisecond):
	}
	if after := sessionCoinBalance(t, db, userID); after != before {
		unlock()
		t.Fatal("payment happened before final save", after, before)
	}
	unlock()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("logout did not complete")
	}
	if after := sessionCoinBalance(t, db, userID); after != before+50 {
		t.Fatal("successful logout failed to credit", after, before)
	}
}

func TestDashboardCoinActualLogoutSaveFailureRetainsQueue(t *testing.T) {
	_, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	server := createTestServerWithDB(t, db)
	server.ID = 1
	s := createTestSessionForServerWithChar(server, charID, "SessionChar")
	s.userID = userID
	s.token = token
	s.done = make(chan struct{})
	if err := s.bindDashboardCoinSession(); err != nil {
		t.Fatal(err)
	}
	queueSessionTestCoins(t, db, userID, charID)
	before := sessionCoinBalance(t, db, userID)
	charRepo := &mockCharacterRepo{loadSaveDataErr: errors.New("save unavailable")}
	server.charRepo = charRepo
	logoutPlayer(s)
	if after := sessionCoinBalance(t, db, userID); after != before {
		t.Fatal("failed save credited", after, before)
	}
	var failed bool
	if err := db.Get(&failed, `SELECT save_failed FROM dashboard_coin_sessions WHERE id=$1::uuid`, s.dashboardCoinSessionID); err != nil || !failed {
		t.Fatal(failed, err)
	}
}
