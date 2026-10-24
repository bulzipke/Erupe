package channelserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// This driver supplies owned in-memory result rows. It neither evaluates the
// SQL expression nor connects to PostgreSQL; the real SQL differential remains
// a separate, unexecuted gate. Unsupported writes/transactions fail explicitly.
type returnCandidateConnector struct {
	columns []driver.Value
	err     error
}

func (c returnCandidateConnector) Connect(context.Context) (driver.Conn, error) {
	return returnCandidateConnection{c}, nil
}
func (c returnCandidateConnector) Driver() driver.Driver { return returnCandidateDriver{} }

type returnCandidateDriver struct{}

func (returnCandidateDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("no external connections")
}

type returnCandidateConnection struct{ returnCandidateConnector }

func (returnCandidateConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not allowed")
}
func (returnCandidateConnection) Close() error { return nil }
func (returnCandidateConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction not allowed")
}
func (c returnCandidateConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if query != readReturnQuestExpiriesSQL || len(args) != 1 || args[0].Value != int64(77) {
		return nil, errors.New("unexpected query or unauthenticated account")
	}
	if c.err != nil {
		return nil, c.err
	}
	return &returnCandidateRows{columns: c.columns}, nil
}

type returnCandidateRows struct {
	columns []driver.Value
	done    bool
}

func (*returnCandidateRows) Columns() []string {
	return []string{"id", "return_kind", "return_finite", "newcomer_kind", "newcomer_finite"}
}
func (*returnCandidateRows) Close() error { return nil }
func (r *returnCandidateRows) Next(destination []driver.Value) error {
	if r.done || r.columns == nil {
		return io.EOF
	}
	r.done = true
	copy(destination, r.columns)
	return nil
}

func TestReturnQuestCandidateRepositoryProjectionScan(t *testing.T) {
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		columns       []driver.Value
		queryError    error
		wantError     bool
		first, second returnQuestExpiryKind
	}{
		{name: "NULL NULL", columns: []driver.Value{int64(77), int64(0), nil, int64(0), nil}},
		{name: "negative and positive infinity", columns: []driver.Value{int64(77), int64(1), nil, int64(3), nil}, first: returnQuestExpiryNegativeInfinity, second: returnQuestExpiryPositiveInfinity},
		{name: "original finite pair", columns: []driver.Value{int64(77), int64(2), at, int64(2), at.Add(time.Hour)}, first: returnQuestExpiryFinite, second: returnQuestExpiryFinite},
		{name: "no row", wantError: true},
		{name: "query error", queryError: errors.New("owned driver error"), wantError: true},
		{name: "foreign account", columns: []driver.Value{int64(78), int64(3), nil, int64(0), nil}, wantError: true},
		{name: "finite NULL inconsistent", columns: []driver.Value{int64(77), int64(2), nil, int64(0), nil}, wantError: true},
		{name: "nonfinite time inconsistent", columns: []driver.Value{int64(77), int64(3), at, int64(0), nil}, wantError: true},
		{name: "unknown kind", columns: []driver.Value{int64(77), int64(4), nil, int64(0), nil}, wantError: true},
		{name: "unknown negative kind", columns: []driver.Value{int64(77), int64(-1), nil, int64(0), nil}, wantError: true},
		{name: "nanosecond value rejected", columns: []driver.Value{int64(77), int64(2), at.Add(time.Nanosecond), int64(0), nil}, wantError: true},
		{name: "unparseable driver timestamp", columns: []driver.Value{int64(77), int64(2), []byte("unsupported timestamp"), int64(0), nil}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := sql.OpenDB(returnCandidateConnector{columns: tc.columns, err: tc.queryError})
			t.Cleanup(func() { _ = base.Close() })
			repo := NewUserRepository(sqlx.NewDb(base, "owned-return-candidate"))
			got, err := repo.ReadReturnQuestExpiries(context.Background(), 77)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error=%v", err)
			}
			if tc.wantError {
				if got.userID != 0 {
					t.Fatal("failed read published partial evidence")
				}
				return
			}
			if got.userID != 77 || got.returnExpiry.kind != tc.first || got.newcomerExpiry.kind != tc.second {
				t.Fatal("original projection tags changed")
			}
			if tc.first == returnQuestExpiryFinite && (!got.returnExpiry.at.Equal(at) || !got.newcomerExpiry.at.Equal(at.Add(time.Hour))) {
				t.Fatal("finite values changed")
			}
		})
	}
}

func TestReturnQuestCandidateDriverFiniteDateRange(t *testing.T) {
	for _, text := range []string{"0001-01-01 00:00:00+00 BC", "0001-01-01 00:00:00+00", "294276-12-31 23:59:59.999999+00"} {
		t.Run(strings.ReplaceAll(text, " ", "_"), func(t *testing.T) {
			parsed, err := pq.ParseTimestamp(nil, text)
			if err != nil {
				t.Fatal("current lib/pq rejected finite capture", err)
			}
			expiry := returnQuestExpiry{kind: returnQuestExpiryFinite, at: parsed}
			if !expiry.valid() {
				t.Fatal("finite timestamp lost microsecond precision")
			}
			if expiry.after(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)) != strings.HasPrefix(text, "294276-") {
				t.Fatal("finite date was collapsed into infinity or the present")
			}
		})
	}
}
