package channelserver

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

type auxiliaryQuestBytes struct {
	BytesHex string `json:"bytes_hex"`
}

type auxiliaryQuestText struct {
	Version            int                  `json:"version"`
	ProjectionLanguage string               `json:"projection_language"`
	SomeString         *auxiliaryQuestBytes `json:"some_string,omitempty"`
	QuestTypeString    *auxiliaryQuestBytes `json:"quest_type_string,omitempty"`
}

// ExportQuestJSONPreservingAuxiliaryText explicitly exports the original bytes
// of some_string and quest_type_string alongside their selected-language text
// projection. It does not guess an encoding, translate text or preserve the
// entire binary layout. Existing ParseQuestBinary callers remain unchanged.
// Keep this offline output detached until the quest's release gates are met.
func ExportQuestJSONPreservingAuxiliaryText(data []byte, lang string) ([]byte, error) {
	q, err := ParseQuestBinary(data, lang)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	table := uint64(binary.LittleEndian.Uint32(data[0x30:]))
	if table == 0 {
		return encoded, nil
	}
	if table > uint64(len(data)) || uint64(len(data))-table < 8 {
		return nil, fmt.Errorf("auxiliary text table outside quest binary")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	raw := auxiliaryQuestText{Version: 1, ProjectionLanguage: lang}
	for i, name := range []string{"some_string", "quest_type_string"} {
		pointer := uint64(binary.LittleEndian.Uint32(data[int(table)+4*i:]))
		if pointer == 0 {
			continue
		}
		if pointer >= uint64(len(data)) {
			return nil, fmt.Errorf("%s pointer outside quest binary", name)
		}
		end := bytes.IndexByte(data[int(pointer):], 0)
		if end < 0 {
			return nil, fmt.Errorf("%s has no in-file NUL terminator", name)
		}
		value := &auxiliaryQuestBytes{BytesHex: hex.EncodeToString(data[int(pointer) : int(pointer)+end])}
		text := q.SomeString
		if i == 0 {
			raw.SomeString = value
		} else {
			raw.QuestTypeString = value
			text = q.QuestType
		}
		// Preserve an explicitly present empty string despite legacy omitempty.
		fields[name], err = json.Marshal(text)
		if err != nil {
			return nil, err
		}
	}
	if raw.SomeString == nil && raw.QuestTypeString == nil {
		return encoded, nil
	}
	fields["auxiliary_text_raw"], err = json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// selectedQuestJSONFields applies stricter spelling/duplicate checks only to
// the reserved raw contract and its two corresponding text keys. Other legacy
// top-level keys keep encoding/json's existing behavior.
func selectedQuestJSONFields(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, fmt.Errorf("auxiliary text contract requires a quest JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("invalid quest JSON key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		for _, reserved := range []string{"auxiliary_text_raw", "some_string", "quest_type_string"} {
			if !strings.EqualFold(key, reserved) {
				continue
			}
			if key != reserved {
				return nil, fmt.Errorf("auxiliary text contract requires canonical key %q", reserved)
			}
			if _, exists := fields[key]; exists {
				return nil, fmt.Errorf("duplicate auxiliary text key %q", key)
			}
			fields[key] = value
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing quest JSON after auxiliary text contract")
	}
	return fields, nil
}

func strictAuxiliaryJSONObject(data json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, fmt.Errorf("auxiliary text value must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("invalid auxiliary text object key")
		}
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known {
			return nil, fmt.Errorf("unknown auxiliary text key %q", key)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate auxiliary text key %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing auxiliary text JSON")
	}
	return fields, nil
}

func auxiliaryJSONString(fields map[string]json.RawMessage, name string) (string, error) {
	raw, exists := fields[name]
	if !exists || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '"' {
		return "", fmt.Errorf("auxiliary text requires explicit string %q", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func compileAuxiliaryQuestText(data []byte, q *QuestJSON, lang string) ([2][]byte, bool, error) {
	var values [2][]byte
	if len(q.AuxiliaryTextRaw) == 0 {
		return values, false, nil
	}
	top, err := selectedQuestJSONFields(data)
	if err != nil {
		return values, false, err
	}
	raw, err := strictAuxiliaryJSONObject(top["auxiliary_text_raw"], "version", "projection_language", "some_string", "quest_type_string")
	if err != nil {
		return values, false, err
	}
	var version int
	if err := json.Unmarshal(raw["version"], &version); err != nil || version != 1 {
		return values, false, fmt.Errorf("auxiliary text version must be integer 1")
	}
	projection, err := auxiliaryJSONString(raw, "projection_language")
	if err != nil {
		return values, false, err
	}
	if projection != lang {
		return values, false, fmt.Errorf("auxiliary text projection language differs from compile language")
	}
	preserved := 0
	for i, name := range []string{"some_string", "quest_type_string"} {
		blob, present := raw[name]
		if !present {
			text := []string{q.SomeString, q.QuestType}[i]
			if text != "" {
				if uint64(len(text)) >= math.MaxUint32 {
					return values, false, fmt.Errorf("auxiliary text exceeds u32 layout")
				}
				values[i], err = encodeContentText(text, lang)
				if err != nil {
					return values, false, err
				}
			}
			continue
		}
		pair, err := strictAuxiliaryJSONObject(blob, "bytes_hex")
		if err != nil {
			return values, false, fmt.Errorf("%s: %w", name, err)
		}
		encoded, err := auxiliaryJSONString(pair, "bytes_hex")
		if err != nil {
			return values, false, err
		}
		if len(encoded)%2 != 0 || uint64(len(encoded)/2) >= math.MaxUint32 {
			return values, false, fmt.Errorf("%s raw hex length is invalid or exceeds u32 layout", name)
		}
		original, err := hex.DecodeString(encoded)
		if err != nil {
			return values, false, fmt.Errorf("%s raw hex: %w", name, err)
		}
		if bytes.IndexByte(original, 0) >= 0 {
			return values, false, fmt.Errorf("%s raw bytes contain an embedded NUL", name)
		}
		text, err := auxiliaryJSONString(top, name)
		if err != nil {
			return values, false, err
		}
		decoded, err := decodeContentText(original, projection)
		if err != nil || decoded != text {
			return values, false, fmt.Errorf("%s raw bytes and text projection disagree", name)
		}
		values[i] = append(original, 0)
		preserved++
	}
	if preserved == 0 {
		return values, false, fmt.Errorf("auxiliary text contract requires at least one raw byte field")
	}
	return values, true, nil
}

func checkedQuestU32Size(total, extra uint64) (uint64, error) {
	if total > math.MaxUint32 || extra > math.MaxUint32-total {
		return 0, fmt.Errorf("auxiliary text layout exceeds u32 offsets")
	}
	return total + extra, nil
}

func checkAuxiliaryQuestLayout(start uint64, values [2][]byte, tables []QuestGatheringTableJSON) error {
	total, err := checkedQuestU32Size(start, 8)
	if err != nil {
		return err
	}
	for _, value := range values {
		total, err = checkedQuestU32Size(total, uint64(len(value)))
		if err != nil {
			return err
		}
	}
	if uint64(len(tables)) > math.MaxUint32/4 {
		return fmt.Errorf("gathering table pointer array exceeds u32 layout")
	}
	total, err = checkedQuestU32Size(total, uint64(len(tables))*4)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if uint64(len(table.Items)) > (math.MaxUint32-2)/4 {
			return fmt.Errorf("gathering item array exceeds u32 layout")
		}
		total, err = checkedQuestU32Size(total, uint64(len(table.Items))*4+2)
		if err != nil {
			return err
		}
	}
	return nil
}
