package channelserver

import (
	"bytes"
	"encoding/binary"
)

const nativeQuestRecordBytes = 0x4ac

// HuntClientSettings describes the settings frozen at the end of this hunt.
// Used flags also retain options used earlier, even if disabled before clearing.
// Absence is unknown, never inferred as OFF. UI fields are display metadata;
// VUI4 NPC usage governs ranking priority, while VUI3 Support usage governs
// eligibility. Timings use the native final RecordLog frame field.
type HuntClientSettings struct {
	Version      uint8 `json:"version"`
	HPView       bool  `json:"hpView"`
	HPViewUsed   bool  `json:"hpViewUsed"`
	ElapsedMode  uint8 `json:"elapsedMode"`
	ElapsedUsed  bool  `json:"elapsedUsed"`
	FOV          uint8 `json:"fov"`
	FOVUsed      bool  `json:"fovUsed"`
	SupportKnown bool  `json:"supportKnown,omitempty"`
	SupportUsed  bool  `json:"supportUsed"`
	NPCKnown     bool  `json:"npcKnown,omitempty"`
	NPCUsed      bool  `json:"npcUsed"`
	Solo         bool  `json:"solo"` // Filled by the server's quest-party tracking.
}

// decodeHuntClientTrailer validates the complete optional extension, including
// the native quest/time/result echoes. VUI1 is readable for existing metadata
// and HC corroboration, but cannot prove that a main objective was achieved.
func decodeHuntClientTrailer(data []byte) []byte {
	if len(data) != nativeQuestRecordBytes+16 {
		return nil
	}
	tail := data[nativeQuestRecordBytes:]
	allowedFlags := byte(15)
	if string(tail[:4]) == "VUI3" {
		allowedFlags = 31
	}
	if string(tail[:4]) == "VUI4" {
		allowedFlags = 63
	}
	if tail[4]&^allowedFlags != 0 || tail[5] > 2 || tail[6] < 30 || tail[6] > 90 {
		return nil
	}
	switch string(tail[:4]) {
	case "VUI1":
		if tail[7] != 0 || tail[15] != 0 {
			return nil
		}
	case "VUI3", "VUI4":
		if data[killLogHeaderSize+93] != 0 || data[killLogHeaderSize+149] != 0 {
			if tail[7] != 0 || tail[15] != 0 {
				return nil
			}
			break // Raid results retain their native outcome rules.
		}
		fallthrough
	case "VUI2":
		if data[questResultCodeOffset] != questResultCodeCleared || !huntMainObjectiveComplete(tail[7], tail[15]) {
			return nil
		}
	default:
		return nil
	}
	if binary.LittleEndian.Uint32(tail[8:12]) != binary.LittleEndian.Uint32(data[12:16]) ||
		binary.LittleEndian.Uint16(tail[12:14]) != binary.LittleEndian.Uint16(data[:2]) || tail[14] != data[8] {
		return nil
	}
	// All normal/violent and Berserk Raviente aggregation remains unchanged.
	if string(tail[:4]) != "VUI3" && string(tail[:4]) != "VUI4" && (data[killLogHeaderSize+93] != 0 || data[killLogHeaderSize+149] != 0) {
		return nil
	}
	return tail
}

// Native objective modes: alternatives, main + two subs, two mains + one sub,
// or all active objectives. Sub objectives never become a required main target.
func huntMainObjectiveComplete(mode, state byte) bool {
	if state&0xc0 != 0 {
		return false
	}
	active, achieved := state&7, (state>>3)&7
	if achieved&^active != 0 {
		return false
	}
	switch mode {
	case 1:
		return achieved != 0
	case 2:
		return achieved&1 != 0
	case 3:
		return achieved&3 == 3
	case 4:
		return active&^achieved == 0
	default:
		return false
	}
}

func huntRecordMainTargetCleared(data []byte) bool {
	tail := decodeHuntClientTrailer(data)
	return tail != nil && (bytes.Equal(tail[:4], []byte("VUI2")) || bytes.Equal(tail[:4], []byte("VUI3")) || bytes.Equal(tail[:4], []byte("VUI4"))) &&
		huntMainObjectiveComplete(tail[7], tail[15])
}

// huntRecordSupportAllowed requires explicit OFF evidence from the matching
// quest result. Old clients lack this field; never interpret unknown as OFF.
func huntRecordSupportAllowed(data []byte) bool {
	settings := decodeHuntClientSettings(data)
	return settings != nil && settings.SupportKnown && !settings.SupportUsed
}

func decodeHuntClientSettings(data []byte) *HuntClientSettings {
	tail := decodeHuntClientTrailer(data)
	if tail == nil {
		return nil
	}
	return &HuntClientSettings{
		Version: 1, HPView: tail[4]&1 != 0, HPViewUsed: tail[4]&2 != 0,
		ElapsedMode: tail[5], ElapsedUsed: tail[4]&4 != 0,
		FOV: tail[6], FOVUsed: tail[4]&8 != 0,
		SupportKnown: string(tail[:4]) == "VUI3" || string(tail[:4]) == "VUI4", SupportUsed: tail[4]&16 != 0,
		NPCKnown: string(tail[:4]) == "VUI4", NPCUsed: tail[4]&32 != 0,
	}
}
