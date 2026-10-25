package channelserver

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestHunterGemPointTuneWire(t *testing.T) {
	var fixtures []struct {
		Multiplier       float32 `json:"multiplier"`
		Percent          uint16  `json:"percent"`
		Key              uint16  `json:"key"`
		Count            int     `json:"count"`
		NativeRecordsHex string  `json:"native_records_hex"`
	}
	var baseline map[uint16]uint16
	for _, multiplier := range []float32{1, 0, 0.2, 1.5, 2.5, 5, 10, 100} {
		t.Run(fmt.Sprint(multiplier), func(t *testing.T) {
			s := newEnumerateQuestSession(0)
			s.server.erupeConfig.RealClientMode = cfg.ZZ
			s.server.erupeConfig.GameplayOptions.HunterGemPointMultiplier = multiplier
			s.server.erupeConfig.GameplayOptions.MezFesRandomMinigame = true
			s.mezFesMinigame = 2
			handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 1})
			payload := readAck(t, s).Payload
			key := binary.BigEndian.Uint16(payload[2:])
			count := int(binary.BigEndian.Uint16(payload[4:]))
			if count > tuneLimitZZ || len(payload) < 6+count*10 {
				t.Fatalf("invalid tune count %d", count)
			}
			ordinary := make(map[uint16]uint16)
			native := make([]byte, count*10)
			found := 0
			for i := 0; i < count; i++ {
				row := payload[6+i*10 : 6+(i+1)*10]
				id, value := binary.BigEndian.Uint16(row)^key, binary.BigEndian.Uint16(row[8:])^key
				// The native deserializer stores the wire words little-endian.
				for j := 0; j < 10; j += 2 {
					binary.LittleEndian.PutUint16(native[i*10+j:], binary.BigEndian.Uint16(row[j:]))
				}
				if id == hunterGemPointTuneID {
					found++
					if value != multiplierToTuneValue(multiplier) {
						t.Fatalf("wire percent = %d, multiplier = %v", value, multiplier)
					}
				} else {
					ordinary[id] = value
				}
			}
			if found != 1 || ordinary[mezFesMinigameTuneID] != 2 {
				t.Fatalf("custom tunes missing or duplicated: Hunter Gem=%d MezFes=%d", found, ordinary[mezFesMinigameTuneID])
			}
			if _, present := ordinary[1062]; present {
				t.Fatal("quest multiplier must not publish the native coin-conversion tune")
			}
			if baseline == nil {
				baseline = ordinary
			} else {
				if len(ordinary) != len(baseline) {
					t.Fatal("existing tune entries were truncated")
				}
				for id, value := range baseline {
					if got, ok := ordinary[id]; !ok || got != value {
						t.Fatalf("unrelated tune %d changed", id)
					}
				}
			}
			fixtures = append(fixtures, struct {
				Multiplier       float32 `json:"multiplier"`
				Percent          uint16  `json:"percent"`
				Key              uint16  `json:"key"`
				Count            int     `json:"count"`
				NativeRecordsHex string  `json:"native_records_hex"`
			}{multiplier, multiplierToTuneValue(multiplier), key, count, hex.EncodeToString(native)})
		})
	}
	// Optional cross-client regression input; callers keep this outside the repo.
	if path := os.Getenv("HUNTER_GEM_WIRE_FIXTURE"); path != "" {
		data, err := json.MarshalIndent(fixtures, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHunterGemPointTuneDoesNotTargetOlderClients(t *testing.T) {
	s := newEnumerateQuestSession(0)
	s.server.erupeConfig.RealClientMode = cfg.Z2
	s.server.erupeConfig.GameplayOptions.HunterGemPointMultiplier = 1
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 1})
	payload := readAck(t, s).Payload
	key := binary.BigEndian.Uint16(payload[2:])
	count := int(binary.BigEndian.Uint16(payload[4:]))
	for i := 0; i < count; i++ {
		if binary.BigEndian.Uint16(payload[6+i*10:])^key == hunterGemPointTuneID {
			t.Fatal("unverified client received the custom Hunter Gem tune")
		}
	}
}
