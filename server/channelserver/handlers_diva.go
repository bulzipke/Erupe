package channelserver

import (
	"errors"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// Diva Defense event duration constants (all values in seconds)
const (
	divaPhaseDuration = 601200      // 6d 23h = first song phase
	divaInterlude     = 3900        // 65 min = gap between phases
	divaWeekDuration  = secsPerWeek // 7 days = subsequent phase length
	divaTotalLifespan = 2977200     // ~34.5 days = full event window
)

// Serialization is side-effect free. Lifecycle creation/renewal happens once in
// divaEvent, under the repository lock, before either ID or dates are sent.
func generateDivaTimestamps(_ *Session, start uint32, debug bool) []uint32 {
	timestamps := make([]uint32, 6)
	if debug && start >= 1 && start <= 3 {
		if start == 1 {
			start = uint32(TimeMidnight().Unix())
		} else {
			start = uint32(divaLifecycleStart(TimeAdjusted(), int(start)).Unix())
		}
	}
	timestamps[0] = start
	timestamps[1] = timestamps[0] + divaPhaseDuration
	timestamps[2] = timestamps[1] + divaInterlude
	timestamps[3] = timestamps[1] + divaWeekDuration
	timestamps[4] = timestamps[3] + divaInterlude
	timestamps[5] = timestamps[3] + divaWeekDuration
	return timestamps
}

func handleMsgMhfGetUdSchedule(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdSchedule)
	bf := byteframe.NewByteFrame()

	if s.server.erupeConfig.DebugOptions.DivaOverride == 0 {
		if s.server.erupeConfig.RealClientMode >= cfg.Z2 {
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 36))
		} else {
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 32))
		}
		return
	}
	event, err := s.divaEvent()
	if err != nil || event.ID == 0 {
		s.logger.Error("Failed to resolve diva schedule", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	timestamps := generateDivaTimestamps(s, event.StartTime, false)

	if s.server.erupeConfig.RealClientMode >= cfg.Z2 {
		bf.WriteUint32(event.ID)
	}
	for i := range timestamps {
		bf.WriteUint32(timestamps[i])
	}

	bf.WriteUint16(0x19) // Unk 00011001
	bf.WriteUint16(0x2D) // Unk 00101101
	bf.WriteUint16(0x02) // Unk 00000010
	bf.WriteUint16(0x02) // Unk 00000010

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdInfo(s *Session, p mhfpacket.MHFPacket) {
	handleDivaNotices(s, p.(*mhfpacket.MsgMhfGetUdInfo))
}

// defaultBeadTypes are used when the database has no bead rows configured.
var defaultBeadTypes = []int{1, 3, 4, 8}

func handleMsgMhfGetKijuInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetKijuInfo)

	// RE-confirmed entry layout (546 bytes each):
	//   +0x000 char[32]  name
	//   +0x020 char[512] description
	//   +0x220 u8        color_id  (slot index, 1-based)
	//   +0x221 u8        bead_type (effect ID)
	// Response: u8 count + count × 546 bytes.
	beadTypes, err := s.server.divaRepo.GetBeads()
	if err != nil || len(beadTypes) == 0 {
		beadTypes = defaultBeadTypes
	}
	if len(beadTypes) > 4 {
		beadTypes = beadTypes[:4]
	}

	lang := getLangStrings(s.server)
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(uint8(len(beadTypes)))
	for i, bt := range beadTypes {
		name, desc := lang.beadName(bt), lang.beadDescription(bt)
		bf.WriteBytes(stringsupport.PaddedString(name, 32, true))
		bf.WriteBytes(stringsupport.PaddedString(desc, 512, true))
		bf.WriteUint8(uint8(i + 1)) // color_id: slot 1..N
		bf.WriteUint8(uint8(bt))    // bead_type: effect ID
	}

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfSetKiju(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSetKiju)
	beadIndex := int(pkt.Unk1)
	beads, err := s.server.divaRepo.GetBeads()
	if err != nil {
		doAckSimpleFail(s, pkt.AckHandle, []byte{1})
		return
	}
	if len(beads) == 0 {
		beads = defaultBeadTypes
	}
	if beadIndex < 1 || beadIndex > min(len(beads), 4) {
		doAckBufSucceed(s, pkt.AckHandle, []byte{0xF9})
		return
	}
	event, err := s.divaEvent()
	if err != nil || event.ID == 0 {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	now := TimeAdjusted()
	if now.Before(s.divaSongStart(event)) || now.Unix() >= int64(event.StartTime)+divaPhaseDuration {
		doAckBufSucceed(s, pkt.AckHandle, []byte{0xF9})
		return
	}
	if err := s.server.divaRepo.AssignBead(s.charID, event.ID, beadIndex, now); err != nil {
		if errors.Is(err, errDivaBeadLocked) {
			// HD 0x103abae0 recognizes only -7 as a business refusal.
			doAckBufSucceed(s, pkt.AckHandle, []byte{0xF9})
			return
		}
		s.logger.Warn("Failed to assign bead",
			zap.Uint32("charID", s.charID),
			zap.Int("beadIndex", beadIndex),
			zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, []byte{1})
		return
	} else {
		s.currentBeadIndex = beadIndex
	}
	doAckBufSucceed(s, pkt.AckHandle, []byte{0x00})
}

func handleMsgMhfAddUdPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddUdPoint)

	// Find the current diva event to associate points with.
	eventID := uint32(0)
	if s.server.divaRepo != nil {
		event, err := s.divaEvent()
		if err == nil {
			eventID = event.ID
		}
	}

	if eventID != 0 && s.charID != 0 && (pkt.QuestPoints > 0 || pkt.BonusPoints > 0) {
		if err := s.server.divaRepo.RecordDivaPoints(s.charID, eventID, pkt.QuestPoints, pkt.BonusPoints, TimeAdjusted()); err != nil {
			s.logger.Warn("Failed to add diva points",
				zap.Uint32("charID", s.charID),
				zap.Uint32("questPoints", pkt.QuestPoints),
				zap.Uint32("bonusPoints", pkt.BonusPoints),
				zap.Error(err))
			doAckSimpleFail(s, pkt.AckHandle, []byte{1})
			return
		}
		s.logger.Info("Diva song points saved", zap.Uint32("charID", s.charID),
			zap.Uint32("eventID", eventID), zap.Uint32("questPoints", pkt.QuestPoints), zap.Uint32("bonusPoints", pkt.BonusPoints))
	} else {
		s.logger.Warn("Diva song points not recorded", zap.Uint32("charID", s.charID),
			zap.Uint32("eventID", eventID), zap.Uint32("questPoints", pkt.QuestPoints), zap.Uint32("bonusPoints", pkt.BonusPoints))
		if eventID == 0 {
			doAckSimpleFail(s, pkt.AckHandle, []byte{1})
			return
		}
	}

	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
}

func handleMsgMhfGetUdMyPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdMyPoint)
	event, err := s.divaEvent()
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, []byte{1})
		return
	}
	firstDay := divaNoon(s.divaSongStart(event))
	days, err := s.server.divaRepo.GetDivaDays(s.charID, event.ID, firstDay)
	if err != nil {
		s.logger.Warn("Failed to get daily diva points", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, []byte{1})
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, divaMyPointPayload(days, firstDay))
}

// udMilestones are the global contribution milestones for Diva Defense.
// RE confirms: 64 × u64 target_values + 64 × u8 target_types + u64 total = ~585 bytes.
// Slots 0–12 are populated; slots 13–63 are zero.
var udMilestones = []uint64{
	500000, 1000000, 2000000, 3000000, 5000000, 7000000, 10000000,
	15000000, 20000000, 30000000, 50000000, 70000000, 100000000,
}

func handleMsgMhfGetUdTotalPointInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTotalPointInfo)

	event, err := s.divaEvent()
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, []byte{1})
		return
	}
	questTotal, bonusTotal, err := s.server.divaRepo.GetTotalPoints(event.ID)
	if err != nil {
		s.logger.Warn("Failed to get total bead points", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, []byte{1})
		return
	}
	total := questTotal + bonusTotal

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // error = success
	// 64 × u64 target_values (big-endian)
	for i := 0; i < 64; i++ {
		var v uint64
		if i < len(udMilestones) {
			v = udMilestones[i]
		}
		bf.WriteUint64(v)
	}
	// 64 × u8 target_types (0 = global)
	for i := 0; i < 64; i++ {
		bf.WriteUint8(0)
	}
	// u64 total_souls
	bf.WriteUint64(uint64(total))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdSelectedColorInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdSelectedColorInfo)
	event, err := s.divaEvent()
	if err != nil || event.ID == 0 {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	colors, err := s.server.divaRepo.GetDivaWinningColors(event.ID, divaNoon(s.divaSongStart(event)), TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, append([]byte{0}, colors...))
}

