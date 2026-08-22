package signserver

import "erupe-ce/common/stringsupport"

type smcGroup struct {
	charGroup [][]rune
}

type clientNGWordPart struct {
	value    uint16
	smcIndex int16
}

// normalizeClientNGWordParts mirrors the SMC pass performed by the client
// before it compares text with the nam/msg tables. This matters for the Korean
// client because many CP949 bytes overlap Shift-JIS half-width kana bytes and
// are therefore rewritten by the original Japanese SMC table.
func normalizeClientNGWordParts(parts []uint16, groups []smcGroup) []clientNGWordPart {
	normalized := make([]clientNGWordPart, 0, len(parts))
	for offset := 0; offset < len(parts); {
		groupIndex := int16(0)
		matched := false
		for _, group := range groups {
			for _, variant := range group.charGroup {
				candidate := stringsupport.ToNGWord(string(variant))
				if len(candidate) == 0 || len(candidate) > len(parts)-offset || !ngWordPartsEqual(parts[offset:], candidate) {
					continue
				}

				value := parts[offset]
				if canonical := stringsupport.ToNGWord(string(group.charGroup[0])); len(canonical) > 0 {
					value = canonical[0]
				}
				normalized = append(normalized, clientNGWordPart{value: value, smcIndex: groupIndex})
				offset += len(candidate)
				matched = true
				break
			}
			if matched {
				break
			}
			groupIndex += int16(len(group.charGroup) + 1)
		}
		if matched {
			continue
		}
		normalized = append(normalized, clientNGWordPart{value: parts[offset], smcIndex: -1})
		offset++
	}
	return normalized
}

func ngWordPartsEqual(input, candidate []uint16) bool {
	if len(input) < len(candidate) {
		return false
	}
	for i := range candidate {
		if input[i] != candidate[i] {
			return false
		}
	}
	return true
}
