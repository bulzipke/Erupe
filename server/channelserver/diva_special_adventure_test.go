package channelserver

import (
	"errors"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type specialAdventureMockRepo struct {
	mockDivaRepo
	err                       error
	calls                     int
	char, destination, charge uint32
	mode                      int
}

func (r *specialAdventureMockRepo) RegisterDivaSpecialAdventure(char, destination, charge uint32, mode int) error {
	r.calls++
	r.char, r.destination, r.charge, r.mode = char, destination, charge, mode
	return r.err
}

func TestDivaSpecialAdventureHandlerACK(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		mode                              int
		client                            cfg.Mode
		shifted, missingRepo, missingChar bool
		err                               error
		wantCall, wantSuccess             bool
	}{
		{name: "automatic", mode: -1, client: cfg.ZZ, wantCall: true, wantSuccess: true},
		{name: "welcome", mode: 3, client: cfg.ZZ, wantCall: true, wantSuccess: true},
		{name: "prayer", mode: 1, client: cfg.ZZ},
		{name: "battle", mode: 2, client: cfg.ZZ},
		{name: "disabled", mode: 0, client: cfg.ZZ},
		{name: "unsupported mode", mode: 4, client: cfg.ZZ},
		{name: "old client", mode: 3, client: cfg.Z1},
		{name: "shifted clock", mode: 3, client: cfg.ZZ, shifted: true},
		{name: "missing repository", mode: 3, client: cfg.ZZ, missingRepo: true},
		{name: "missing character", mode: 3, client: cfg.ZZ, missingChar: true},
		{name: "authorization denied", mode: 3, client: cfg.ZZ, err: errDivaSpecialAdventureUnavailable, wantCall: true},
		{name: "database error", mode: 3, client: cfg.ZZ, err: errors.New("db unavailable"), wantCall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = tc.client
			srv.erupeConfig.DebugOptions.DivaOverride = tc.mode
			if tc.shifted {
				hour := 12
				srv.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
			}
			repo := &specialAdventureMockRepo{err: tc.err}
			srv.divaRepo = repo
			if tc.missingRepo {
				srv.divaRepo = &mockDivaRepo{}
			}
			// No preflight guild repository is needed: authorization and insert
			// must use the same current membership inside the Diva transaction.
			srv.guildRepo = nil
			s := createMockSession(17, srv)
			if tc.missingChar {
				s.charID = 0
			}
			handleMsgMhfRegistGuildAdventureDiva(s, &mhfpacket.MsgMhfRegistGuildAdventureDiva{AckHandle: 73, Destination: 3, Charge: 200})
			ack := readAck(t, s)
			if (ack.ErrorCode == 0) != tc.wantSuccess || ack.IsBufferResponse || len(ack.Payload) != 4 || ack.AckHandle != 73 {
				t.Fatal(ack)
			}
			if (repo.calls == 1) != tc.wantCall {
				t.Fatalf("calls=%d wantCall=%v", repo.calls, tc.wantCall)
			}
			if tc.wantCall && (repo.char != 17 || repo.destination != 3 || repo.charge != 200 || repo.mode != tc.mode) {
				t.Fatal(repo)
			}
			select {
			case <-s.sendPackets:
				t.Fatal("multiple ACKs")
			default:
			}
		})
	}
}
