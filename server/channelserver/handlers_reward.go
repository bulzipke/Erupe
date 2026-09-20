package channelserver

import (
	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func handleMsgMhfGetAdditionalBeatReward(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetAdditionalBeatReward)
	// Actual response in packet captures are all just giant batches of null bytes
	// I'm assuming this is because it used to be tied to an actual event and
	// they never bothered killing off the packet when they made it static
	const beatRewardResponseSize = 0x104
	doAckBufSucceed(s, pkt.AckHandle, make([]byte, beatRewardResponseSize))
}

func handleMsgMhfGetUdRankingRewardList(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdRankingRewardList)
	// ZZ: item kind/id/quantity, personal-or-guild, rank upper/lower.
	var rewards []DivaRewardCatalogEntry
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		rewards = divaSongRewardCatalog()
	}
	doAckBufSucceed(s, pkt.AckHandle, divaRankingRewardPayload(rewards))
}

func handleMsgMhfGetRewardSong(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetRewardSong)
	// RE-confirmed layout (22 bytes):
	//   +0x00 u8  error
	//   +0x01 u8  usage_count
	//   +0x02 u32 prayer_id
	//   +0x06 u32 prayer_end  (0xFFFFFFFF = no active prayer)
	//   then 4 × (u8 color_error, u8 color_id, u8 color_usage_count)
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)           // error
	bf.WriteUint8(0)           // usage_count
	bf.WriteUint32(0)          // prayer_id
	bf.WriteUint32(0xFFFFFFFF) // prayer_end: no active prayer
	for colorID := uint8(1); colorID <= 4; colorID++ {
		bf.WriteUint8(0)       // color_error
		bf.WriteUint8(colorID) // color_id
		bf.WriteUint8(0)       // color_usage_count
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfUseRewardSong(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfUseRewardSong)
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00})
}

func handleMsgMhfAddRewardSongCount(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddRewardSongCount)
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00})
}

func handleMsgMhfAcquireMonthlyReward(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAcquireMonthlyReward)

	resp := byteframe.NewByteFrame()
	resp.WriteUint32(0)

	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

func handleMsgMhfAcceptReadReward(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented
