package scenario

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"erupe-ce/cmd/protbot/protocol"
	"erupe-ce/common/byteframe"
)

// Fixtures encode the server's ZZ framing independently of the decoder. Row IDs
// deliberately differ from body quest IDs; notes and trailing tables vary.
func questPageFixture(total, offset int, ids ...uint16) []byte {
	return questPageFixtureWithBody(total, offset, 352, ids...)
}

func questPageFixtureWithBody(total, offset, bodyLength int, ids ...uint16) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint16(uint16(len(ids)))
	for _, id := range ids {
		bf.WriteUint32(uint32(id) + 100000)
		bf.WriteUint32(0)
		bf.WriteUint8(0)
		bf.WriteUint8(4)
		bf.WriteUint8(18)
		bf.WriteBool(true)
		bf.WriteUint16(0)
		bf.WriteUint32(123)
		bf.WriteUint16(0)
		body := make([]byte, bodyLength)
		binary.LittleEndian.PutUint16(body[0x2e:], id)
		bf.WriteUint16(uint16(len(body)))
		bf.WriteBytes(body)
		bf.WriteUint8(5)
		bf.WriteBytes([]byte{'n', 'o', 't', 'e', 0})
	}
	bf.WriteUint16(77)
	bf.WriteUint16(2)
	bf.WriteBytes(make([]byte, 20))
	bf.WriteUint16(2)
	bf.WriteUint16(0)
	bf.WriteUint16(1)
	bf.WriteUint16(0)
	bf.WriteBytes(make([]byte, 4+8))
	bf.WriteUint16(uint16(total))
	bf.WriteUint16(uint16(offset))
	return bf.Data()
}

func TestQuestEnumerationAllPages(t *testing.T) {
	pages := map[uint16][]byte{
		0: questPageFixture(5, 0, 50, 2),
		2: questPageFixture(5, 2, 65535),
		3: questPageFixture(5, 3, 4, 31),
	}
	var offsets []uint16
	result, err := enumerateQuestPages(func(offset uint16) ([]byte, error) {
		offsets = append(offsets, offset)
		p, ok := pages[offset]
		if !ok {
			return nil, fmt.Errorf("unexpected offset %d", offset)
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(offsets) != "[0 2 3]" || result.total != 5 || result.pages != 3 || fmt.Sprint(result.questIDs) != "[50 2 65535 4 31]" {
		t.Fatalf("incomplete enumeration: offsets=%v result=%+v", offsets, result)
	}
	// Each frame is 22 header + 352 body + 6 notes bytes. No page tables leak.
	if len(result.entries) != 5*380 || result.payloadBytes != len(pages[0])+len(pages[2])+len(pages[3]) {
		t.Fatalf("wrong byte accounting: %+v", result)
	}
	var expected []byte
	for _, offset := range []uint16{0, 2, 3} {
		page := pages[offset]
		count := int(binary.BigEndian.Uint16(page))
		expected = append(expected, page[2:2+count*380]...)
	}
	if !bytes.Equal(result.entries, expected) {
		t.Fatal("returned entry bytes or ordering changed")
	}
}

func TestQuestEnumerationCapacity16384(t *testing.T) {
	for _, total := range []int{0, 1, 16383, 16384} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			requests := 0
			result, err := enumerateQuestPages(func(offset uint16) ([]byte, error) {
				requests++
				end := min(int(offset)+150, total)
				ids := make([]uint16, end-int(offset))
				for i := range ids {
					ids[i] = offset + uint16(i) + 1
				}
				return questPageFixture(total, int(offset), ids...), nil
			})
			if err != nil || result.total != total || len(result.questIDs) != total || requests != max(1, (total+149)/150) {
				t.Fatalf("total=%d result=%+v requests=%d err=%v", total, result, requests, err)
			}
		})
	}
	if result, err := enumerateQuestPages(func(uint16) ([]byte, error) {
		return questPageFixture(16385, 0, 1), nil
	}); err == nil || result != nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("over-capacity accepted: result=%v err=%v", result, err)
	}
}

func TestQuestPageEntryLengthBounds(t *testing.T) {
	for _, frameLength := range []int{76, 351, 352, 896, 897} {
		t.Run(fmt.Sprint(frameLength), func(t *testing.T) {
			_, err := parseQuestPage(questPageFixtureWithBody(1, 0, frameLength-22-6, 55))
			valid := frameLength >= 352 && frameLength <= 896
			if (err == nil) != valid {
				t.Fatalf("frame length=%d valid=%v err=%v", frameLength, valid, err)
			}
		})
	}
}

func TestQuestPageExtendedPayloadTables(t *testing.T) {
	ids := make([]uint16, 154)
	for i := range ids {
		ids[i] = uint16(i + 1)
	}
	page := questPageFixture(len(ids), 0, ids...)
	tuneCountOffset := 2 + len(ids)*380 + 2
	large := append([]byte{}, page[:tuneCountOffset]...)
	large = binary.BigEndian.AppendUint16(large, 770)
	large = append(large, make([]byte, 770*10)...)
	large = append(large, page[tuneCountOffset+2+20:]...)
	if len(large) <= 65535 {
		t.Fatal("fixture does not require extended ACK payload length")
	}
	if parsed, err := parseQuestPage(large); err != nil || len(parsed.questIDs) != len(ids) {
		t.Fatalf("extended quest payload rejected: %v", err)
	}
}

