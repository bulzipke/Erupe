package channelserver

import (
	"bytes"
	"encoding/binary"
)

const nativeQuestRecordBytes = 0x4ac

// HuntClientSettings describes the settings frozen at the end of this hunt.
// Used flags also retain options used earlier, even if disabled before clearing.
// Absence is unknown, never inferred as OFF. FOV and elapsed mode are metadata;
// HP display, VUI4 NPC usage and VUI3 Support usage govern ranking priority.
// Timings use the native final RecordLog frame field.
type HuntClientSettings struct {
	Version        uint8          `json:"version"`
	HPView         bool           `json:"hpView"`
	HPViewUsed     bool           `json:"hpViewUsed"`
	ElapsedMode    uint8          `json:"elapsedMode"`
	ElapsedUsed    bool           `json:"elapsedUsed"`
	FOV            uint8          `json:"fov"`
	FOVUsed        bool           `json:"fovUsed"`
	SupportKnown   bool           `json:"supportKnown,omitempty"`
	SupportUsed    bool           `json:"supportUsed"`
	NPCKnown       bool           `json:"npcKnown,omitempty"`
	NPCUsed        bool           `json:"npcUsed"`
	Solo           bool           `json:"solo"`                     // Filled by the server's quest-party tracking.
	FeatureEnabled bool           `json:"featureEnabled,omitempty"` // Local FEATURE selected at departure.
	DivaBeads      *HuntDivaBeads `json:"divaBeads,omitempty"`      // Server-captured departure snapshot; never client supplied.
}

// HuntDivaBeads stays with the saved time after the activation or event ends.
type HuntDivaBeads struct {
	Round  uint32   `json:"round"`
	Beads  []string `json:"beads"`
	Levels []int    `json:"levels,omitempty"`
}

// decodeHuntClientTrailer validates the complete optional extension, including
// the native quest/time/result echoes. VUI1 is readable for existing metadata
// and HC corroboration, but cannot prove that a main objective was achieved.
func decodeHuntClientTrailer(data []byte) []byte {
	if len(data) != nativeQuestRecordBytes+16 {
		return nil
	}
	tail := data[nativeQuestRecordBytes:]
	version := string(tail[:4])
	raid := data[killLogHeaderSize+93] != 0 || data[killLogHeaderSize+149] != 0
	allowedFlags := byte(15)
	switch version {
	case "VUI3":
		allowedFlags = 31
	case "VUI4":
		allowedFlags = 63
	case "VUI5":
		if tail[4]&64 == 0 {
			return nil
		}
		allowedFlags = 127
		if raid {
			allowedFlags = 95 // Same raid flags as VUI3, plus FEATURE.
		}
	}
	if tail[4]&^allowedFlags != 0 || tail[5] > 2 || tail[6] < 30 || tail[6] > 90 {
		return nil
	}
	switch version {
	case "VUI1":
		if tail[7] != 0 || tail[15] != 0 {
			return nil
		}
	case "VUI3", "VUI4", "VUI5":
		if raid {
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
	if raid && version != "VUI3" && version != "VUI4" && version != "VUI5" {
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
	return tail != nil && (bytes.Equal(tail[:4], []byte("VUI2")) || bytes.Equal(tail[:4], []byte("VUI3")) || bytes.Equal(tail[:4], []byte("VUI4")) || bytes.Equal(tail[:4], []byte("VUI5"))) &&
		huntMainObjectiveComplete(tail[7], tail[15])
}

// huntRecordSupportKnown requires bound Support evidence from the matching
// quest result. Both ON and OFF are accepted; usage affects ranking priority.
// Old clients lack this field; never interpret unknown as OFF.
func huntRecordSupportKnown(data []byte) bool {
	settings := decodeHuntClientSettings(data)
	return settings != nil && settings.SupportKnown
}

func decodeHuntClientSettings(data []byte) *HuntClientSettings {
	tail := decodeHuntClientTrailer(data)
	if tail == nil {
		return nil
	}
	version := string(tail[:4])
	raid := data[killLogHeaderSize+93] != 0 || data[killLogHeaderSize+149] != 0
	return &HuntClientSettings{
		Version: 1, HPView: tail[4]&1 != 0, HPViewUsed: tail[4]&2 != 0,
		ElapsedMode: tail[5], ElapsedUsed: tail[4]&4 != 0,
		FOV: tail[6], FOVUsed: tail[4]&8 != 0,
		SupportKnown: version == "VUI3" || version == "VUI4" || version == "VUI5", SupportUsed: tail[4]&16 != 0,
		NPCKnown: version == "VUI4" || (version == "VUI5" && !raid), NPCUsed: tail[4]&32 != 0,
		FeatureEnabled: version == "VUI5",
	}
}
