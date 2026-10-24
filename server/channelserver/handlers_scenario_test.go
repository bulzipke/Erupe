package channelserver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// --- mockScenarioRepo ---

type mockScenarioRepo struct {
	scenarios []Scenario
	err       error
}

func (m *mockScenarioRepo) GetCounters() ([]Scenario, error) {
	return m.scenarios, m.err
}

func TestHandleMsgMhfInfoScenarioCounter_Empty(t *testing.T) {
	server := createMockServer()
	server.scenarioRepo = &mockScenarioRepo{}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100}
	handleMsgMhfInfoScenarioCounter(session, pkt)

	select {
	case p := <-session.sendPackets:
		_, errCode, ackData := parseAckBufData(t, p.data)
		if errCode != 0 {
			t.Errorf("ErrorCode = %d, want 0", errCode)
		}
		if len(ackData) < 1 {
			t.Fatal("AckData too short")
		}
		if ackData[0] != 0 {
			t.Errorf("scenario count = %d, want 0", ackData[0])
		}
	default:
		t.Fatal("No response queued")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_WithScenarios(t *testing.T) {
	server := createMockServer()
	server.scenarioRepo = &mockScenarioRepo{
		scenarios: []Scenario{
			{MainID: 1000, CategoryID: 0},
			{MainID: 2000, CategoryID: 3},
			{MainID: 3000, CategoryID: 6},
			{MainID: 4000, CategoryID: 7},
			{MainID: 5000, CategoryID: 1},
		},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100}
	handleMsgMhfInfoScenarioCounter(session, pkt)

	select {
	case p := <-session.sendPackets:
		_, _, data := parseAckBufData(t, p.data)
		if len(data) < 1 {
			t.Fatal("AckData too short")
		}
		count := data[0]
		if count != 5 {
			t.Errorf("scenario count = %d, want 5", count)
		}

		// Each scenario: mainID(4) + exchange(1) + categoryID(1) = 6 bytes
		expectedLen := 1 + 5*6
		if len(data) != expectedLen {
			t.Errorf("AckData len = %d, want %d", len(data), expectedLen)
		}

		// Verify first scenario (categoryID=0, exchange=false)
		mainID := binary.BigEndian.Uint32(data[1:5])
		if mainID != 1000 {
			t.Errorf("first mainID = %d, want 1000", mainID)
		}
		if data[5] != 0 {
			t.Errorf("categoryID=0 should have exchange=false, got %d", data[5])
		}

		// Verify second scenario (categoryID=3, exchange=true)
		if data[5+6] != 1 {
			t.Errorf("categoryID=3 should have exchange=true, got %d", data[5+6])
		}
	default:
		t.Fatal("No response queued")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_TrimTo128(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.Z2
	server.erupeConfig.QuestDelivery.Enabled = true
	scenarios := make([]Scenario, 200)
	for i := range scenarios {
		scenarios[i] = Scenario{MainID: uint32(i), CategoryID: 0}
	}
	server.scenarioRepo = &mockScenarioRepo{scenarios: scenarios}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100}
	handleMsgMhfInfoScenarioCounter(session, pkt)

	select {
	case p := <-session.sendPackets:
		_, _, data := parseAckBufData(t, p.data)
		if data[0] != 128 {
			t.Errorf("scenario count = %d, want 128 (trimmed)", data[0])
		}
	default:
		t.Fatal("No response queued")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_ZZCapacity(t *testing.T) {
	for _, count := range []int{0, 128, 129, 171, 172, 255, 256, 300} {
		t.Run(fmt.Sprintf("rows_%d", count), func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.erupeConfig.QuestDelivery.Enabled = true
			rows := make([]Scenario, count)
			for i := range rows {
				rows[i] = Scenario{MainID: uint32(i + 1000), CategoryID: 1}
			}
			server.scenarioRepo = &mockScenarioRepo{scenarios: rows}
			session := createMockSession(1, server)
			handleMsgMhfInfoScenarioCounter(session, &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100})
			select {
			case packet := <-session.sendPackets:
				_, status, data := parseAckBufData(t, packet.data)
				if len(data) == 0 {
					t.Fatal("scenario ACK has no count")
				}
				want := count
				if want > 255 {
					if status == 0 || len(data) != 1 || data[0] != 0 {
						t.Fatalf("oversized roster did not fail as a whole: status=%d bytes=%x", status, data)
					}
					return
				}
				if status != 0 || len(data) != 1+want*6 || int(data[0]) != want {
					t.Fatalf("status=%d bytes=%d count=%d; want status0 bytes%d count%d", status, len(data), data[0], 1+want*6, want)
				}
				// The plain scenario count 0xff has no campaign-style escape.
				for i := 0; i < want; i++ {
					offset := 1 + i*6
					if got := binary.BigEndian.Uint32(data[offset : offset+4]); got != uint32(i+1000) {
						t.Fatalf("record%d mainID=%d, want%d", i, got, i+1000)
					}
					if data[offset+4] != 0 || data[offset+5] != 1 {
						t.Fatalf("record%d changed its exchange/category flags", i)
					}
				}
			default:
				t.Fatal("no ACK queued")
			}
		})
	}
}

func TestHandleMsgMhfInfoScenarioCounter_ZZMixedBoundaryOrder(t *testing.T) {
	for _, count := range []int{255, 300} {
		t.Run(fmt.Sprintf("rows_%d", count), func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.erupeConfig.QuestDelivery.Enabled = true
			rows := make([]Scenario, count)
			for i := range rows {
				rows[i] = Scenario{MainID: uint32(1000 - i), CategoryID: uint8(i % 8)}
			}
			before := append([]Scenario(nil), rows...)
			server.scenarioRepo = &mockScenarioRepo{scenarios: rows}
			session := createMockSession(1, server)
			handleMsgMhfInfoScenarioCounter(session, &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100})
			select {
			case packet := <-session.sendPackets:
				_, status, data := parseAckBufData(t, packet.data)
				if count > 255 {
					if status == 0 || len(data) != 1 || data[0] != 0 {
						t.Fatal("oversized mixed roster did not fail as a whole")
					}
				} else {
					if status != 0 || len(data) != 1+6*count || int(data[0]) != count {
						t.Fatal("invalid boundary roster")
					}
					seen := make(map[Scenario]bool)
					for i := 0; i < count; i++ {
						offset := 1 + i*6
						row := Scenario{MainID: binary.BigEndian.Uint32(data[offset : offset+4]), CategoryID: data[offset+5]}
						if i > 0 {
							previous := Scenario{MainID: binary.BigEndian.Uint32(data[offset-6 : offset-2]), CategoryID: data[offset-1]}
							if row.CategoryID < previous.CategoryID || row.CategoryID == previous.CategoryID && row.MainID <= previous.MainID {
								t.Fatalf("record%d is not in stable category/MainID order", i)
							}
						}
						if seen[row] {
							t.Fatal("duplicate serialized record")
						}
						seen[row] = true
					}
					for _, row := range before {
						if !seen[row] {
							t.Fatal("boundary roster omitted a source record")
						}
					}
				}
			default:
				t.Fatal("no ACK queued")
			}
			if !reflect.DeepEqual(rows, before) {
				t.Fatal("handler changed the source snapshot")
			}
		})
	}
}

