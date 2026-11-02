package channelserver

import (
	"bytes"
	"fmt"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func TestFirstIntrusionSeenRetainsOnlyOwnedBit(t *testing.T) {
	for old := 0; old < 256; old++ {
		for next := 0; next < 256; next++ {
			prior := &CharacterSaveData{Mode: cfg.ZZ, decompSave: make([]byte, 300)}
			prior.decompSave[firstIntrusionSeenBlobOffset] = byte(old)
			incoming := &CharacterSaveData{Mode: cfg.ZZ, decompSave: bytes.Repeat([]byte{0xa5}, 300)}
			incoming.decompSave[firstIntrusionSeenBlobOffset] = byte(next)
			before := append([]byte(nil), incoming.decompSave...)
			incoming.preserveFirstIntrusionSeen(prior.firstIntrusionSeen())
			if incoming.firstIntrusionSeen() != ((old|next)&2 != 0) {
				t.Fatal("encounter bit regressed")
			}
			for i := range before {
				mask := byte(0)
				if i == firstIntrusionSeenBlobOffset {
					mask = 2
				}
				if before[i]&^mask != incoming.decompSave[i]&^mask {
					t.Fatal("unowned savedata bit changed")
				}
			}
		}
	}
	for _, mode := range []cfg.Mode{cfg.G10, cfg.Z1, cfg.Z2} {
		save := &CharacterSaveData{Mode: mode, decompSave: make([]byte, 300)}
		save.preserveFirstIntrusionSeen(true)
		if save.firstIntrusionSeen() || !bytes.Equal(save.decompSave, make([]byte, 300)) {
			t.Fatal("non-ZZ layout changed")
		}
	}
}

func TestFirstIntrusionSeenActualSaveAndReconnect(t *testing.T) {
	for _, saveType := range []uint8{0, 1} {
		for _, oldSeen := range []bool{false, true} {
			for _, nextSeen := range []bool{false, true} {
				t.Run(fmt.Sprintf("type%d/old%v/next%v", saveType, oldSeen, nextSeen), func(t *testing.T) {
					server := createMockServer()
					server.erupeConfig.RealClientMode = cfg.ZZ
					repo := newMockCharacterRepo()
					repo.loadSaveDataID, repo.loadSaveDataName = 42, "Hunter"
					repo.strings["name"] = "Hunter"
					old := characterSaveBlob(t, "Hunter")
					if oldSeen {
						old[firstIntrusionSeenBlobOffset] = 2
					}
					repo.loadSaveDataData, _ = nullcomp.Compress(old)
					server.charRepo = repo
					s := createMockSession(42, server)
					s.Name = "Hunter"
					incoming := characterSaveBlob(t, "Hunter")
					incoming[firstIntrusionSeenBlobOffset] = 1 // The separate Tower intro flag.
					if nextSeen {
						incoming[firstIntrusionSeenBlobOffset] |= 2
					}
					payload := incoming
					if saveType == 1 {
						payload = nil
						for pos := 0; pos < len(incoming); {
							n := min(65534, len(incoming)-pos)
							payload = append(payload, 1)
							count := n + 1
							if count < 256 {
								payload = append(payload, byte(count))
							} else {
								payload = append(payload, 0, byte(count>>8), byte(count))
							}
							payload = append(payload, incoming[pos:pos+n]...)
							pos += n
						}
					}
					compressed, err := nullcomp.Compress(payload)
					if err != nil {
						t.Fatal(err)
					}
					handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 8, SaveType: saveType, RawDataPayload: compressed})
					if ack := readAck(t, s); ack.ErrorCode != 0 {
						t.Fatalf("save ACK: %+v", ack)
					}
					if len(repo.saveAtomicParams) != 1 {
						t.Fatal("missing synthetic save")
					}
					repo.loadSaveDataData = repo.saveAtomicParams[0].CompSave
					// Reconnect with a different in-memory session; no local state is carried.
					reconnected := createMockSession(42, server)
					stored, err := GetCharacterSaveData(reconnected, 42)
					if err != nil {
						t.Fatal(err)
					}
					if stored.firstIntrusionSeen() != (oldSeen || nextSeen) {
						t.Fatal("reconnected encounter state lost")
					}
					if stored.decompSave[firstIntrusionSeenBlobOffset]&1 == 0 {
						t.Fatal("Tower intro lost")
					}
				})
			}
		}
	}
}
