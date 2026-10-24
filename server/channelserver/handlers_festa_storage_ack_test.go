package channelserver

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"erupe-ce/network"
	"erupe-ce/network/mhfpacket"
)

type festaStorageAckEntryCall struct {
	guildID uint32
	team    string
}

type festaStorageAckChargeCall struct {
	charID  uint32
	guildID uint32
	souls   []uint16
}

type festaStorageAckRepo struct {
	*mockFestaRepo
	err         error
	voteCalls   [][2]uint32
	entryCalls  []festaStorageAckEntryCall
	chargeCalls []festaStorageAckChargeCall
}

func (r *festaStorageAckRepo) VoteTrial(charID, trialID uint32) error {
	r.voteCalls = append(r.voteCalls, [2]uint32{charID, trialID})
	return r.err
}

func (r *festaStorageAckRepo) RegisterGuild(guildID uint32, team string) error {
	r.entryCalls = append(r.entryCalls, festaStorageAckEntryCall{guildID: guildID, team: team})
	return r.err
}

func (r *festaStorageAckRepo) SubmitSouls(charID, guildID uint32, souls []uint16) error {
	r.chargeCalls = append(r.chargeCalls, festaStorageAckChargeCall{
		charID: charID, guildID: guildID, souls: append([]uint16(nil), souls...),
	})
	return r.err
}

// These cases use only owned repository mocks and the actual FestaService.
// They do not create a database, start a server, or open a network connection.
func TestFestaStorageAckIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		err      error
		allZero  bool
		wantCode byte
	}{
		{name: "vote_success", mode: "vote"},
		{name: "vote_storage_failure", mode: "vote", err: errors.New("injected vote storage failure"), wantCode: 1},
		{name: "entry_success", mode: "entry"},
		{name: "entry_storage_failure", mode: "entry", err: errors.New("injected entry storage failure"), wantCode: 1},
		{name: "charge_success", mode: "charge"},
		{name: "charge_storage_failure", mode: "charge", err: errors.New("injected charge storage failure"), wantCode: 1},
		{name: "charge_all_zero_noop", mode: "charge", allZero: true, err: errors.New("all-zero service must not call storage")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const charID, guildID, trialID = uint32(321), uint32(77), uint32(19)
			const ackHandle = uint32(0x12345678)
			server := createMockServer()
			repo := &festaStorageAckRepo{mockFestaRepo: &mockFestaRepo{}, err: tc.err}
			guildRepo := &mockGuildRepo{membership: &GuildMember{CharID: charID, GuildID: guildID}}
			server.festaRepo = repo
			server.guildRepo = guildRepo
			server.festaService = NewFestaService(repo, server.logger)
			session := createMockSession(charID, server)
			souls := []uint16{10, 0, 20}
			if tc.allZero {
				souls = []uint16{0, 0, 0}
			}
			originalSouls := append([]uint16(nil), souls...)
			switch tc.mode {
			case "vote":
				handleMsgMhfVoteFesta(session, &mhfpacket.MsgMhfVoteFesta{AckHandle: ackHandle, TrialID: trialID})
			case "entry":
				handleMsgMhfEntryFesta(session, &mhfpacket.MsgMhfEntryFesta{AckHandle: ackHandle})
			case "charge":
				// A zero packet guild means the authenticated character's actual
				// guild; the storage call must receive guildID rather than zero.
				handleMsgMhfChargeFesta(session, &mhfpacket.MsgMhfChargeFesta{AckHandle: ackHandle, GuildID: 0, Souls: souls})
			}

			wantVote, wantEntry, wantCharge, wantMembership := 0, 0, 0, 0
			var team uint32
			switch tc.mode {
			case "vote":
				wantVote = 1
				if len(repo.voteCalls) != 1 || repo.voteCalls[0] != [2]uint32{charID, trialID} {
					t.Fatalf("vote calls=%v", repo.voteCalls)
				}
			case "entry":
				wantEntry, wantMembership = 1, 1
				if len(repo.entryCalls) != 1 || repo.entryCalls[0].guildID != guildID {
					t.Fatalf("entry calls=%v", repo.entryCalls)
				}
				switch repo.entryCalls[0].team {
				case "blue":
					team = 0
				case "red":
					team = 1
				default:
					t.Fatalf("invalid stored team=%q", repo.entryCalls[0].team)
				}
			case "charge":
				wantMembership = 1
				if !tc.allZero {
					wantCharge = 1
					if len(repo.chargeCalls) != 1 {
						t.Fatalf("charge calls=%v", repo.chargeCalls)
					}
					call := repo.chargeCalls[0]
					if call.charID != charID || call.guildID != guildID || !reflect.DeepEqual(call.souls, []uint16{10, 0, 20}) {
						t.Fatalf("charge arguments=%+v", call)
					}
				}
			}
			if len(repo.voteCalls) != wantVote || len(repo.entryCalls) != wantEntry || len(repo.chargeCalls) != wantCharge {
				t.Fatalf("unexpected storage calls: vote=%v entry=%v charge=%v", repo.voteCalls, repo.entryCalls, repo.chargeCalls)
			}
			if len(guildRepo.membershipQueries) != wantMembership || wantMembership == 1 && guildRepo.membershipQueries[0] != charID {
				t.Fatalf("membership queries=%v", guildRepo.membershipQueries)
			}
			if !reflect.DeepEqual(souls, originalSouls) {
				t.Fatalf("input souls changed: got=%v want=%v", souls, originalSouls)
			}

			opcode := uint16(network.MSG_SYS_ACK)
			want := []byte{byte(opcode >> 8), byte(opcode), 0x12, 0x34, 0x56, 0x78, 0, tc.wantCode, 0, 0, 0, 0, 0, 0}
			if tc.mode == "entry" && tc.err == nil {
				// The native simple ACK retains the original big-endian uint32
				// team value, matching the blue/red value actually stored.
				want[13] = byte(team)
			}
			select {
			case response := <-session.sendPackets:
				if !bytes.Equal(response.data, want) {
					t.Fatalf("ACK=%x want=%x", response.data, want)
				}
			default:
				t.Fatal("missing storage acknowledgement")
			}
			select {
			case response := <-session.sendPackets:
				t.Fatalf("unexpected second acknowledgement: %x", response.data)
			default:
			}
		})
	}
}
