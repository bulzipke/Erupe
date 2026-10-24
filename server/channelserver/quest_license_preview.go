package channelserver

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"erupe-ce/common/byteframe"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const (
	questLicensePreviewMagic         uint32 = 0x4c505631 // LPV1
	questLicensePreviewVersion       uint16 = 1
	questLicensePreviewAccountFlag   uint16 = 1 << 0
	questLicensePreviewForeverFlag   uint16 = 1 << 1
	questLicensePreviewMaxBytes             = 0x4000
	questLicensePreviewFixedBytes           = 40 // Header, including title length.
	questLicensePreviewRowFixedBytes        = 8  // ID plus three string lengths.
	questLicensePreviewMaxRows              = (questLicensePreviewMaxBytes - questLicensePreviewFixedBytes) / questLicensePreviewRowFixedBytes
)

type QuestLicenseOwnership uint8

const (
	QuestLicenseOwnershipNone QuestLicenseOwnership = iota
	QuestLicenseOwnershipOwned
	QuestLicenseOwnershipPartial
)

// QuestLicensePreview contains trusted server display data, not request fields.
// Ownership is advisory display state; the purchase transaction checks it again.
// A payment code in an owned/partial preview does not authorize another debit.
type QuestLicensePreview struct {
	Quote     QuestLicenseQuote
	Title     string
	Ownership QuestLicenseOwnership
	Quests    []QuestLicensePreviewQuest
}

type QuestLicensePreviewQuest struct {
	ID          int
	Name        string
	Conditions  string
	Publication string
}

type questLicensePreviewText struct {
	name, conditions, publication []byte
}

