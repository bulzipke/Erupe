package scenario

import (
	"encoding/binary"
	"fmt"

	"erupe-ce/common/byteframe"
)

// Protbot signs in as ZZ. This matches its expanded receiver contract; it is
// neither a single-ACK format nor support for the older u8 Offset request.
const questEnumerationCapacity = 16384

// Match encodeQuestDeliverySnapshot's bounds on the entire serialized entry,
// including its 22-byte header and Pascal8 notes (not just the quest body).
const (
	questEnumerationMinFrame = 352
	questEnumerationMaxFrame = 896
)

type questEnumeration struct {
	total        int
	pages        int
	payloadBytes int
	questIDs     []uint16
	entries      []byte
}

type questPage struct {
	total    int
	offset   int
	questIDs []uint16
	entries  []byte
}

func enumerateQuestPages(fetch func(uint16) ([]byte, error)) (*questEnumeration, error) {
	result := &questEnumeration{total: -1}
	seen := make(map[uint16]bool)
	for offset := 0; ; {
		data, err := fetch(uint16(offset))
		if err != nil {
			return nil, fmt.Errorf("enumerate quests at offset %d: %w", offset, err)
		}
		page, err := parseQuestPage(data)
		if err != nil {
			return nil, fmt.Errorf("enumerate quests at offset %d: %w", offset, err)
		}
		if page.total > questEnumerationCapacity {
			return nil, fmt.Errorf("quest total %d exceeds receiver capacity %d", page.total, questEnumerationCapacity)
		}
		if result.total < 0 {
			result.total = page.total
		} else if page.total != result.total {
			return nil, fmt.Errorf("quest total changed from %d to %d at offset %d", result.total, page.total, offset)
		}
		if page.offset != offset {
			return nil, fmt.Errorf("quest page offset %d does not match requested %d", page.offset, offset)
		}
		next := offset + len(page.questIDs)
		if next > result.total {
			return nil, fmt.Errorf("quest page returns %d entries at offset %d beyond total %d", len(page.questIDs), offset, result.total)
		}
		if next == offset && offset < result.total {
			return nil, fmt.Errorf("quest enumeration made no progress at offset %d of %d", offset, result.total)
		}
		for _, id := range page.questIDs {
			if seen[id] {
				return nil, fmt.Errorf("duplicate quest ID %d at offset %d", id, offset)
			}
			seen[id] = true
		}
		result.pages++
		result.payloadBytes += len(data)
		result.questIDs = append(result.questIDs, page.questIDs...)
		result.entries = append(result.entries, page.entries...)
		if next == result.total {
			return result, nil
		}
		offset = next
	}
}

// parseQuestPage follows makeEventQuest and handleMsgMhfEnumerateQuest's ZZ
// layout. The DB row ID at entry+0 is distinct from the LE quest ID at body+0x2e.
// Every counted entry and trailing table must fit, and the trailer must end the
// ACK payload exactly; truncated pages must never become successful short lists.
func parseQuestPage(data []byte) (*questPage, error) {
	bf := byteframe.NewByteFrameFromBytes(data)
	returned := int(bf.ReadUint16())
	if returned > questEnumerationCapacity {
		return nil, fmt.Errorf("quest returned count %d exceeds receiver capacity", returned)
	}
	page := &questPage{}
	entryStart := int(bf.Index())
	for i := 0; i < returned; i++ {
		frameStart := int(bf.Index())
		bf.ReadUint32()  // DB row ID, not quest ID.
		bf.ReadBytes(16) // Remaining ZZ header, up to body length.
		bodyLength := bf.ReadUint16()
		body := bf.ReadBytes(uint(bodyLength))
		if bf.Err() != nil || len(body) < 0x30 {
			return nil, fmt.Errorf("quest entry %d has a truncated header/body", i)
		}
		id := binary.LittleEndian.Uint16(body[0x2e:0x30])
		if id == 0 {
			return nil, fmt.Errorf("quest entry %d has zero quest ID", i)
		}
		page.questIDs = append(page.questIDs, id)
		notesLength := bf.ReadUint8() // Pascal8 byte count includes its NUL.
		notes := bf.ReadBytes(uint(notesLength))
		if bf.Err() != nil || len(notes) == 0 || notes[len(notes)-1] != 0 {
			return nil, fmt.Errorf("quest entry %d has invalid notes", i)
		}
		frameLength := int(bf.Index()) - frameStart
		if frameLength < questEnumerationMinFrame || frameLength > questEnumerationMaxFrame {
			return nil, fmt.Errorf("quest entry %d length %d outside %d..%d", i, frameLength, questEnumerationMinFrame, questEnumerationMaxFrame)
		}
	}
	entryEnd := int(bf.Index())
	if bf.Err() != nil {
		return nil, fmt.Errorf("truncated quest page: %w", bf.Err())
	}
	page.entries = data[entryStart:entryEnd]
	bf.ReadUint16() // Tune XOR key.
	tunes := bf.ReadUint16()
	bf.ReadBytes(uint(tunes) * 10)
	items := bf.ReadUint16()
	unknownItems := bf.ReadUint16()
	bets := bf.ReadUint16()
	bf.ReadUint16() // Reserved.
	// The current server emits zero and no such array. Do not invent a
	// nonzero array layout from the reserved field's descriptive comment.
	if unknownItems != 0 {
		return nil, fmt.Errorf("unsupported quest VS array count %d", unknownItems)
	}
	bf.ReadBytes(uint(items)*2 + uint(bets)*8)
	page.total = int(bf.ReadUint16())
	page.offset = int(bf.ReadUint16())
	if bf.Err() != nil {
		return nil, fmt.Errorf("truncated quest tables/trailer: %w", bf.Err())
	}
	if int(bf.Index()) != len(data) {
		return nil, fmt.Errorf("quest page has %d trailing bytes", len(data)-int(bf.Index()))
	}
	return page, nil
}
