package channelserver

import (
	"errors"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

var errDivaSpecialPugiUnavailable = errors.New("diva special poogie clothing change unavailable")

type DivaSpecialPugiRepository interface {
	ChangeDivaSpecialPugi(charID, guildID uint32, slot uint8, outfit uint32, override int) error
}

// In map 445 the native selection loop exposes all ten clothes without the
// ordinary guild's unlock bitmask or material cost (106f4580/106f7a10). Keep
// the original uint32 until validated, rather than truncating forged IDs.
func validDivaSpecialPugiClothing(slot uint8, outfit uint32) bool {
	return slot >= 1 && slot <= 3 && outfit <= 9
}

func handleChangeDivaSpecialPugi(s *Session, p *mhfpacket.MsgMhfOperateGuild) bool {
	mode := s.server.erupeConfig.DebugOptions.DivaOverride
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || (mode != -1 && mode != 3) ||
		s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil || p.Data1 == nil {
		return false
	}
	slot := uint8(p.Action - mhfpacket.OperateGuildChangeDivaPugi1 + 1)
	outfit := p.Data1.ReadUint32()
	if p.Data1.Err() != nil || !validDivaSpecialPugiClothing(slot, outfit) {
		return false
	}
	r, ok := s.server.divaRepo.(DivaSpecialPugiRepository)
	if !ok {
		return false
	}
	if err := r.ChangeDivaSpecialPugi(s.charID, p.GuildID, slot, outfit, mode); err != nil {
		if !errors.Is(err, errDivaSpecialPugiUnavailable) {
			s.logger.Warn("Failed to change Diva special Poogie clothing", zap.Error(err))
		}
		return false
	}
	return true
}