// encodeQuestLicensePreview stages a typed, noncollectible LPV1 document. It is
// deliberately not registered with a packet handler or any existing KPV1 client.
// Quest IDs are IDs, never indexes into an inventory item-name table.
//
// All integers are big-endian. The exact wire layout is:
//
//	0: magic[4]="LPV1"; 4: version:u16=1; 6: flags:u16=account|permanent;
//	8: ownership:u8; 9: reserved:u8=0; 10: campaign_id:u32;
//	14: price:u16; 16: quest_count:u16; 18: expires_unix_seconds:u32;
//	22: payment_code[16]; 38: title_length:u16; 40: title CP949 bytes;
//	then quest_count rows: ID:u16 and name/conditions/publication, each encoded
//	as length:u16 followed by exactly that many CP949 bytes, with no terminator.
//
// Title/name are required; conditions/publication may be empty when not supplied.
// Their encoded byte limits are respectively 96/120/512/512. Every row and quote
// ID must be the same strictly ascending list. Total output is at most 16 KiB;
// invalid input returns nil rather than a truncated/partly serialized contract.
// Expiry is bounded to Unix seconds 1..MaxUint32 and rounded down on the wire.
// Checking that it is still in the future belongs to the server quote manager,
// not this deterministic serializer. This is a staging protocol, not a released
// client/server capability or a native inventory-grant document.
func encodeQuestLicensePreview(preview QuestLicensePreview) ([]byte, error) {
	if len(preview.Quote.Starters) > 0 {
		return encodeQuestLicenseStarterPreview(preview)
	}
	quote := preview.Quote
	if quote.CampaignID == 0 || quote.CampaignID > math.MaxInt32 ||
		quote.Price == 0 || quote.Price > math.MaxUint16 || !validQuestLicensePaymentCode(quote.PaymentCode) {
		return nil, fmt.Errorf("invalid quest license preview quote")
	}
	codePrice, err := strconv.ParseUint(quote.PaymentCode[1:5], 16, 16)
	if err != nil || codePrice != uint64(quote.Price) {
		return nil, fmt.Errorf("quest license preview price differs from quote")
	}
	expiresUTC := quote.ExpiresAt.UTC()
	if quote.ExpiresAt.IsZero() || expiresUTC.Year() < 1970 || expiresUTC.Year() > 2106 ||
		quote.ExpiresAt.Unix() < 1 || quote.ExpiresAt.Unix() > math.MaxUint32 {
		return nil, fmt.Errorf("invalid quest license preview expiry")
	}
	if preview.Ownership > QuestLicenseOwnershipPartial || len(preview.Quests) == 0 ||
		len(preview.Quests) > questLicensePreviewMaxRows || len(preview.Quests) != len(quote.QuestIDs) {
		return nil, fmt.Errorf("invalid quest license preview ownership or count")
	}
	title, err := questLicensePreviewEncodeText(preview.Title, 96, true)
	if err != nil {
		return nil, fmt.Errorf("quest license preview title: %w", err)
	}
	size := questLicensePreviewFixedBytes + len(title)
	texts := make([]questLicensePreviewText, len(preview.Quests))
	previous := 0
	for i, quest := range preview.Quests {
		if quest.ID < 1 || quest.ID > math.MaxUint16 || quest.ID <= previous || quest.ID != quote.QuestIDs[i] {
			return nil, fmt.Errorf("quest license preview rows differ from quote")
		}
		previous = quest.ID
		texts[i].name, err = questLicensePreviewEncodeText(quest.Name, 120, true)
		if err != nil {
			return nil, fmt.Errorf("quest license preview quest %d name: %w", quest.ID, err)
		}
		texts[i].conditions, err = questLicensePreviewEncodeText(quest.Conditions, 512, false)
		if err != nil {
			return nil, fmt.Errorf("quest license preview quest %d conditions: %w", quest.ID, err)
		}
		texts[i].publication, err = questLicensePreviewEncodeText(quest.Publication, 512, false)
		if err != nil {
			return nil, fmt.Errorf("quest license preview quest %d publication: %w", quest.ID, err)
		}
		size += questLicensePreviewRowFixedBytes + len(texts[i].name) + len(texts[i].conditions) + len(texts[i].publication)
		if size > questLicensePreviewMaxBytes {
			return nil, fmt.Errorf("quest license preview exceeds reply capacity")
		}
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint32(questLicensePreviewMagic)
	bf.WriteUint16(questLicensePreviewVersion)
	bf.WriteUint16(questLicensePreviewAccountFlag | questLicensePreviewForeverFlag)
	bf.WriteUint8(uint8(preview.Ownership))
	bf.WriteUint8(0)
	bf.WriteUint32(quote.CampaignID)
	bf.WriteUint16(uint16(quote.Price))
	bf.WriteUint16(uint16(len(preview.Quests)))
	bf.WriteUint32(uint32(quote.ExpiresAt.Unix()))
	bf.WriteBytes([]byte(quote.PaymentCode))
	writeText := func(value []byte) {
		bf.WriteUint16(uint16(len(value)))
		bf.WriteBytes(value)
	}
	writeText(title)
	for i, quest := range preview.Quests {
		bf.WriteUint16(uint16(quest.ID))
		writeText(texts[i].name)
		writeText(texts[i].conditions)
		writeText(texts[i].publication)
	}
	return bf.Data(), nil
}

var questLicenseStarterPreviewKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// LPV2 keeps the LPV1 header and quest rows, with magic="LPV2", version=2,
// then appends a starter_count:u16 followed by starter rows:
// scope:u8=1 (once per character), reserved:u8=0, item_type:u16=7,
// item_id:u16, quantity:u16, key_length:u16, key ASCII bytes.
// The item is a collectible starting material, not a quest completion reward.
// A client must support LPV2 explicitly; an LPV1 preview never silently omits
// an included material. No released client/packet path consumes either format.
func encodeQuestLicenseStarterPreview(preview QuestLicensePreview) ([]byte, error) {
	starters := preview.Quote.Starters
	if len(starters) > 64 {
		return nil, fmt.Errorf("too many quest license preview starters")
	}
	previous := ""
	extra := 2
	for _, starter := range starters {
		if !questLicenseStarterPreviewKey.MatchString(starter.Key) || starter.Key <= previous || starter.ItemID == 0 || starter.Quantity == 0 {
			return nil, fmt.Errorf("invalid quest license preview starter")
		}
		previous = starter.Key
		extra += 10 + len(starter.Key)
	}
	preview.Quote.Starters = nil
	base, err := encodeQuestLicensePreview(preview)
	if err != nil {
		return nil, err
	}
	if len(base)+extra > questLicensePreviewMaxBytes {
		return nil, fmt.Errorf("quest license starter preview exceeds reply capacity")
	}
	bf := byteframe.NewByteFrame()
	bf.WriteBytes(base)
	bf.WriteUint16(uint16(len(starters)))
	for _, starter := range starters {
		bf.WriteUint8(1)
		bf.WriteUint8(0)
		bf.WriteUint16(7)
		bf.WriteUint16(starter.ItemID)
		bf.WriteUint16(starter.Quantity)
		bf.WriteUint16(uint16(len(starter.Key)))
		bf.WriteBytes([]byte(starter.Key))
	}
	data := bf.Data()
	copy(data[:4], "LPV2")
	data[4], data[5] = 0, 2
	return data, nil
}

func questLicensePreviewEncodeText(value string, maxBytes int, required bool) ([]byte, error) {
	// Every CP949 character is in the BMP and uses at most three UTF-8 bytes.
	// Reject plainly oversized input before allocating an encoded copy.
	if len(value) > maxBytes*3 {
		return nil, fmt.Errorf("text exceeds encoded byte limit")
	}
	if required && strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("required text is empty")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return nil, fmt.Errorf("text contains control characters")
		}
	}
	encoded, _, err := transform.String(korean.EUCKR.NewEncoder(), value)
	if err != nil {
		return nil, fmt.Errorf("text is not representable in CP949")
	}
	if len(encoded) > maxBytes {
		return nil, fmt.Errorf("text exceeds encoded byte limit")
	}
	decoded, _, err := transform.String(korean.EUCKR.NewDecoder(), encoded)
	if err != nil || decoded != value {
		return nil, fmt.Errorf("text does not roundtrip through CP949")
	}
	return []byte(encoded), nil
}
