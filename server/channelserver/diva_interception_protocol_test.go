package channelserver

import (
	"bytes"
	"reflect"
	"testing"

	"erupe-ce/common/byteframe"
)

func readDivaTacticsQuestPayload(t *testing.T, payload []byte, withTotal bool) (uint32, []uint16) {
	t.Helper()
	bf := byteframe.NewByteFrameFromBytes(payload)
	if status := bf.ReadUint8(); status != 0 {
		t.Fatalf("status = %d", status)
	}
	var total uint32
	if withTotal {
		total = bf.ReadUint32()
	}
	if count := bf.ReadUint8(); count != 255 {
		t.Fatalf("replacement count = %d", count)
	}
	quests := make([]uint16, 255)
	for i := range quests {
		quests[i] = bf.ReadUint16()
	}
	if err := bf.Err(); err != nil {
		t.Fatal(err)
	}
	return total, quests
}

func TestDivaTacticsPointPayloadNativeLayout(t *testing.T) {
	input := []uint16{58128, 58043, 0, 58043, 58044}
	before := append([]uint16(nil), input...)
	get := divaTacticsPointPayload(0x12345678, input)
	add := divaTacticsAddPayload(input)
	if len(get) != 0x204 || len(add) != 0x200 {
		t.Fatalf("native buffer sizes: get=%d add=%d", len(get), len(add))
	}
	if !bytes.Equal(get[:6], []byte{0, 0x12, 0x34, 0x56, 0x78, 255}) ||
		!bytes.Equal(add[:2], []byte{0, 255}) {
		t.Fatal("incorrect native headers or byte order")
	}
	total, questIDs := readDivaTacticsQuestPayload(t, get, true)
	_, addQuestIDs := readDivaTacticsQuestPayload(t, add, false)
	if total != 0x12345678 || !reflect.DeepEqual(questIDs, addQuestIDs) {
		t.Fatal("get/add completed lists differ")
	}
	if !reflect.DeepEqual(questIDs[:3], []uint16{58043, 58044, 58128}) {
		t.Fatalf("completed list is not unique/sorted: %v", questIDs[:4])
	}
	for _, id := range questIDs[3:] {
		if id != 0 {
			t.Fatal("unused quest slot is not zero")
		}
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("serializer changed caller's quest list")
	}
}

func TestDivaTacticsPayloadClearsPreviousRound(t *testing.T) {
	for _, withTotal := range []bool{false, true} {
		var payload []byte
		if withTotal {
			payload = divaTacticsPointPayload(0, nil)
		} else {
			payload = divaTacticsAddPayload(nil)
		}
		_, replacement := readDivaTacticsQuestPayload(t, payload, withTotal)
		// Model the native parser's copy-without-clear behavior.
		var native [255]uint16
		for i := range native {
			native[i] = 58043
		}
		copy(native[:], replacement)
		for _, id := range native {
			if id != 0 {
				t.Fatal("prior round quest survived replacement")
			}
		}
	}
}

func TestDivaTacticsPayloadClampsToNativeCapacity(t *testing.T) {
	quests := make([]uint16, 300)
	for i := range quests {
		quests[i] = uint16(300 - i)
	}
	get := divaTacticsPointPayload(^uint32(0), quests)
	if len(get) != 0x204 {
		t.Fatal("oversized completed list exceeded native response buffer")
	}
	total, ids := readDivaTacticsQuestPayload(t, get, true)
	if total != ^uint32(0) {
		t.Fatal("unsigned total was truncated")
	}
	for i, id := range ids {
		if id != uint16(i+1) {
			t.Fatalf("clamped quest[%d] = %d", i, id)
		}
	}
	if len(divaTacticsAddPayload(quests)) != 0x200 {
		t.Fatal("oversized Add payload")
	}
}
