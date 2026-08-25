package channelserver

import (
	"testing"

	"erupe-ce/common/mhfitem"
	"erupe-ce/network/mhfpacket"
)

func TestUpdateGuildItemRejectsAnotherGuild(t *testing.T) {
	guildRepo := &mockGuildRepo{membership: &GuildMember{GuildID: 10, CharID: 1}}
	server := createMockServer()
	server.guildRepo = guildRepo
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfUpdateGuildItem{
		AckHandle: 3,
		GuildID:   11,
		UpdatedItems: []mhfitem.MHFItemStack{
			{Item: mhfitem.MHFItem{ItemID: 100}, Quantity: 1},
		},
	}
	handleMsgMhfUpdateGuildItem(session, pkt)

	if ack := readAck(t, session); ack.ErrorCode != 1 {
		t.Fatalf("ACK error code = %d, want failure", ack.ErrorCode)
	}
	if guildRepo.updateItemBoxCalled {
		t.Fatal("unauthorized guild item update reached persistence")
	}
}
