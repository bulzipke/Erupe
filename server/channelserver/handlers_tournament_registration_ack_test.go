package channelserver

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
	"erupe-ce/network/mhfpacket"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

// The owned connector supplies in-memory rows to the real repository. It never
// opens a network connection, runs PostgreSQL, or uses a shared database helper.
type tournamentRegistrationAckState struct {
	activeValues  []driver.Value
	activeErr     error
	registerErr   error
	entryErr      error
	activeReads   int
	registerCalls int
	entryReads    int
	unexpected    []string
}

type tournamentRegistrationAckConnector struct {
	state *tournamentRegistrationAckState
}

func (c tournamentRegistrationAckConnector) Connect(context.Context) (driver.Conn, error) {
	return tournamentRegistrationAckConnection{state: c.state}, nil
}

func (tournamentRegistrationAckConnector) Driver() driver.Driver {
	return tournamentRegistrationAckDriver{}
}

type tournamentRegistrationAckDriver struct{}

func (tournamentRegistrationAckDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("tournament ACK test forbids external connections")
}

type tournamentRegistrationAckConnection struct {
	state *tournamentRegistrationAckState
}

func (c tournamentRegistrationAckConnection) reject(operation string) error {
	c.state.unexpected = append(c.state.unexpected, operation)
	return errors.New("unexpected tournament ACK test operation: " + operation)
}

func (c tournamentRegistrationAckConnection) Prepare(string) (driver.Stmt, error) {
	return nil, c.reject("prepare")
}

func (tournamentRegistrationAckConnection) Close() error { return nil }

func (c tournamentRegistrationAckConnection) Begin() (driver.Tx, error) {
	return nil, c.reject("transaction")
}

func (c tournamentRegistrationAckConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	query = strings.Join(strings.Fields(query), " ")
	if strings.HasPrefix(query, "SELECT id, name, start_time, entry_end, ranking_end, reward_end FROM tournaments ") {
		if len(args) != 1 {
			return nil, c.reject("active query arguments")
		}
		if now, ok := args[0].Value.(int64); !ok || now <= 0 {
			return nil, c.reject("active query timestamp")
		}
		c.state.activeReads++
		if c.state.activeErr != nil {
			return nil, c.state.activeErr
		}
		return &tournamentRegistrationAckRows{
			columns: []string{"id", "name", "start_time", "entry_end", "ranking_end", "reward_end"},
			values:  c.state.activeValues,
		}, nil
	}
	if query == "SELECT id FROM tournament_entries WHERE char_id = $1 AND tournament_id = $2" {
		if !tournamentRegistrationAckArguments(args) {
			return nil, c.reject("entry query arguments")
		}
		c.state.entryReads++
		if c.state.entryErr != nil {
			return nil, c.state.entryErr
		}
		return &tournamentRegistrationAckRows{columns: []string{"id"}, values: []driver.Value{int64(0x10203040)}}, nil
	}
	return nil, c.reject("query: " + query)
}

func (c tournamentRegistrationAckConnection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query != "INSERT INTO tournament_entries (char_id, tournament_id) VALUES ($1, $2) ON CONFLICT (char_id, tournament_id) DO NOTHING" || !tournamentRegistrationAckArguments(args) {
		return nil, c.reject("registration write")
	}
	c.state.registerCalls++
	if c.state.registerErr != nil {
		return nil, c.state.registerErr
	}
	return driver.RowsAffected(1), nil
}

func tournamentRegistrationAckArguments(args []driver.NamedValue) bool {
	return len(args) == 2 && args[0].Value == int64(321) && args[1].Value == int64(17)
}

type tournamentRegistrationAckRows struct {
	columns []string
	values  []driver.Value
	done    bool
}

func (r *tournamentRegistrationAckRows) Columns() []string { return r.columns }
func (*tournamentRegistrationAckRows) Close() error        { return nil }
func (r *tournamentRegistrationAckRows) Next(destination []driver.Value) error {
	if r.done || r.values == nil {
		return io.EOF
	}
	r.done = true
	copy(destination, r.values)
	return nil
}

func TestTournamentRegistrationAckStorageIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		noActive      bool
		invalidActive bool
		activeErr     error
		registerErr   error
		entryErr      error
		wantCode      byte
		wantBuffer    bool
		wantWrites    int
		wantEntryRead int
	}{
		{name: "active_lookup_failure", activeErr: errors.New("injected active lookup failure"), wantCode: 1},
		{name: "registration_write_failure", registerErr: errors.New("injected registration write failure"), wantCode: 1, wantWrites: 1},
		{name: "registration_ID_lookup_failure", entryErr: errors.New("injected registration ID lookup failure"), wantCode: 1, wantWrites: 1, wantEntryRead: 1},
		{name: "no_active_tournament", noActive: true},
		{name: "invalid_active_tournament", invalidActive: true},
		{name: "successful_registration", wantBuffer: true, wantWrites: 1, wantEntryRead: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &tournamentRegistrationAckState{
				activeValues: []driver.Value{int64(17), "test tournament", int64(1), int64(2), int64(3), int64(4)},
				activeErr:    tc.activeErr,
				registerErr:  tc.registerErr,
				entryErr:     tc.entryErr,
			}
			if tc.noActive {
				state.activeValues = nil
			}
			if tc.invalidActive {
				state.activeValues[3] = int64(0)
			}
			base := sql.OpenDB(tournamentRegistrationAckConnector{state: state})
			t.Cleanup(func() { _ = base.Close() })
			server := &Server{
				erupeConfig:    &cfg.Config{},
				tournamentRepo: NewTournamentRepository(sqlx.NewDb(base, "owned-tournament-registration-ack")),
			}
			session := &Session{
				charID:        321,
				clientContext: &clientctx.ClientContext{},
				sendPackets:   make(chan packet, 2),
				server:        server,
				logger:        zap.NewNop(),
			}
			handleMsgMhfEntryTournament(session, &mhfpacket.MsgMhfEntryTournament{AckHandle: 0x12345678})
			if state.activeReads != 1 || state.registerCalls != tc.wantWrites || state.entryReads != tc.wantEntryRead || len(state.unexpected) != 0 {
				t.Fatalf("repository calls: active=%d register=%d entry=%d unexpected=%v", state.activeReads, state.registerCalls, state.entryReads, state.unexpected)
			}
			opcode := uint16(network.MSG_SYS_ACK)
			expected := []byte{byte(opcode >> 8), byte(opcode), 0x12, 0x34, 0x56, 0x78, 0, tc.wantCode, 0, 0, 0, 0, 0, 0}
			if tc.wantBuffer {
				expected[6], expected[9] = 1, 4
				copy(expected[10:], []byte{0x10, 0x20, 0x30, 0x40})
			}
			select {
			case response := <-session.sendPackets:
				if !bytes.Equal(response.data, expected) {
					t.Fatalf("ACK=%x want=%x", response.data, expected)
				}
			default:
				t.Fatal("missing ACK")
			}
			select {
			case response := <-session.sendPackets:
				t.Fatalf("unexpected second ACK: %x", response.data)
			default:
			}
		})
	}
}