// Shared by the native point-list response and bonus target validation.
// Keep the existing point values unchanged here.
var divaSongMonsterPoints = []struct {
	MID    uint8
	Points uint16
}{
	{MID: 0x01, Points: 0x3C}, // em1 Rathian
	{MID: 0x02, Points: 0x5A}, // em2 Fatalis
	{MID: 0x06, Points: 0x14}, // em6 Yian Kut-Ku
	{MID: 0x07, Points: 0x50}, // em7 Lao-Shan Lung
	{MID: 0x08, Points: 0x28}, // em8 Cephadrome
	{MID: 0x0B, Points: 0x3C}, // em11 Rathalos
	{MID: 0x0E, Points: 0x3C}, // em14 Diablos
	{MID: 0x0F, Points: 0x46}, // em15 Khezu
	{MID: 0x11, Points: 0x46}, // em17 Gravios
	{MID: 0x14, Points: 0x28}, // em20 Gypceros
	{MID: 0x15, Points: 0x3C}, // em21 Plesioth
	{MID: 0x16, Points: 0x32}, // em22 Basarios
	{MID: 0x1A, Points: 0x32}, // em26 Monoblos
	{MID: 0x1B, Points: 0x0A}, // em27 Velocidrome
	{MID: 0x1C, Points: 0x0A}, // em28 Gendrome
	{MID: 0x1F, Points: 0x0A}, // em31 Iodrome
	{MID: 0x21, Points: 0x50}, // em33 Kirin
	{MID: 0x24, Points: 0x64}, // em36 Crimson Fatalis
	{MID: 0x25, Points: 0x3C}, // em37 Pink Rathian
	{MID: 0x26, Points: 0x1E}, // em38 Blue Yian Kut-Ku
	{MID: 0x27, Points: 0x28}, // em39 Purple Gypceros
	{MID: 0x28, Points: 0x50}, // em40 Yian Garuga
	{MID: 0x29, Points: 0x5A}, // em41 Silver Rathalos
	{MID: 0x2A, Points: 0x50}, // em42 Gold Rathian
	{MID: 0x2B, Points: 0x3C}, // em43 Black Diablos
	{MID: 0x2C, Points: 0x3C}, // em44 White Monoblos
	{MID: 0x2D, Points: 0x46}, // em45 Red Khezu
	{MID: 0x2E, Points: 0x3C}, // em46 Green Plesioth
	{MID: 0x2F, Points: 0x50}, // em47 Black Gravios
	{MID: 0x30, Points: 0x1E}, // em48 Daimyo Hermitaur
	{MID: 0x31, Points: 0x3C}, // em49 Azure Rathalos
	{MID: 0x32, Points: 0x50}, // em50 Ashen Lao-Shan Lung
	{MID: 0x33, Points: 0x3C}, // em51 Blangonga
	{MID: 0x34, Points: 0x28}, // em52 Congalala
	{MID: 0x35, Points: 0x50}, // em53 Rajang
	{MID: 0x36, Points: 0x6E}, // em54 Kushala Daora
	{MID: 0x37, Points: 0x50}, // em55 Shen Gaoren
	{MID: 0x3A, Points: 0x50}, // em58 Yama Tsukami
	{MID: 0x3B, Points: 0x6E}, // em59 Chameleos
	{MID: 0x40, Points: 0x64}, // em64 Lunastra
	{MID: 0x41, Points: 0x6E}, // em65 Teostra
	{MID: 0x43, Points: 0x28}, // em67 Shogun Ceanataur
	{MID: 0x44, Points: 0x0A}, // em68 Bulldrome
	{MID: 0x47, Points: 0x6E}, // em71 White Fatalis
	{MID: 0x4A, Points: 0xFA}, // em74 Hypnocatrice
	{MID: 0x4B, Points: 0xFA}, // em75 Lavasioth
	{MID: 0x4C, Points: 0x46}, // em76 Tigrex
	{MID: 0x4D, Points: 0x64}, // em77 Akantor
	{MID: 0x4E, Points: 0xFA}, // em78 Bright Hypnoc
	{MID: 0x4F, Points: 0xFA}, // em79 Lavasioth Subspecies
	{MID: 0x50, Points: 0xFA}, // em80 Espinas
	{MID: 0x51, Points: 0xFA}, // em81 Orange Espinas
	{MID: 0x52, Points: 0xFA}, // em82 White Hypnoc
	{MID: 0x53, Points: 0xFA}, // em83 Akura Vashimu
	{MID: 0x54, Points: 0xFA}, // em84 Akura Jebia
	{MID: 0x55, Points: 0xFA}, // em85 Berukyurosu
	{MID: 0x59, Points: 0xFA}, // em89 Pariapuria
	{MID: 0x5A, Points: 0xFA}, // em90 White Espinas
	{MID: 0x5B, Points: 0xFA}, // em91 Kamu Orugaron
	{MID: 0x5C, Points: 0xFA}, // em92 Nono Orugaron
	{MID: 0x5E, Points: 0xFA}, // em94 Dyuragaua
	{MID: 0x5F, Points: 0xFA}, // em95 Doragyurosu
	{MID: 0x60, Points: 0xFA}, // em96 Gurenzeburu
	{MID: 0x63, Points: 0xFA}, // em99 Rukodiora
	{MID: 0x65, Points: 0xFA}, // em101 Gogomoa
	{MID: 0x67, Points: 0xFA}, // em103 Taikun Zamuza
	{MID: 0x68, Points: 0xFA}, // em104 Abiorugu
	{MID: 0x69, Points: 0xFA}, // em105 Kuarusepusu
	{MID: 0x6A, Points: 0xFA}, // em106 Odibatorasu
	{MID: 0x6B, Points: 0xFA}, // em107 Disufiroa
	{MID: 0x6C, Points: 0xFA}, // em108 Rebidiora
	{MID: 0x6D, Points: 0xFA}, // em109 Anorupatisu
	{MID: 0x6E, Points: 0xFA}, // em110 Hyujikiki
	{MID: 0x6F, Points: 0xFA}, // em111 Midogaron
	{MID: 0x70, Points: 0xFA}, // em112 Giaorugu
	{MID: 0x72, Points: 0xFA}, // em114 Farunokku
	{MID: 0x73, Points: 0xFA}, // em115 Pokaradon
	{MID: 0x74, Points: 0xFA}, // em116 Shantien
	{MID: 0x77, Points: 0xFA}, // em119 Goruganosu
	{MID: 0x78, Points: 0xFA}, // em120 Aruganosu
	{MID: 0x79, Points: 0xFA}, // em121 Baruragaru
	{MID: 0x7A, Points: 0xFA}, // em122 Zerureusu
	{MID: 0x7B, Points: 0xFA}, // em123 Gougarf
	{MID: 0x7D, Points: 0xFA}, // em125 Forokururu
	{MID: 0x7E, Points: 0xFA}, // em126 Meraginasu
	{MID: 0x7F, Points: 0xFA}, // em127 Diorekkusu
	{MID: 0x80, Points: 0xFA}, // em128 Garuba Daora
	{MID: 0x81, Points: 0xFA}, // em129 Inagami
	{MID: 0x82, Points: 0xFA}, // em130 Varusaburosu
	{MID: 0x83, Points: 0xFA}, // em131 Poborubarumu
	{MID: 0x8B, Points: 0xFA}, // em139 Gureadomosu
	{MID: 0x8C, Points: 0xFA}, // em140 Harudomerugu
	{MID: 0x8D, Points: 0xFA}, // em141 Toridcless
	{MID: 0x8E, Points: 0xFA}, // em142 Gasurabazura
	{MID: 0x90, Points: 0xFA}, // em144 Yama Kurai
	{MID: 0x92, Points: 0x78}, // em146 Zinogre
	{MID: 0x93, Points: 0x78}, // em147 Deviljho
	{MID: 0x94, Points: 0x78}, // em148 Brachydios
	{MID: 0x96, Points: 0xFA}, // em150 Toa Tesukatora
	{MID: 0x97, Points: 0x78}, // em151 Barioth
	{MID: 0x98, Points: 0x78}, // em152 Uragaan
	{MID: 0x99, Points: 0x78}, // em153 Stygian Zinogre
	{MID: 0x9A, Points: 0xFA}, // em154 Guanzorumu
	{MID: 0x9E, Points: 0xFA}, // em158 Voljang
	{MID: 0x9F, Points: 0x78}, // em159 Nargacuga
	{MID: 0xA0, Points: 0xFA}, // em160 Keoaruboru
	{MID: 0xA1, Points: 0xFA}, // em161 Zenaserisu
	{MID: 0xA2, Points: 0x78}, // em162 Gore Magala
	{MID: 0xA4, Points: 0x78}, // em164 Shagaru Magala
	{MID: 0xA5, Points: 0x78}, // em165 Amatsu
	{MID: 0xA6, Points: 0xFA}, // em166 Elzelion
	{MID: 0xA9, Points: 0x78}, // em169 Seregios
	{MID: 0xAA, Points: 0xFA}, // em170 Bogabadorumu
}

func handleMsgMhfGetUdMonsterPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdMonsterPoint)
	resp := byteframe.NewByteFrame()
	resp.WriteUint8(uint8(len(divaSongMonsterPoints)))
	for _, mp := range divaSongMonsterPoints {
		resp.WriteUint8(mp.MID)
		resp.WriteUint16(mp.Points)
	}

	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

func handleMsgMhfGetUdDailyPresentList(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdDailyPresentList)
	// ZZ: item kind/id/quantity, HR-or-GR, rank upper/lower, participation day.
	var rewards []DivaRewardCatalogEntry
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		rewards = divaSongRewardCatalog()
	}
	doAckBufSucceed(s, pkt.AckHandle, divaDailyRewardPayload(rewards))
}

func handleMsgMhfGetUdNormaPresentList(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdNormaPresentList)
	payload := divaNormaRewardPayload(nil)
	if s.server.erupeConfig.RealClientMode == cfg.ZZ {
		payload = divaPrayerNormaRewardPayload()
	}
	doAckBufSucceed(s, pkt.AckHandle, payload)
}

func handleMsgMhfAcquireUdItem(s *Session, p mhfpacket.MHFPacket) {
	handleDivaRewardAcquire(s, p.(*mhfpacket.MsgMhfAcquireUdItem))
}

func handleMsgMhfGetUdRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdRanking)
	if pkt.Unk0 > 3 {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	var ranks []DivaRank
	var err error
	switch pkt.Unk0 {
	case 0:
		ranks, err = s.divaPersonalRanks()
	case 2:
		ranks, err = s.divaGuildRanks()
	}
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, divaRankingPayload(pkt.Unk0, ranks))
}

func handleMsgMhfGetUdMyRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdMyRanking)
	ranks, guildRanks, err := s.divaMyRanks()
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, divaMyRankingWithGuildPayload(ranks, guildRanks, s.charID))
}
