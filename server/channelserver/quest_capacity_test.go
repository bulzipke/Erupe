package channelserver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestExpandedQuestSnapshotCapacityAndU16Counts(t *testing.T) {
	s := deliveryTestSession(t, `[]`)
	rows := make([]EventQuest, cfg.MaximumEventQuestCapacity+1)
	for i := range rows {
		// Virtual source keys avoid real Tower/collaboration eligibility. This
		// count-only fixture does not claim native support for these quest IDs.
		rows[i].QuestID = 100000 + i
	}
	// One shared valid frame is sufficient for the constructor's count bound;
	// native record parsing and real handler pages are tested separately below.
	frame := make([]byte, questDataMinLen)
	encode := func(EventQuest) ([]byte, error) { return frame, nil }
	for _, capacity := range []int{513, 1024, 16384, cfg.MaximumEventQuestCapacity} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			s.server.erupeConfig.EventQuestCapacity = capacity
			for _, count := range []int{capacity - 1, capacity} {
				snapshot, err := s.encodeQuestDeliverySnapshot(rows[:count], deliveryTestTime(), encode)
				if err != nil || snapshot == nil {
					t.Fatalf("count %d: constructor failed: %v", count, err)
				}
				if len(snapshot.entries) != count || snapshot.capacity != capacity || snapshot.contractVersion != questEnumerationContractVersion {
					t.Fatalf("count %d: got %d, capacity %d, contract %d", count, len(snapshot.entries), snapshot.capacity, snapshot.contractVersion)
				}
				bf := byteframe.NewByteFrame()
				total, returned := snapshot.appendPage(bf, uint16(count-1))
				if int(total) != count || returned != 1 || !bytes.Equal(bf.Data(), frame) {
					t.Fatal("last-page count wrapped or bytes changed")
				}
			}
			if got, err := s.encodeQuestDeliverySnapshot(rows[:capacity+1], deliveryTestTime(), encode); err == nil || got != nil || !strings.Contains(err.Error(), "by 1") {
				t.Fatalf("overflow must reject whole list: %v", err)
			}
		})
	}
}

