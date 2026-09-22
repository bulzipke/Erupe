package channelserver

import (
	"bytes"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// This small model is transcribed from HD FUN_103a8a80 cases 3/5, not from the
// server's error constant. "error-dialog" means FUN_103a97f0 cases 0xb/0xc:
// show the built-in error and wait for acknowledgement without another request.
// It checks native control flow, but does not replace an in-game smoke test.
func nativeDivaMapNextAction(ack ackResponse, generate, hasNodes, goalComplete bool) string {
	if !ack.IsBufferResponse || ack.ErrorCode != 0 {
		if generate {
			return "error-dialog"
		}
		return "generate"
	}
	if len(ack.Payload) == 0 {
		return "invalid-response"
	}
	switch ack.Payload[0] {
	case 0xff:
		return "refresh-event-info"
	case 0:
		if generate {
			return "reload-map"
		}
		if !hasNodes || goalComplete {
			return "generate"
		}
		return "show-map"
	case 0xf7:
		if !generate {
			return "generate"
		}
	case 0xf8:
		if generate {
			return "show-map"
		}
	}
	return "error-dialog"
}

func assertDivaMapTerminalFailure(t *testing.T, ack ackResponse, generate bool) {
	t.Helper()
	if !ack.IsBufferResponse || ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0x01}) {
		t.Fatalf("wrong native terminal refusal: %+v", ack)
	}
	if action := nativeDivaMapNextAction(ack, generate, false, false); action != "error-dialog" {
		t.Fatalf("unavailable map initiates native action %q instead of waiting for error acknowledgement", action)
	}
}

func TestDivaMapNativeFailureTransitions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		generate bool
		ack      ackResponse
		want     string
	}{
		{"get terminal", false, ackResponse{IsBufferResponse: true, Payload: []byte{1}}, "error-dialog"},
		{"generate terminal", true, ackResponse{IsBufferResponse: true, Payload: []byte{1}}, "error-dialog"},
		{"get old ff refreshes", false, ackResponse{IsBufferResponse: true, Payload: []byte{0xff}}, "refresh-event-info"},
		{"generate old ff refreshes", true, ackResponse{IsBufferResponse: true, Payload: []byte{0xff}}, "refresh-event-info"},
		{"get transport failure generates", false, ackResponse{IsBufferResponse: true, ErrorCode: 1}, "generate"},
		{"get simple ack generates", false, ackResponse{}, "generate"},
		{"get missing map generates", false, ackResponse{IsBufferResponse: true, Payload: []byte{0xf7}}, "generate"},
		{"get empty success generates", false, ackResponse{IsBufferResponse: true, Payload: make([]byte, 9)}, "generate"},
		{"generate success reloads", true, ackResponse{IsBufferResponse: true, Payload: []byte{0}}, "reload-map"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeDivaMapNextAction(tc.ack, tc.generate, false, false); got != tc.want {
				t.Fatalf("native action=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestDivaMapMissingCatalogTerminatesNativeRequestCycle(t *testing.T) {
	for _, tt := range []struct {
		name     string
		generate bool
		call     func(*Session)
	}{
		{"get", false, func(s *Session) { handleMsgMhfGetUdGuildMapInfo(s, &mhfpacket.MsgMhfGetUdGuildMapInfo{AckHandle: 55}) }},
		{"generate", true, func(s *Session) {
			handleMsgMhfGenerateUdGuildMap(s, &mhfpacket.MsgMhfGenerateUdGuildMap{AckHandle: 55})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := createMockSession(1, createMockServer())
			s.server.erupeConfig.RealClientMode = cfg.ZZ
			tt.call(s)
			ack := readAck(t, s)
			if ack.AckHandle != 55 {
				t.Fatalf("wrong acknowledgement handle: %+v", ack)
			}
			assertDivaMapTerminalFailure(t, ack, tt.generate)
		})
	}
}