func TestHandleMsgMhfInfoScenarioCounter_ZZStableDetachedOrder(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.QuestDelivery.Enabled = true
	rows := []Scenario{{MainID: 50, CategoryID: 7}, {MainID: 93, CategoryID: 1}, {MainID: 37, CategoryID: 1}, {MainID: 0, CategoryID: 0}}
	before := append([]Scenario(nil), rows...)
	server.scenarioRepo = &mockScenarioRepo{scenarios: rows}
	session := createMockSession(1, server)
	for pass := 0; pass < 2; pass++ {
		handleMsgMhfInfoScenarioCounter(session, &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100})
		select {
		case packet := <-session.sendPackets:
			_, status, data := parseAckBufData(t, packet.data)
			want := []Scenario{{MainID: 0, CategoryID: 0}, {MainID: 37, CategoryID: 1}, {MainID: 93, CategoryID: 1}, {MainID: 50, CategoryID: 7}}
			if status != 0 || len(data) != 1+6*len(want) || int(data[0]) != len(want) {
				t.Fatal("invalid scenario response")
			}
			for i, row := range want {
				offset := 1 + i*6
				if binary.BigEndian.Uint32(data[offset:offset+4]) != row.MainID || data[offset+5] != row.CategoryID {
					t.Fatalf("pass%d record%d order differs", pass, i)
				}
			}
		default:
			t.Fatal("no ACK queued")
		}
	}
	if !reflect.DeepEqual(rows, before) {
		t.Fatal("handler changed repository snapshot")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_ZZDisabledKeepsLegacyRoster(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	rows := make([]Scenario, 172)
	for i := range rows {
		rows[i] = Scenario{MainID: uint32(1000 - i), CategoryID: 1}
	}
	server.scenarioRepo = &mockScenarioRepo{scenarios: rows}
	session := createMockSession(1, server)
	handleMsgMhfInfoScenarioCounter(session, &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100})
	select {
	case packet := <-session.sendPackets:
		_, status, data := parseAckBufData(t, packet.data)
		if status != 0 || len(data) != 1+128*6 || data[0] != 128 || binary.BigEndian.Uint32(data[1:5]) != 1000 {
			t.Fatal("disabled rollout changed the legacy limit or order")
		}
	default:
		t.Fatal("no ACK queued")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_ZZRetrospectiveYearOrder(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.QuestDelivery.Enabled = true
	rows := []Scenario{
		{MainID: 81, CategoryID: 3},
		{MainID: 93, CategoryID: 1}, // Finale.
		{MainID: 81, CategoryID: 1}, // Year 11, not year 1.
		{MainID: 94, CategoryID: 1},
		{MainID: 80, CategoryID: 1},
		{MainID: 17, CategoryID: 0},
		{MainID: 92, CategoryID: 1}, // Year 12.
	}
	for mainID := uint32(91); mainID >= 82; mainID-- {
		rows = append(rows, Scenario{MainID: mainID, CategoryID: 1})
	}
	before := append([]Scenario(nil), rows...)
	server.scenarioRepo = &mockScenarioRepo{scenarios: rows}
	session := createMockSession(1, server)

	handleMsgMhfInfoScenarioCounter(session, &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100})
	select {
	case packet := <-session.sendPackets:
		_, status, data := parseAckBufData(t, packet.data)
		want := []Scenario{{MainID: 17, CategoryID: 0}, {MainID: 80, CategoryID: 1}}
		for mainID := uint32(82); mainID <= 91; mainID++ {
			want = append(want, Scenario{MainID: mainID, CategoryID: 1})
		}
		want = append(want,
			Scenario{MainID: 81, CategoryID: 1},
			Scenario{MainID: 92, CategoryID: 1},
			Scenario{MainID: 93, CategoryID: 1},
			Scenario{MainID: 94, CategoryID: 1},
			Scenario{MainID: 81, CategoryID: 3},
		)
		if status != 0 || len(data) != 1+6*len(want) || int(data[0]) != len(want) {
			t.Fatal("invalid scenario response")
		}
		for i, row := range want {
			offset := 1 + i*6
			got := Scenario{MainID: binary.BigEndian.Uint32(data[offset : offset+4]), CategoryID: data[offset+5]}
			wantExchange := byte(0)
			if row.CategoryID == 3 {
				wantExchange = 1
			}
			if got != row || data[offset+4] != wantExchange {
				t.Fatalf("record%d = %+v exchange%d, want %+v exchange%d", i, got, data[offset+4], row, wantExchange)
			}
		}
	default:
		t.Fatal("no ACK queued")
	}
	if !reflect.DeepEqual(rows, before) {
		t.Fatal("handler changed the source snapshot")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_DBError(t *testing.T) {
	server := createMockServer()
	server.scenarioRepo = &mockScenarioRepo{err: errors.New("db error")}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100}
	handleMsgMhfInfoScenarioCounter(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Fatal("Should still respond on error")
		}
	default:
		t.Fatal("No response queued")
	}
}

func TestHandleMsgMhfInfoScenarioCounter_CategoryExchangeFlags(t *testing.T) {
	tests := []struct {
		name       string
		categoryID uint8
		wantExch   bool
	}{
		{"Basic", 0, false},
		{"Veteran", 1, false},
		{"Other (exchange)", 3, true},
		{"Pallone (exchange)", 6, true},
		{"Diva (exchange)", 7, true},
		{"Unknown category 2", 2, false},
		{"Unknown category 4", 4, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := createMockServer()
			server.scenarioRepo = &mockScenarioRepo{
				scenarios: []Scenario{{MainID: 1, CategoryID: tt.categoryID}},
			}
			session := createMockSession(1, server)

			pkt := &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 100}
			handleMsgMhfInfoScenarioCounter(session, pkt)

			select {
			case p := <-session.sendPackets:
				_, _, data := parseAckBufData(t, p.data)
				isExchange := data[5] != 0
				if isExchange != tt.wantExch {
					t.Errorf("exchange = %v, want %v for categoryID=%d", isExchange, tt.wantExch, tt.categoryID)
				}
			default:
				t.Fatal("No response queued")
			}
		})
	}
}