func TestExpandedQuestPagesPreserveEveryIDAndByte(t *testing.T) {
	for _, size := range []int{questDataMinLen, questDataMaxLen} {
		for _, count := range []int{513, 1023, 1024, 1025, 9517, 16384} {
			t.Run(fmt.Sprintf("%d/%d", size, count), func(t *testing.T) {
				s := deliveryTestSession(t, `[]`)
				s.server.erupeConfig.EventQuestCapacity = 16384
				rows := make([]EventQuest, count)
				for i := range rows {
					rows[i].QuestID = i + 100000
				}
				snapshot, err := s.encodeQuestDeliverySnapshot(rows, deliveryTestTime(), func(eq EventQuest) ([]byte, error) {
					entry := bytes.Repeat([]byte{byte(eq.QuestID)}, size)
					binary.BigEndian.PutUint32(entry, uint32(eq.QuestID))
					return entry, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				stored, pages := 0, 0
				for stored < count {
					bf := byteframe.NewByteFrame()
					bf.WriteUint16(0)
					total, returned := snapshot.appendPage(bf, uint16(stored))
					if int(total) != count || returned == 0 || len(bf.Data()) > 60000+size {
						t.Fatal("invalid expanded page bounds")
					}
					for i := 0; i < int(returned); i++ {
						got := bf.Data()[2+i*size : 2+(i+1)*size]
						if !bytes.Equal(got, snapshot.entries[stored+i]) || int(binary.BigEndian.Uint32(got)) != 100000+stored+i {
							t.Fatal("lost, repeated, reordered or changed entry")
						}
					}
					stored += int(returned)
					pages++
					if pages > count {
						t.Fatal("paging did not terminate")
					}
				}
				bf := byteframe.NewByteFrame()
				if total, returned := snapshot.appendPage(bf, uint16(stored)); int(total) != count || returned != 0 || len(bf.Data()) != 0 {
					t.Fatal("completed list repeated its last page")
				}
			})
		}
	}
}

func TestExpandedQuestHandlerFreezesUnmanagedRowsAndFailsOverflow(t *testing.T) {
	s := newEnumerateQuestSession(1025)
	s.server.erupeConfig.EventQuestCapacity = 2048
	repo := &rehearsalListRepo{rows: s.server.eventRepo.(*mockEventRepo).eventQuests}
	s.server.eventRepo = repo
	stored, pages := 0, 0
	var first *questDeliveryListSnapshot
	for stored < 1025 {
		handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: uint32(pages + 1), Offset: uint16(stored)})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 {
			t.Fatal("expanded page failed", ack.ErrorCode)
		}
		payload := ack.Payload
		returned := int(binary.BigEndian.Uint16(payload))
		total := int(binary.BigEndian.Uint16(payload[len(payload)-4:]))
		offset := int(binary.BigEndian.Uint16(payload[len(payload)-2:]))
		if total != 1025 || offset != stored || returned == 0 || returned > 2048-stored {
			t.Fatalf("bad actual ACK: total=%d offset=%d returned=%d stored=%d", total, offset, returned, stored)
		}
		frameOffset := 2
		for i := 0; i < returned; i++ {
			// Parse the existing 22-byte event header and Pascal notes, rather
			// than assuming the ACK trailer starts immediately after its rows.
			if frameOffset+22 > len(payload) || binary.BigEndian.Uint32(payload[frameOffset:]) != uint32(stored+i+1) {
				t.Fatal("actual ACK lost/reordered event row")
			}
			bodyLength := int(binary.BigEndian.Uint16(payload[frameOffset+20:]))
			notes := frameOffset + 22 + bodyLength
			if bodyLength != 600 || notes+2 > len(payload) || payload[notes] != 1 || payload[notes+1] != 0 {
				t.Fatal("actual ACK changed body/notes boundary")
			}
			frameOffset = notes + 2
		}
		stored += returned
		pages++
		if pages == 1 {
			first = s.questDeliveryList
			repo.rows = nil
		}
		if pages > 100 {
			t.Fatal("client loop did not terminate")
		}
	}
	if repo.reads != 1 || repo.writes != 0 || first != s.questDeliveryList || first.capacity != 2048 {
		t.Fatal("expanded legacy enumeration was not frozen/read-only")
	}
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 99, Offset: 0})
	if ack := readAck(t, s); ack.ErrorCode != 0 || binary.BigEndian.Uint16(ack.Payload) != 0 || repo.reads != 2 {
		t.Fatal("offset zero did not refresh")
	}
	overflow := newEnumerateQuestSession(1025)
	overflow.server.erupeConfig.EventQuestCapacity = 1024
	handleMsgMhfEnumerateQuest(overflow, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 100})
	if ack := readAck(t, overflow); ack.ErrorCode == 0 || overflow.questDeliveryList != nil {
		t.Fatal("expanded overflow silently truncated or retained old list")
	}
}

func TestExpandedQuestInvalidRuntimeSettingRejectsCachedPages(t *testing.T) {
	for _, capacity := range []int{65536, 512, 511, -1} {
		s := newEnumerateQuestSession(1)
		s.questDeliveryList = &questDeliveryListSnapshot{entries: [][]byte{make([]byte, 400)}, capacity: 1024, contractVersion: 1}
		s.server.erupeConfig.EventQuestCapacity = capacity
		handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 1, Offset: 1})
		if ack := readAck(t, s); ack.ErrorCode == 0 || s.questDeliveryList != nil {
			t.Fatal("invalid config reused cached page")
		}
		if got, err := s.encodeQuestDeliverySnapshot(nil, deliveryTestTime(), deliveryTestEncode); err == nil || got != nil {
			t.Fatal("builder bypassed invalid config")
		}
	}
}