func TestQuestEnumerationRejectsInconsistentPages(t *testing.T) {
	tests := []struct {
		name  string
		pages [][]byte
		want  string
	}{
		{"duplicate within page", [][]byte{questPageFixture(2, 0, 9, 9)}, "duplicate quest ID"},
		{"duplicate across pages", [][]byte{questPageFixture(2, 0, 9), questPageFixture(2, 1, 9)}, "duplicate quest ID"},
		{"first offset", [][]byte{questPageFixture(1, 1, 9)}, "offset"},
		{"repeated offset", [][]byte{questPageFixture(2, 0, 9), questPageFixture(2, 0, 10)}, "offset"},
		{"changed total", [][]byte{questPageFixture(3, 0, 9), questPageFixture(2, 1, 10)}, "total changed"},
		{"zero progress", [][]byte{questPageFixture(2, 0, 9), questPageFixture(2, 1)}, "no progress"},
		{"return exceeds total", [][]byte{questPageFixture(1, 0, 9, 10)}, "beyond total"},
		{"nonempty zero total", [][]byte{questPageFixture(0, 0, 9)}, "beyond total"},
		{"zero quest ID", [][]byte{questPageFixture(1, 0, 0)}, "zero quest ID"},
		{"early end", [][]byte{questPageFixture(2, 0, 9)}, "EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := 0
			result, err := enumerateQuestPages(func(uint16) ([]byte, error) {
				if i == len(tt.pages) {
					return nil, io.EOF
				}
				p := tt.pages[i]
				i++
				return p, nil
			})
			if result != nil || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("result=%v err=%v; want %q", result, err, tt.want)
			}
		})
	}
}

func TestQuestPageRejectsTruncationAndMalformedCounts(t *testing.T) {
	good := questPageFixture(1, 0, 5)
	for cut := range len(good) {
		if _, err := parseQuestPage(good[:cut]); err == nil {
			t.Fatalf("accepted truncated payload length %d/%d", cut, len(good))
		}
	}
	cases := map[string][]byte{}
	for _, name := range []string{"returned count", "body length", "notes length", "notes terminator", "tune count", "VS count", "unsupported VS array", "trailing byte"} {
		cases[name] = bytes.Clone(good)
	}
	binary.BigEndian.PutUint16(cases["returned count"], 2)
	binary.BigEndian.PutUint16(cases["body length"][22:], 65535)
	cases["notes length"][376] = 255
	cases["notes terminator"][381] = 1
	binary.BigEndian.PutUint16(cases["tune count"][384:], 65535)
	binary.BigEndian.PutUint16(cases["VS count"][406:], 65535)
	binary.BigEndian.PutUint16(cases["unsupported VS array"][408:], 1)
	cases["trailing byte"] = append(cases["trailing byte"], 0)
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseQuestPage(data); err == nil {
				t.Fatal("accepted malformed payload")
			}
		})
	}
}

type mockQuestRequester struct {
	next    uint32
	request func(uint32, []byte, time.Duration) (*protocol.AckResponse, error)
}

func (m *mockQuestRequester) NextAckHandle() uint32 { m.next++; return m.next }
func (m *mockQuestRequester) RequestQuestPage(ack uint32, packet []byte, timeout time.Duration) (*protocol.AckResponse, error) {
	return m.request(ack, packet, timeout)
}

func TestQuestEnumerationTransportRequestsOffsets(t *testing.T) {
	var offsets []uint16
	m := &mockQuestRequester{request: func(ack uint32, packet []byte, timeout time.Duration) (*protocol.AckResponse, error) {
		offset := binary.BigEndian.Uint16(packet[10:12])
		offsets = append(offsets, offset)
		if binary.BigEndian.Uint32(packet[2:6]) != ack || packet[7] != 2 || binary.BigEndian.Uint16(packet[8:10]) != 7 || timeout != 15*time.Second {
			t.Fatal("request fields changed")
		}
		return &protocol.AckResponse{AckHandle: ack, IsBufferResponse: true, Data: questPageFixture(2, int(offset), offset+11)}, nil
	}}
	result, err := enumerateQuests(m, 2, 7)
	if err != nil || result.pages != 2 || fmt.Sprint(offsets) != "[0 1]" || m.next != 2 {
		t.Fatalf("offsets=%v result=%v err=%v", offsets, result, err)
	}
}

func TestQuestEnumerationRejectsBadAck(t *testing.T) {
	for _, name := range []string{"nil", "handle", "error", "simple", "transport", "empty"} {
		t.Run(name, func(t *testing.T) {
			m := &mockQuestRequester{request: func(ack uint32, _ []byte, _ time.Duration) (*protocol.AckResponse, error) {
				r := &protocol.AckResponse{AckHandle: ack, IsBufferResponse: true, Data: questPageFixture(0, 0)}
				switch name {
				case "nil":
					return nil, nil
				case "handle":
					r.AckHandle++
				case "error":
					r.ErrorCode = 1
				case "simple":
					r.IsBufferResponse = false
				case "transport":
					return nil, errors.New("mock transport closed")
				case "empty":
					r.Data = nil
				}
				return r, nil
			}}
			if result, err := enumerateQuests(m, 0, 0); result != nil || err == nil {
				t.Fatalf("accepted bad ACK: %v %v", result, err)
			}
		})
	}
}
