package channelserver

import (
	"erupe-ce/common/byteframe"
	ps "erupe-ce/common/pascalstring"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"time"
)

type CampaignEvent struct {
	ID           uint32    `db:"id"`
	MinHR        int16     `db:"min_hr"`
	MaxHR        int16     `db:"max_hr"`
	MinSR        int16     `db:"min_sr"`
	MaxSR        int16     `db:"max_sr"`
	MinGR        int16     `db:"min_gr"`
	MaxGR        int16     `db:"max_gr"`
	RewardType   uint16    `db:"reward_type"`
	Stamps       uint8     `db:"stamps"`
	ReceiveType  uint8     `db:"receive_type"`
	BackgroundID uint16    `db:"background_id"`
	Start        time.Time `db:"start_time"`
	End          time.Time `db:"end_time"`
	Title        string    `db:"title"`
	Reward       string    `db:"reward"`
	Link         string    `db:"link"`
	Prefix       string    `db:"code_prefix"`
}

type CampaignCategory struct {
	ID          uint16 `db:"id"`
	Type        uint8  `db:"type"`
	Title       string `db:"title"`
	Description string `db:"description"`
}

type CampaignLink struct {
	CategoryID uint16 `db:"category_id"`
	CampaignID uint32 `db:"campaign_id"`
}

type CampaignReward struct {
	ID       uint32    `db:"id"`
	ItemType uint16    `db:"item_type"`
	Quantity uint16    `db:"quantity"`
	ItemID   uint16    `db:"item_id"`
	Deadline time.Time `db:"deadline"`
}

// campaignRequiredStamps returns the stamp requirement for a campaign,
// clamping to a minimum of 1. Campaigns with 0 stamps in the DB are
// treated as requiring a single stamp (code redemption) to unlock.
func campaignRequiredStamps(stamps int) int {
	if stamps < 1 {
		return 1
	}
	return stamps
}

// Campaign string limits, from the client's event parser (FUN_1151b7c0 HD,
// FUN_114f4f20 non-HD): it reads five uint8-length strings per event and copies
// the first four only when the length (terminator included) is 1..0x3f and the
// fifth (link) when it is 1..0x7f; a longer string is skipped and left blank.
const (
	campaignTextMax = 0x3f - 1
	campaignLinkMax = 0x7f - 1
)

// writeCampaignString writes a campaign text field in the client's text
// encoding (EUC-KR, stringsupport.UTF8ToSJIS) with a uint8 length prefix and a
// terminator, cut at a whole character within limit bytes. pascalstring encodes
// Shift-JIS, which has no Hangul: a Korean title failed to encode and went out
// as a bare zero length, and the Japanese seed text drew as broken glyphs on
// the CP949 client.
func writeCampaignString(bf *byteframe.ByteFrame, text string, limit int) {
	var out []byte
	for _, r := range text {
		b := stringsupport.UTF8ToSJIS(string(r))
		if len(out)+len(b) > limit {
			break
		}
		out = append(out, b...)
	}
	bf.WriteUint8(uint8(len(out) + 1))
	bf.WriteNullTerminatedBytes(out)
}

// The client copies a category title only when its sent length is under 0x40
// and a description only under 0x100 (FUN_1151bc30 HD).
const (
	campaignCategoryTitleMax = 0x3F
	campaignCategoryDescMax  = 0xFF
)

// campaignCategoryText encodes a category title or description with its
// terminator counted in the sent length, cut at a whole character to fit max.
// The client copies each category into a slot it zeroes only once, and writes
// every category of the other tab into the slot before the first category it
// keeps; copying just the text left the tail of a longer earlier description
// behind it ("…있습니다.다.있습니다." under 프리미엄 키트·오리지널).
func campaignCategoryText(text string, max int) []byte {
	var out []byte
	for _, r := range text {
		b := stringsupport.UTF8ToSJIS(string(r))
		if len(out)+len(b) > max-1 {
			break
		}
		out = append(out, b...)
	}
	return append(out, 0)
}

// writeCampaignCount writes a section count the way the client reads it: one
// byte, or 0xFF followed by a big-endian uint16 when the byte would be 0xFF or
// more (a plain count of 255 would be taken for the escape).
func writeCampaignCount(bf *byteframe.ByteFrame, n int) {
	if n >= 0xFF {
		bf.WriteUint8(0xFF)
		bf.WriteUint16(uint16(n))
		return
	}
	bf.WriteUint8(uint8(n))
}

// writeCampaignPrefix writes one entry of the code-prefix section: campaign ID,
// one byte the client stores but never reads, and the four prefix bytes. The
// client matches the first four characters of an entered event code against
// these to find its campaign (FUN_1151bec0 HD).
func writeCampaignPrefix(bf *byteframe.ByteFrame, campaignID uint32, prefix string) {
	b := make([]byte, 4)
	copy(b, stringsupport.UTF8ToSJIS(prefix))
	bf.WriteUint32(campaignID)
	bf.WriteUint8(0)
	bf.WriteBytes(b)
}

