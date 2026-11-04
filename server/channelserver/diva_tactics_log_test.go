package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaTacticsLogWire(t *testing.T) {
	// Synthetic journal values verify the recovered wire, not retail rewards.
	row := divaTacticsLogEntry{Kind: 6, Branch: 2, CharID: 0x1020304, Name: "한글헌터", Value: 5000, At: time.Unix(1777777777, 0)}
	data, err := divaTacticsLogPayload([]divaTacticsLogEntry{row})
	if err != nil || len(data) != 47 || data[0] != 1 || data[1] != 6 || data[2] != 2 {
		t.Fatalf("header: %x %v", data, err)
	}
	if binary.BigEndian.Uint32(data[3:7]) != row.CharID || binary.BigEndian.Uint32(data[39:43]) != row.Value || binary.BigEndian.Uint32(data[43:47]) != uint32(row.At.Unix()) {
		t.Fatalf("native offsets: %x", data)
	}
	if !bytes.Equal(data[7:39], stringsupport.PaddedString(row.Name, 32, true)) || data[38] != 0 {
		t.Fatalf("CP949 name: %x", data[7:39])
	}
	if empty, err := divaTacticsLogPayload(nil); err != nil || !bytes.Equal(empty, []byte{0}) {
		t.Fatalf("empty cache clear: %x %v", empty, err)
	}
	rows := make([]divaTacticsLogEntry, 200)
	for i := range rows {
		rows[i] = row
	}
	if full, err := divaTacticsLogPayload(rows); err != nil || len(full) != 0x23f1 {
		t.Fatalf("maximum native buffer: %d %v", len(full), err)
	}
	if _, err := divaTacticsLogPayload(append(rows, row)); err == nil {
		t.Fatal("201 rows overflow native cache")
	}
}

func TestDivaTacticsLogPersonalMilestoneWire(t *testing.T) {
	// Native 103b9104..103b9146 passes the character name and value to the
	// kind-2 format, exactly as kind 0; it does not consume a branch number.
	row := divaTacticsLogEntry{Kind: 2, CharID: 42, Name: "한글헌터", Value: divaTacticsLogPersonalStep, At: time.Unix(1777777777, 0)}
	data, err := divaTacticsLogPayload([]divaTacticsLogEntry{row})
	if err != nil || len(data) != 47 || data[1] != 2 || data[2] != 0 ||
		binary.BigEndian.Uint32(data[3:7]) != 42 || binary.BigEndian.Uint32(data[39:43]) != divaTacticsLogPersonalStep {
		t.Fatalf("personal milestone native record: %x %v", data, err)
	}
	for name, change := range map[string]func(*divaTacticsLogEntry){
		"missing hunter": func(r *divaTacticsLogEntry) { r.CharID = 0 },
		"missing name":   func(r *divaTacticsLogEntry) { r.Name = "" },
		"unsafe name":    func(r *divaTacticsLogEntry) { r.Name = "%n" },
		"branch":         func(r *divaTacticsLogEntry) { r.Branch = 1 },
		"zero value":     func(r *divaTacticsLogEntry) { r.Value = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			unsafe := row
			change(&unsafe)
			if _, err := divaTacticsLogPayload([]divaTacticsLogEntry{unsafe}); err == nil {
				t.Fatal("unsafe personal milestone accepted")
			}
		})
	}
}

func TestDivaTacticsLogRejectsUnsafeRows(t *testing.T) {
	base := divaTacticsLogEntry{CharID: 1, Name: "헌터", Value: 1, At: time.Unix(1777777777, 0)}
	for name, change := range map[string]func(*divaTacticsLogEntry){
		"unknown kind":                func(r *divaTacticsLogEntry) { r.Kind = 9 },
		"branch without branch event": func(r *divaTacticsLogEntry) { r.Branch = 1 },
		"missing branch":              func(r *divaTacticsLogEntry) { r.Kind = 6 },
		"no hunter":                   func(r *divaTacticsLogEntry) { r.CharID = 0 },
		"empty name":                  func(r *divaTacticsLogEntry) { r.Name = "" },
		"printf":                      func(r *divaTacticsLogEntry) { r.Name = "%n" },
		"control":                     func(r *divaTacticsLogEntry) { r.Name = "헌\n터" },
		"name overflow":               func(r *divaTacticsLogEntry) { r.Name = strings.Repeat("가", 16) },
		"zero points":                 func(r *divaTacticsLogEntry) { r.Value = 0 },
		"signed overflow":             func(r *divaTacticsLogEntry) { r.Value = math.MaxInt32 + 1 },
		"timestamp overflow":          func(r *divaTacticsLogEntry) { r.At = time.Unix(math.MaxUint32+1, 0) },
		"empty timestamp":             func(r *divaTacticsLogEntry) { r.At = time.Unix(0, 0) },
		"system with hunter":          func(r *divaTacticsLogEntry) { r.Kind = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			row := base
			change(&row)
			if _, err := divaTacticsLogPayload([]divaTacticsLogEntry{row}); err == nil {
				t.Fatal("unsafe native row accepted")
			}
		})
	}
}

