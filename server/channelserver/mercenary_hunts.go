package channelserver

import (
	"encoding/binary"
	"time"

	"erupe-ce/common/mhfmon"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type mercenaryDeparture struct {
	stage     *Stage
	rastaID   uint32
	startedAt time.Time
}

type mercenaryCompanion struct {
	stage   *Stage
	rastaID uint32
}

// The native client can publish its own companion before entering the stage.
// Bind that proposal to the next matching departure and discard it on entry.
func (s *Session) beginMercenaryDeparture(stage *Stage, now time.Time) {
	s.mercenaryDeparture = mercenaryDeparture{stage: stage, startedAt: now}
	if s.pendingMercenaryCompanion.stage == stage {
		s.mercenaryDeparture.rastaID = s.pendingMercenaryCompanion.rastaID
	}
	s.pendingMercenaryCompanion = mercenaryCompanion{}
}

func (s *Session) observeMercenaryDeparture(stage *Stage, pkt *mhfpacket.MsgSysSetStageBinary) {
	const (
		firstHunterCompanion = 23
		hunterSlots          = 4
		companionBytes       = 0x114
	)
	data := pkt.RawDataPayload
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || pkt.BinaryType0 != 1 ||
		pkt.BinaryType1 < firstHunterCompanion || pkt.BinaryType1 >= firstHunterCompanion+hunterSlots ||
		len(data) != companionBytes {
		return
	}
	// The regular Rasta has a registered ID at +8 and an active record at
	// +16. It can also carry the standard equipment flag (bit 2).
	// The DB matches the ID to the borrower's pact.
	rastaID := uint32(0)
	if data[4]&^byte(4) == 0 && data[16] != 0 {
		rastaID = binary.LittleEndian.Uint32(data[8:12])
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.mercenaryDeparture.stage == stage {
		s.mercenaryDeparture.rastaID = rastaID
	} else {
		s.pendingMercenaryCompanion = mercenaryCompanion{stage: stage, rastaID: rastaID}
	}
}

func (s *Session) recordMercenaryResult(questID uint16, data []byte, validTime bool, now time.Time) error {
	if s.server.mercenaryRepo == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	departure := s.mercenaryDeparture
	matching := s.towerGuardianQuestID == questID && s.towerGuardianRunID != "" && departure.stage != nil
	s.lifecycleMu.Unlock()
	if !matching || departure.rastaID == 0 || !validTime ||
		len(data) < killLogHeaderSize+killLogMonsterCount || data[questResultCodeOffset] != questResultCodeCleared {
		return nil
	}
	var monsters []int32
	for id, count := range data[killLogHeaderSize : killLogHeaderSize+killLogMonsterCount] {
		if count > 0 && mhfmon.Monsters[id].Large {
			monsters = append(monsters, int32(id))
		}
	}
	if len(monsters) == 0 {
		return nil
	}
	return s.server.mercenaryRepo.RecordMercenaryHunts(s.charID, departure.rastaID, departure.startedAt, now, monsters)
}
