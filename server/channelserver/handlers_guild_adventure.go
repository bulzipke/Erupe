package channelserver

import (
	"errors"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// GuildAdventure represents a guild adventure expedition.
type GuildAdventure struct {
	ID          uint32 `db:"id"`
	Destination uint32 `db:"destination"`
	Charge      uint32 `db:"charge"`
	Depart      uint32 `db:"depart"`
	Return      uint32 `db:"return"`
	CollectedBy string `db:"collected_by"`
}

func handleMsgMhfLoadGuildAdventure(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfLoadGuildAdventure)
	guild, err := s.server.guildRepo.GetByCharID(s.charID)
	if err != nil || guild == nil {
		s.logger.Error("Failed to get guild for character", zap.Error(err))
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 1))
		return
	}
	adventures, err := s.server.guildRepo.ListAdventures(guild.ID)
	if err != nil {
		s.logger.Error("Failed to get guild adventures from db", zap.Error(err))
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 1))
		return
	}
	temp := byteframe.NewByteFrame()
	for _, adventureData := range adventures {
		temp.WriteUint32(adventureData.ID)
		temp.WriteUint32(adventureData.Destination)
		temp.WriteUint32(adventureData.Charge)
		temp.WriteUint32(adventureData.Depart)
		temp.WriteUint32(adventureData.Return)
		temp.WriteBool(stringsupport.CSVContains(adventureData.CollectedBy, int(s.charID)))
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(uint8(len(adventures)))
	bf.WriteBytes(temp.Data())
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfRegistGuildAdventure(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfRegistGuildAdventure)
	membership, err := s.server.guildRepo.GetCharacterMembership(s.charID)
	if err != nil || membership == nil || membership.CharID != s.charID || membership.IsApplicant {
		s.logger.Error("Failed to get guild for character", zap.Error(err))
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if err := s.server.guildRepo.CreateAdventureForGuild(membership.GuildID, s.charID, pkt.Destination, TimeAdjusted().Unix(), TimeAdjusted().Add(6*time.Hour).Unix()); err != nil {
		s.logger.Error("Failed to register guild adventure", zap.Error(err))
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfAcquireGuildAdventure(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAcquireGuildAdventure)
	membership, err := s.server.guildRepo.GetCharacterMembership(s.charID)
	if err != nil || membership == nil || membership.CharID != s.charID || membership.IsApplicant {
		if err != nil {
			s.logger.Error("Failed to get guild membership for adventure collection", zap.Error(err))
		}
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if err := s.server.guildRepo.CollectAdventureForGuild(membership.GuildID, pkt.ID, s.charID); err != nil {
		s.logger.Error("Failed to collect adventure", zap.Error(err))
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfChargeGuildAdventure(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfChargeGuildAdventure)
	membership, err := s.server.guildRepo.GetCharacterMembership(s.charID)
	if err != nil || membership == nil || membership.CharID != s.charID || membership.IsApplicant {
		if err != nil {
			s.logger.Error("Failed to get guild membership for adventure charge", zap.Error(err))
		}
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if err := s.server.guildRepo.ChargeAdventureForGuild(membership.GuildID, pkt.ID, s.charID, pkt.Amount); err != nil {
		s.logger.Error("Failed to charge guild adventure", zap.Error(err))
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfRegistGuildAdventureDiva(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfRegistGuildAdventureDiva)
	options := s.server.erupeConfig
	mode := options.DebugOptions.DivaOverride
	repo, ok := s.server.divaRepo.(DivaSpecialAdventureRepository)
	if !ok || s.charID == 0 || options.RealClientMode != cfg.ZZ ||
		(mode != -1 && mode != 3) || options.DebugOptions.InGameTimeOverrideHour != nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	// Resolve membership, earned hall access and the real deadline together.
	// A preflight GetDivaSpecialHall followed by an ordinary insert has a race
	// with guild changes and the welcome-song period closing.
	if err := repo.RegisterDivaSpecialAdventure(s.charID, pkt.Destination, pkt.Charge, mode); err != nil {
		if !errors.Is(err, errDivaSpecialAdventureUnavailable) {
			s.logger.Warn("Failed to register Diva special guild adventure", zap.Error(err))
		}
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}
