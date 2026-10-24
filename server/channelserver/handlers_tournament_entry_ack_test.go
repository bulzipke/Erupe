package channelserver

import (
	"bytes"
	"errors"
	"testing"

	"erupe-ce/network"
	"erupe-ce/network/mhfpacket"
)

type tournamentEntryAckCandidateRepo struct {
	*mockTournamentRepo
	err   error
	calls [][5]uint32
}

func (r *tournamentEntryAckCandidateRepo) SubmitResult(a, b, c, d, e uint32) error {
	r.calls = append(r.calls, [5]uint32{a, b, c, d, e})
	return r.err
}

// This tests acknowledgement integrity for the existing entry notification.
// It does not reinterpret its fields as validated completion/time results.
func TestTournamentEntryStorageAckIntegrityCandidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code byte
	}{
		{name: "success"},
		{name: "storage_failure", err: errors.New("injected tournament entry storage failure"), code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			repo := &tournamentEntryAckCandidateRepo{mockTournamentRepo: &mockTournamentRepo{}, err: tc.err}
			srv.tournamentRepo = repo
			const characterID = uint32(321)
			s := createMockSession(characterID, srv)
			packet := &mhfpacket.MsgMhfEnterTournamentQuest{AckHandle: 0x12345678, TournamentID: 17, EntryHandle: 21, Unk2: 31, QuestSlot: 41, StageHandle: 51, Unk5: 61}
			handleMsgMhfEnterTournamentQuest(s, packet)
			if len(repo.calls) != 1 || repo.calls[0] != [5]uint32{characterID, 17, 31, 41, 51} {
				t.Fatalf("calls=%v", repo.calls)
			}
			opcode := uint16(network.MSG_SYS_ACK)
			expected := []byte{byte(opcode >> 8), byte(opcode), 0x12, 0x34, 0x56, 0x78, 0, tc.code, 0, 0, 0, 0, 0, 0}
			select {
			case response := <-s.sendPackets:
				if !bytes.Equal(response.data, expected) {
					t.Fatalf("ACK=%x want=%x", response.data, expected)
				}
			default:
				t.Fatal("missing ACK")
			}
			select {
			case response := <-s.sendPackets:
				t.Fatalf("unexpected second ACK: %x", response.data)
			default:
			}
		})
	}
}