func handleMsgMhfEnumerateCampaign(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfEnumerateCampaign)
	if s.server.db == nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	bf := byteframe.NewByteFrame()

	var events []CampaignEvent
	var categories []CampaignCategory
	var campaignLinks []CampaignLink

	err := s.server.db.Select(&events, "SELECT id,min_hr,max_hr,min_sr,max_sr,min_gr,max_gr,reward_type,stamps,receive_type,background_id,start_time,end_time,title,reward,link,code_prefix FROM campaigns")
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	err = s.server.db.Select(&categories, "SELECT id, type, title, description FROM campaign_categories")
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	err = s.server.db.Select(&campaignLinks, "SELECT campaign_id, category_id FROM campaign_category_links")
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	// The response has four sections in this order: events, code prefixes,
	// categories, category links. The client parses all four (FUN_1151b7c0,
	// FUN_1151bc30, FUN_1151bd60 HD; FUN_114f4f20.. non-HD); without the prefix
	// section it read the category count as the prefix count and every later
	// field out of place, so both tents drew garbage or an empty list.
	writeCampaignCount(bf, len(events))
	for _, event := range events {
		bf.WriteUint32(event.ID)
		bf.WriteUint32(0)
		bf.WriteInt16(event.MinHR)
		bf.WriteInt16(event.MaxHR)
		bf.WriteInt16(event.MinSR)
		bf.WriteInt16(event.MaxSR)
		if s.server.erupeConfig.RealClientMode >= cfg.G3 {
			bf.WriteInt16(event.MinGR)
			bf.WriteInt16(event.MaxGR)
		}
		bf.WriteUint16(event.RewardType)
		bf.WriteUint8(event.Stamps)
		bf.WriteUint8(event.ReceiveType)
		bf.WriteUint16(event.BackgroundID)
		bf.WriteUint16(0)
		bf.WriteUint32(uint32(event.Start.Unix()))
		bf.WriteUint32(uint32(event.End.Unix()))
		bf.WriteBool(event.End.Before(time.Now()))
		writeCampaignString(bf, event.Title, campaignTextMax)
		writeCampaignString(bf, event.Reward, campaignTextMax)
		writeCampaignString(bf, event.Prefix, campaignTextMax)
		ps.Uint8(bf, "", false)
		writeCampaignString(bf, event.Link, campaignLinkMax)
	}

	var prefixed []CampaignEvent
	for _, event := range events {
		if event.Prefix != "" {
			prefixed = append(prefixed, event)
		}
	}
	writeCampaignCount(bf, len(prefixed))
	for _, event := range prefixed {
		writeCampaignPrefix(bf, event.ID, event.Prefix)
	}

	writeCampaignCount(bf, len(categories))
	for _, category := range categories {
		bf.WriteUint16(category.ID)
		bf.WriteUint8(category.Type)
		xTitle := campaignCategoryText(category.Title, campaignCategoryTitleMax)
		xDescription := campaignCategoryText(category.Description, campaignCategoryDescMax)
		bf.WriteUint8(uint8(len(xTitle)))
		bf.WriteUint8(uint8(len(xDescription)))
		bf.WriteBytes(xTitle)
		bf.WriteBytes(xDescription)
	}

	writeCampaignCount(bf, len(campaignLinks))
	for _, link := range campaignLinks {
		bf.WriteUint16(link.CategoryID)
		bf.WriteUint32(link.CampaignID)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfStateCampaign(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfStateCampaign)
	if s.server.db == nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	bf := byteframe.NewByteFrame()
	var required int
	var deadline time.Time
	var stamps []uint32

	err := s.server.db.Select(&stamps, "SELECT id FROM campaign_state WHERE campaign_id = $1 AND character_id = $2", pkt.CampaignID, s.charID)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	err = s.server.db.QueryRow(`SELECT stamps, end_time FROM campaigns WHERE id = $1`, pkt.CampaignID).Scan(&required, &deadline)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	bf.WriteUint16(uint16(len(stamps)))
	required = campaignRequiredStamps(required)

	if len(stamps) >= required && deadline.After(time.Now()) {
		bf.WriteUint16(2)
	} else {
		bf.WriteUint16(0)
	}

	for _, v := range stamps {
		bf.WriteUint32(v)
	}

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfApplyCampaign(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfApplyCampaign)
	if s.server.db == nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	// Check if the code exists, belongs to this campaign, and check if it's a multi-code
	var multi bool
	err := s.server.db.QueryRow(`SELECT multi FROM public.campaign_codes WHERE code = $1 AND campaign_id = $2`, pkt.Code, pkt.CampaignID).Scan(&multi)
	if err != nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	// Check if the code is already used
	var exists bool
	if multi {
		err = s.server.db.QueryRow(`SELECT COUNT(*) > 0 FROM public.campaign_state WHERE code = $1 AND character_id = $2`, pkt.Code, s.charID).Scan(&exists)
	} else {
		err = s.server.db.QueryRow(`SELECT COUNT(*) > 0 FROM public.campaign_state WHERE code = $1`, pkt.Code).Scan(&exists)
	}
	if err != nil || exists {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	_, err = s.server.db.Exec(`INSERT INTO public.campaign_state (code, campaign_id, character_id) VALUES ($1, $2, $3)`, pkt.Code, pkt.CampaignID, s.charID)
	if err != nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfEnumerateItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfEnumerateItem)
	if s.server.db == nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	bf := byteframe.NewByteFrame()

	var stamps, required, rewardType uint16
	err := s.server.db.QueryRow(`SELECT COUNT(*) FROM campaign_state WHERE campaign_id = $1 AND character_id = $2`, pkt.CampaignID, s.charID).Scan(&stamps)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	err = s.server.db.QueryRow(`SELECT stamps, reward_type FROM campaigns WHERE id = $1`, pkt.CampaignID).Scan(&required, &rewardType)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	required = uint16(campaignRequiredStamps(int(required)))

	if stamps >= required {
		var items []CampaignReward
		if rewardType == 2 {
			var exists int
			err = s.server.db.QueryRow(`SELECT COUNT(*) FROM campaign_quest WHERE campaign_id = $1 AND character_id = $2`, pkt.CampaignID, s.charID).Scan(&exists)
			if err != nil {
				doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			if exists > 0 {
				err = s.server.db.Select(&items, `
					SELECT id, item_type, quantity, item_id, TO_TIMESTAMP(0) AS deadline FROM campaign_rewards
					WHERE campaign_id = $1 AND item_type != 9
					AND NOT EXISTS (SELECT 1 FROM campaign_rewards_claimed WHERE reward_id = campaign_rewards.id AND character_id = $2)
				`, pkt.CampaignID, s.charID)
			} else {
				err = s.server.db.Select(&items, `
					SELECT cr.id, cr.item_type, cr.quantity, cr.item_id, COALESCE(c.end_time, TO_TIMESTAMP(0)) AS deadline FROM campaign_rewards cr
					JOIN campaigns c ON cr.campaign_id = c.id
					WHERE campaign_id = $1 AND item_type = 9`, pkt.CampaignID)
			}
		} else {
			err = s.server.db.Select(&items, `
				SELECT id, item_type, quantity, item_id, TO_TIMESTAMP(0) AS deadline FROM campaign_rewards
				WHERE campaign_id = $1
				AND NOT EXISTS (SELECT 1 FROM campaign_rewards_claimed WHERE reward_id = campaign_rewards.id AND character_id = $2)
			`, pkt.CampaignID, s.charID)
		}
		if err != nil {
			doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}

		bf.WriteUint16(uint16(len(items)))
		for _, item := range items {
			bf.WriteUint32(item.ID)
			bf.WriteUint16(item.ItemType)
			bf.WriteUint16(item.Quantity)
			bf.WriteUint16(item.ItemID) //HACK:placed quest id in this field to fit with Item No pattern. however it could be another field... possibly the other unks.
			bf.WriteUint16(0)           //Unk4, gets cast to uint8
			bf.WriteUint32(0)           //Unk5
			bf.WriteUint32(uint32(item.Deadline.Unix()))
		}
		if len(items) == 0 {
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
		} else {
			doAckBufSucceed(s, pkt.AckHandle, bf.Data())
		}
	} else {
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
	}
}

func handleMsgMhfAcquireItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAcquireItem)
	if s.server.db == nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	for _, id := range pkt.RewardIDs {
		_, err := s.server.db.Exec(`INSERT INTO campaign_rewards_claimed (reward_id, character_id) VALUES ($1, $2)`, id, s.charID)
		if err != nil {
			doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfTransferItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfTransferItem)
	if s.server.db == nil {
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	if pkt.ItemType == 9 {
		var campaignID uint32
		err := s.server.db.QueryRow(`
			SELECT ce.campaign_id FROM campaign_rewards ce
			JOIN event_quests eq ON ce.item_id = eq.quest_id
			WHERE eq.id = $1
		`, pkt.QuestID).Scan(&campaignID)
		if err == nil {
			_, err = s.server.db.Exec(`INSERT INTO campaign_quest (campaign_id, character_id) VALUES ($1, $2)`, campaignID, s.charID)
			if err != nil {
				doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
		}
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}