func TestDivaTacticsLogNameAndDates(t *testing.T) {
	if got := divaTacticsLogName("%~{한\n글}\x00😀"); got != "한글" {
		t.Fatal(got)
	}
	if got := divaTacticsLogName("%😀"); got != "헌터" {
		t.Fatal(got)
	}
	if got := divaTacticsLogName(strings.Repeat("한", 20)); got != strings.Repeat("한", 15) {
		t.Fatal(got)
	}
	latest := time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)
	rows := []divaTacticsLogEntry{{Kind: 1, Value: 2, At: latest}, {Kind: 1, Value: 1, At: latest.Add(-time.Second)}}
	withDates := divaTacticsLogWithDates(rows)
	if len(withDates) != 4 || withDates[0].Kind != 5 || withDates[2].Kind != 5 ||
		!withDates[1].At.Equal(latest.Add(-time.Second)) || !withDates[3].At.Equal(latest) {
		t.Fatal(withDates)
	}
	rows = make([]divaTacticsLogEntry, 300)
	for i := range rows {
		rows[i] = divaTacticsLogEntry{Kind: 1, Value: 1, At: latest}
	}
	if got := divaTacticsLogWithDates(rows); len(got) != 200 || got[199].Kind == 5 {
		t.Fatal("cap excludes date headers")
	}
	// 198 entries + one header leaves one slot, insufficient for another day.
	rows = rows[:199]
	rows[198].At = latest.Add(-24 * time.Hour)
	if got := divaTacticsLogWithDates(rows); len(got) != 199 || got[198].Kind == 5 || !got[198].At.Equal(latest) {
		t.Fatal("orphan date header")
	}
}

func TestDivaTacticsLogChronologicalWirePreservesDaysAndNewestWindow(t *testing.T) {
	latest := time.Date(2026, 11, 4, 12, 39, 0, 0, time.UTC)
	previousDay := latest.Add(-24 * time.Hour)
	rows := []divaTacticsLogEntry{
		{Kind: 2, CharID: 1, Name: "Hunter", Value: 30000, At: latest},
		{Kind: 0, CharID: 1, Name: "Hunter", Value: 3384, At: latest},
		{Kind: 3, At: latest.Add(-39 * time.Minute)},
		{Kind: 1, Value: 24, At: previousDay},
	}
	ordered := divaTacticsLogWithDates(rows)
	wantKinds := []uint8{5, 1, 5, 3, 0, 2}
	if len(ordered) != len(wantKinds) || rows[0].Kind != 2 {
		t.Fatal("presentation changed the source journal or row count")
	}
	data, err := divaTacticsLogPayload(ordered)
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range wantKinds {
		if ordered[i].Kind != kind || data[1+46*i] != kind {
			t.Fatalf("wire row %d kind=%d want=%d", i, ordered[i].Kind, kind)
		}
		if i > 0 && ordered[i].At.Before(ordered[i-1].At) {
			t.Fatalf("date header or event out of chronological order: %+v", ordered)
		}
	}
	rows = make([]divaTacticsLogEntry, 300)
	for i := range rows {
		rows[i] = divaTacticsLogEntry{Kind: 1, Value: uint32(300 - i), At: latest.Add(-time.Duration(i) * time.Second)}
	}
	ordered = divaTacticsLogWithDates(rows)
	if len(ordered) != 200 || ordered[0].Kind != 5 || ordered[1].Value != 102 || ordered[199].Value != 300 ||
		!ordered[199].At.Equal(latest) {
		t.Fatalf("reversal retained older rows or dropped the newest event: %+v", ordered)
	}
}

type divaTacticsLogFake struct {
	*divaInterceptionHandlerFakeRepo
	rows  []divaTacticsLogEntry
	err   error
	calls int
	char  uint32
}

func (r *divaTacticsLogFake) GetDivaTacticsLog(char uint32, now time.Time) ([]divaTacticsLogEntry, error) {
	r.calls++
	r.char = char
	if !now.IsZero() {
		return nil, errors.New("must sample database clock after locks")
	}
	return r.rows, r.err
}

func TestDivaTacticsLogHandlerAlwaysClearsOrPopulates(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "error", "unsafe", "disabled", "old client", "clock override", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			s, base, _ := newDivaInterceptionHandlerTestSession()
			r := &divaTacticsLogFake{divaInterceptionHandlerFakeRepo: base, rows: []divaTacticsLogEntry{{Kind: 1, Value: 1, At: time.Unix(1777777777, 0)}}}
			s.server.divaRepo = r
			switch mode {
			case "empty":
				r.rows = nil
			case "error":
				r.err = errors.New("offline")
			case "unsafe":
				r.rows[0].Kind = 255
			case "disabled":
				s.server.erupeConfig.DebugOptions.DivaOverride = 0
			case "old client":
				s.server.erupeConfig.RealClientMode = cfg.G10
			case "clock override":
				hour := 12
				s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
			case "anonymous":
				s.charID = 0
			}
			handleMsgMhfGetUdTacticsLog(s, &mhfpacket.MsgMhfGetUdTacticsLog{AckHandle: 77})
			ack := readAck(t, s)
			if ack.AckHandle != 77 || ack.ErrorCode != 0 || !ack.IsBufferResponse {
				t.Fatal(ack)
			}
			if mode == "valid" {
				if len(ack.Payload) != 47 || r.calls != 1 || r.char != s.charID {
					t.Fatal("unscoped or missing log", r.calls, ack)
				}
			} else if !bytes.Equal(ack.Payload, []byte{0}) {
				t.Fatalf("old cache retained: %x", ack.Payload)
			}
			if (mode == "disabled" || mode == "old client" || mode == "clock override" || mode == "anonymous") && r.calls != 0 {
				t.Fatal("disabled route queried ledger")
			}
		})
	}
}
