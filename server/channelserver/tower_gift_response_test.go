package channelserver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"erupe-ce/network/mhfpacket"
)

type giftResponseRepo struct {
	mockTowerRepo
	err              error
	calls            int
	sender, receiver uint32
	gem, message     uint16
}

func (r *giftResponseRepo) TransferGem(sender, receiver uint32, gem, message uint16) error {
	r.calls++
	r.sender, r.receiver, r.gem, r.message = sender, receiver, gem, message
	return r.err
}

func TestTowerGiftNativeResultReply(t *testing.T) {
	for _, tt := range []struct {
		name      string
		err       error
		transport uint8
		result    uint32
	}{
		{"sent", nil, 0, 0},
		{"recipient owns", errTowerGemRecipientOwned, 0, 2},
		{"wrapped recipient owns", fmt.Errorf("gift: %w", errTowerGemRecipientOwned), 0, 2},
		{"ordinary rejection", errors.New("sender needs a duplicate"), 1, 0},
		{"database failure", errors.New("database offline"), 1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := createMockServer()
			repo := &giftResponseRepo{err: tt.err}
			server.towerRepo = repo
			session := createMockSession(12, server)
			handleMsgMhfPostGemInfo(session, &mhfpacket.MsgMhfPostGemInfo{
				AckHandle: 731, Op: 2, CID: 55, Gem: 0x0102, Quantity: 1, Message: 5,
			})
			ack := readAck(t, session)
			if ack.AckHandle != 731 || ack.IsBufferResponse || ack.ErrorCode != tt.transport || len(ack.Payload) != 4 || binary.BigEndian.Uint32(ack.Payload) != tt.result {
				t.Fatalf("native gift reply: %+v", ack)
			}
			if repo.calls != 1 || repo.sender != 12 || repo.receiver != 55 || repo.gem != 0x0102 || repo.message != 5 {
				t.Fatalf("transfer called incorrectly: %+v", repo)
			}
		})
	}
}

func TestTowerGiftInvalidWireStillFails(t *testing.T) {
	for _, tt := range []struct {
		name          string
		cid, quantity int32
	}{
		{"invalid recipient", 0, 1}, {"invalid quantity", 55, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := createMockServer()
			repo := &giftResponseRepo{}
			server.towerRepo = repo
			session := createMockSession(12, server)
			handleMsgMhfPostGemInfo(session, &mhfpacket.MsgMhfPostGemInfo{AckHandle: 732, Op: 2, CID: tt.cid, Gem: 0x0102, Quantity: tt.quantity})
			ack := readAck(t, session)
			if ack.ErrorCode != 1 || repo.calls != 0 {
				t.Fatalf("invalid gift accepted: %+v calls=%d", ack, repo.calls)
			}
		})
	}
}
