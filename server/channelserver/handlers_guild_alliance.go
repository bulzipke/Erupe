package channelserver

import (
	"errors"
	"time"

	"erupe-ce/common/byteframe"
	ps "erupe-ce/common/pascalstring"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// GuildAlliance represents a multi-guild alliance.
type GuildAlliance struct {
	ID           uint32    `db:"id"`
	Name         string    `db:"name"`
	CreatedAt    time.Time `db:"created_at"`
	TotalMembers uint16
	Recruiting   bool `db:"recruiting"`

	ParentGuildID uint32 `db:"parent_id"`
	SubGuild1ID   uint32 `db:"sub1_id"`
	SubGuild2ID   uint32 `db:"sub2_id"`

	ParentGuild Guild
	SubGuild1   Guild
	SubGuild2   Guild
}

func (a *GuildAlliance) guildCount() uint8 {
	count := uint8(1)
	if a.SubGuild1ID > 0 {
		count++
	}
	if a.SubGuild2ID > 0 {
		count++
	}
	return count
}

func handleMsgMhfCreateJoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCreateJoint)
	if !s.validateNameInput("guild_alliance", pkt.Name) {
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x01, 0x01, 0x01, 0x01})
		return
	}
	membership, err := s.server.guildRepo.GetCharacterMembership(s.charID)
	if err != nil || membership == nil || membership.CharID != s.charID || membership.IsApplicant || !membership.IsLeader ||
		membership.GuildID != pkt.GuildID {
		if err != nil {
			s.logger.Error("Failed to get guild membership for alliance creation", zap.Error(err))
		}
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x01, 0x01, 0x01, 0x01})
		return
	}
	if err := s.server.guildRepo.CreateAllianceForMember(pkt.Name, membership.GuildID, s.charID); err != nil {
		s.logger.Error("Failed to create guild alliance in db", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, []byte{0x01, 0x01, 0x01, 0x01})
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x01, 0x01, 0x01, 0x01})
}

func handleMsgMhfOperateJoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfOperateJoint)

	guild, err := s.server.guildRepo.GetByID(pkt.GuildID)
	if err != nil {
		s.logger.Error("Failed to get guild info", zap.Error(err))
	}
	if guild == nil {
		s.logger.Error("Guild not found for alliance operation", zap.Uint32("GuildID", pkt.GuildID))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	alliance, err := s.server.guildRepo.GetAllianceByID(pkt.AllianceID)
	if err != nil {
		s.logger.Error("Failed to get alliance info", zap.Error(err))
	}
	if alliance == nil {
		s.logger.Error("Alliance not found for operation", zap.Uint32("AllianceID", pkt.AllianceID))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	switch pkt.Action {
	case mhfpacket.OPERATE_JOINT_APPLY:
		if guild.Rank(s.server.erupeConfig.RealClientMode) == 0 {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		ackAllianceOperation(s, pkt, s.server.guildRepo.ApplyToAlliance(alliance.ID, guild.ID, s.charID))
	case mhfpacket.OPERATE_JOINT_ACCEPT, mhfpacket.OPERATE_JOINT_REJECT:
		if pkt.Data1 == nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		applicantGuildID := pkt.Data1.ReadUint32()
		if pkt.Data1.Err() != nil || applicantGuildID == 0 {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
		accept := pkt.Action == mhfpacket.OPERATE_JOINT_ACCEPT
		ackAllianceOperation(s, pkt, s.server.guildRepo.DecideAllianceApplication(alliance.ID, guild.ID, applicantGuildID, s.charID, accept))
	case mhfpacket.OPERATE_JOINT_DISBAND:
		if guild.LeaderCharID == s.charID && alliance.ParentGuildID == guild.ID {
			if err := s.server.guildRepo.DeleteAlliance(alliance.ID); err != nil {
				s.logger.Error("Failed to disband alliance", zap.Error(err))
			}
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		} else {
			s.logger.Warn(
				"Non-owner of alliance attempted disband",
				zap.Uint32("CharID", s.charID),
				zap.Uint32("AllyID", alliance.ID),
			)
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		}
	case mhfpacket.OPERATE_JOINT_LEAVE:
		membership, memberErr := s.server.guildRepo.GetCharacterMembership(s.charID)
		if memberErr == nil && membership != nil && membership.CharID == s.charID && !membership.IsApplicant &&
			membership.IsLeader && membership.GuildID == guild.ID && guild.LeaderCharID == s.charID {
			ackAllianceOperation(s, pkt, s.server.guildRepo.LeaveAlliance(alliance.ID, membership.GuildID, s.charID))
		} else {
			s.logger.Warn(
				"Non-owner of guild attempted alliance leave",
				zap.Uint32("CharID", s.charID),
			)
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		}
	case mhfpacket.OPERATE_JOINT_ALLOW, mhfpacket.OPERATE_JOINT_DENY:
		if guild.LeaderCharID == s.charID && alliance.ParentGuildID == guild.ID {
			recruiting := pkt.Action == mhfpacket.OPERATE_JOINT_ALLOW
			ackAllianceOperation(s, pkt, s.server.guildRepo.SetAllianceRecruiting(alliance.ID, recruiting))
		} else {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		}
	case mhfpacket.OPERATE_JOINT_KICK:
		membership, memberErr := s.server.guildRepo.GetCharacterMembership(s.charID)
		if memberErr == nil && membership != nil && membership.CharID == s.charID && !membership.IsApplicant &&
			membership.IsLeader && membership.GuildID == alliance.ParentGuildID && alliance.ParentGuild.LeaderCharID == s.charID {
			kickedGuildID := pkt.Data1.ReadUint32()
			if err := s.server.guildRepo.KickGuildFromAlliance(alliance.ID, kickedGuildID, s.charID); err != nil {
				s.logger.Error("Failed to kick guild from alliance", zap.Error(err))
			}
			doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		} else {
			s.logger.Warn(
				"Non-owner of alliance attempted kick",
				zap.Uint32("CharID", s.charID),
				zap.Uint32("AllyID", alliance.ID),
			)
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		}
	default:
		s.logger.Error("unhandled operate joint action", zap.Uint8("action", uint8(pkt.Action)))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
	}
}

func ackAllianceOperation(s *Session, pkt *mhfpacket.MsgMhfOperateJoint, err error) {
	if err == nil {
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if !errors.Is(err, errGuildScopedMutationRejected) {
		s.logger.Error("Alliance operation failed", zap.Error(err), zap.Uint8("action", uint8(pkt.Action)))
	}
	doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfInfoJoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfInfoJoint)
	bf := byteframe.NewByteFrame()
	alliance, err := s.server.guildRepo.GetAllianceByID(pkt.AllianceID)
	if err != nil || alliance == nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	bf.WriteUint32(alliance.ID)
	bf.WriteUint32(uint32(alliance.CreatedAt.Unix()))
	bf.WriteUint16(alliance.TotalMembers)
	bf.WriteUint8(0)
	bf.WriteBool(!alliance.Recruiting)
	ps.Uint16(bf, alliance.Name, true)
	bf.WriteUint8(alliance.guildCount())
	bf.WriteUint32(alliance.ParentGuildID)
	bf.WriteUint32(alliance.ParentGuild.LeaderCharID)
	bf.WriteUint16(alliance.ParentGuild.Rank(s.server.erupeConfig.RealClientMode))
	bf.WriteUint16(alliance.ParentGuild.MemberCount)
	ps.Uint16(bf, alliance.ParentGuild.Name, true)
	ps.Uint16(bf, alliance.ParentGuild.LeaderName, true)
	if alliance.SubGuild1ID > 0 {
		bf.WriteUint32(alliance.SubGuild1ID)
		bf.WriteUint32(alliance.SubGuild1.LeaderCharID)
		bf.WriteUint16(alliance.SubGuild1.Rank(s.server.erupeConfig.RealClientMode))
		bf.WriteUint16(alliance.SubGuild1.MemberCount)
		ps.Uint16(bf, alliance.SubGuild1.Name, true)
		ps.Uint16(bf, alliance.SubGuild1.LeaderName, true)
	}
	if alliance.SubGuild2ID > 0 {
		bf.WriteUint32(alliance.SubGuild2ID)
		bf.WriteUint32(alliance.SubGuild2.LeaderCharID)
		bf.WriteUint16(alliance.SubGuild2.Rank(s.server.erupeConfig.RealClientMode))
		bf.WriteUint16(alliance.SubGuild2.MemberCount)
		ps.Uint16(bf, alliance.SubGuild2.Name, true)
		ps.Uint16(bf, alliance.SubGuild2.LeaderName, true)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}
