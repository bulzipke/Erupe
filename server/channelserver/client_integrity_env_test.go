package channelserver

import (
	"testing"
	"time"

	"erupe-ce/common/clientintegrity"
	"erupe-ce/common/savejournal"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestClientEnvironmentReportsReachTheSaveJournal(t *testing.T) {
	session, _, out, stored := journalTestSession(t, 1000)

	// A proof without a look says nothing; it must not count as a clean report.
	session.noteClientEnvironment(42, clientintegrity.Env{Looks: 0})
	// First report of the session: recorded although clean.
	session.noteClientEnvironment(42, clientintegrity.Env{Looks: 1})
	// The same state again is not repeated.
	session.noteClientEnvironment(42, clientintegrity.Env{Looks: 10, Span: 600 * time.Second})
	// A tool appears, stays, and a debugger joins it.
	session.noteClientEnvironment(42, clientintegrity.Env{Tools: 3, Looks: 10, Span: 600 * time.Second})
	session.noteClientEnvironment(42, clientintegrity.Env{Tools: 3, Looks: 10, Span: 600 * time.Second})
	session.noteClientEnvironment(42, clientintegrity.Env{Tools: 3, Debugger: true, Looks: 4, Span: 240 * time.Second})

	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 1, RawDataPayload: stored})
	records := out.records(t)
	if len(records) != 1 || records[0].Activity == nil {
		t.Fatalf("records = %+v", records)
	}
	events := records[0].Activity.Events
	want := []savejournal.Event{
		{Kind: "env", Tools: "-"},
		{Kind: "env", Tools: "ce:p,ce:w", Span: 600},
		{Kind: "env", Tools: "ce:p,ce:w", Debugger: true, Span: 240},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i, e := range events {
		if e.Kind != want[i].Kind || e.Tools != want[i].Tools || e.Debugger != want[i].Debugger || e.Span != want[i].Span {
			t.Fatalf("event %d = %+v, want %+v", i, e, want[i])
		}
	}

	// The tool closing is a change too, and lands in the next record.
	session.noteClientEnvironment(42, clientintegrity.Env{Looks: 10, Span: 600 * time.Second})
	handleMsgMhfSavedata(session, &mhfpacket.MsgMhfSavedata{AckHandle: 2, RawDataPayload: stored})
	records = out.records(t)
	if len(records) != 2 || len(records[1].Activity.Events) != 1 || records[1].Activity.Events[0].Tools != "-" ||
		records[1].Activity.Events[0].Span != 600 {
		t.Fatalf("second record = %+v", records[len(records)-1].Activity)
	}
}

func TestClientEnvironmentReportWithoutJournalStillTracksState(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	session := createMockSession(42, server)

	session.noteClientEnvironment(42, clientintegrity.Env{Tools: 1, Looks: 1})
	if got := session.clientEnv.Load(); got != clientEnvSeen|1 {
		t.Fatalf("state = %#x", got)
	}
	session.noteClientEnvironment(42, clientintegrity.Env{Debugger: true, Looks: 2})
	if got := session.clientEnv.Load(); got != clientEnvSeen|clientEnvDebugger {
		t.Fatalf("state = %#x", got)
	}
}
