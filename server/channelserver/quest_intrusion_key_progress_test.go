package channelserver

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"erupe-ce/server/channelserver/compression/nullcomp"
)

func TestFirstIntrusionOnlyTwoKeyConnections(t *testing.T) {
	s := createMockSession(1, createMockServer())
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	for q := 0; q <= 65535; q++ {
		want := uint16(0)
		switch q {
		case 22037:
			want = 26611
		case 22040:
			want = 26623
		}
		if got := firstIntrusionResultForKeyQuest(uint16(q)); got != want {
			t.Fatalf("key %d result %d want %d", q, got, want)
		}
		if want == 0 {
			continue
		}
		if nativeIntrusionResultForParent(uint16(q)) != 0 {
			t.Fatal("key exception changed the original roster")
		}
		for _, flags := range []byte{0, 8, 0x20, 0x28} {
			got := s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, questRunStagePayload(uint16(q), flags))
			if (flags == 0x20 && got != want) || (flags != 0x20 && got != 0) {
				t.Fatalf("key%d flags%x child%d", q, flags, got)
			}
		}
	}
}

func TestFirstIntrusionKeyReceiptAndExactChild(t *testing.T) {
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, pair := range [][2]uint16{{22037, 26611}, {22040, 26623}} {
		srv := nativeIntrusionTestServer(t, []int{22037, 22040, 26611, 26623}, at, at.Add(time.Hour))
		s := createMockSession(1, srv)
		stage := NewStage(deliveryDepartureTestStage)
		stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(pair[0], 0x20)
		pinDeliveryDepartureParticipant(t, s, stage, at)
		for _, child := range []uint16{26611, 26623} {
			if got := s.allowsQuestDeliveryRecord(nativeIntrusionTestRecord(child)); got != (child == pair[1]) {
				t.Fatalf("key%d child%d accepted%v", pair[0], child, got)
			}
		}
		if sameQuestDeliveryResultID(pair[0], pair[1]) {
			t.Fatal("global result matcher broadened")
		}
	}
}

func TestFirstIntrusionKeyProgressPreservesOnlyParentBit(t *testing.T) {
	for _, c := range []struct {
		parent, child uint16
		bit           uint
	}{{22037, 26611, 12}, {22040, 26623, 11}} {
		s := createMockSession(1, createMockServer())
		s.server.erupeConfig.RealClientMode = cfg.ZZ
		s.questDeliveryReceipt.Store(&questDeliveryReceipt{questID: c.parent, nativeIntrusionResultID: c.child})
		index, mask := int(c.bit/8), byte(1<<uint(c.bit%8))
		for prior := 0; prior < 256; prior++ {
			for incoming := 0; incoming < 256; incoming++ {
				previous := []byte{0xa5, byte(prior), 0x31, 0xf0, 0x11, 0xcc, 0xaa, 0x55}
				flags := []byte{0x5a, byte(incoming), 0x8f, 0x37, 0xef, 0x33, 0x55, 0xaa}
				original := append([]byte(nil), flags...)
				before := append([]byte(nil), previous...)
				s.preserveIntrusionKeyProgress(&CharacterSaveData{CharID: s.charID, KQF: flags}, previous)
				if flags[index]&mask != previous[index]&mask {
					t.Fatal("intrusion changed parent completion")
				}
				for i := range flags {
					m := byte(0)
					if i == index {
						m = mask
					}
					if flags[i]&^m != original[i]&^m {
						t.Fatal("unrelated key progress changed")
					}
				}
				if !bytes.Equal(previous, before) {
					t.Fatal("stored baseline mutated")
				}
			}
		}
	}
}

func TestFirstIntrusionKeyProgressLeavesNormalRunsAlone(t *testing.T) {
	for _, receipt := range []*questDeliveryReceipt{nil, {questID: 22037}, {questID: 22040}, {questID: 22037, nativeIntrusionResultID: 26623}, {questID: 22040, nativeIntrusionResultID: 26611}, {questID: 21093, nativeIntrusionResultID: 26614}} {
		s := createMockSession(1, createMockServer())
		s.server.erupeConfig.RealClientMode = cfg.ZZ
		s.questDeliveryReceipt.Store(receipt)
		flags := []byte{0, 0xff, 0x42, 0, 0, 0, 0, 0}
		want := append([]byte(nil), flags...)
		s.preserveIntrusionKeyProgress(&CharacterSaveData{CharID: s.charID, KQF: flags}, make([]byte, 8))
		if !bytes.Equal(flags, want) {
			t.Fatal("normal or mismatched run lost progress")
		}
	}
}

func TestFirstIntrusionKeyProgressActualFullAndDeltaSave(t *testing.T) {
	for _, c := range []struct {
		parent, child uint16
		bit           uint
	}{{22037, 26611, 12}, {22040, 26623, 11}} {
		for _, saveType := range []uint8{0, 1} {
			for _, hadCompleted := range []bool{false, true} {
				for _, intrusion := range []bool{false, true} {
					t.Run(fmt.Sprintf("key%d/type%d/prior%v/intrusion%v", c.parent, saveType, hadCompleted, intrusion), func(t *testing.T) {
						server := createMockServer()
						server.erupeConfig.RealClientMode = cfg.ZZ
						repo := newMockCharacterRepo()
						repo.loadSaveDataID, repo.loadSaveDataName = 42, "Hunter"
						repo.strings["name"] = "Hunter"
						old := characterSaveBlob(t, "Hunter")
						at := getPointers(cfg.ZZ)[pKQF]
						mask := byte(1 << (c.bit % 8))
						index := int(c.bit / 8)
						if hadCompleted {
							old[at+index] |= mask
						}
						repo.loadSaveDataData, _ = nullcomp.Compress(old)
						server.charRepo = repo
						s := createMockSession(42, server)
						s.Name = "Hunter"
						receipt := &questDeliveryReceipt{questID: c.parent}
						if intrusion {
							receipt.nativeIntrusionResultID = c.child
						}
						s.questDeliveryReceipt.Store(receipt)
						incoming := characterSaveBlob(t, "Hunter")
						for i := 0; i < 8; i++ {
							incoming[at+i] = 0xff
						}
						want := append([]byte(nil), incoming[at:at+8]...)
						if intrusion {
							want[index] = want[index]&^mask | old[at+index]&mask
						}
						payload := incoming
						if saveType == 1 {
							// Valid bounded delta blocks, covering the complete synthetic blob.
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
						handleMsgMhfSavedata(s, &mhfpacket.MsgMhfSavedata{AckHandle: 9, SaveType: saveType, RawDataPayload: compressed})
						if ack := readAck(t, s); ack.ErrorCode != 0 {
							t.Fatalf("save ACK: %+v", ack)
						}
						if len(repo.saveAtomicParams) != 1 {
							t.Fatal("missing atomic synthetic save")
						}
						stored, err := nullcomp.Decompress(repo.saveAtomicParams[0].CompSave)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(stored[at:at+8], want) {
							t.Fatalf("stored KQF %x want %x", stored[at:at+8], want)
						}
					})
				}
			}
		}
	}
}
