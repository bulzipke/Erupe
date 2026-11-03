package channelserver

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// Golden identities and caps recovered from mhfsqd's public prayer-bead table.
// In particular, 4 is Wind/Melody, 8 is Cutting, and 19 is Critical; the old
// server labels described unrelated effects for these IDs.
func TestDivaBeadRetailCatalog(t *testing.T) {
	want := []struct {
		id, cap int
		ko      string
	}{
		{1, 3, "폭풍의 기주"}, {3, 1, "단력의 기주"}, {4, 2, "풍운의 기주"},
		{8, 3, "참인의 기주"}, {9, 3, "타명의 기주"}, {10, 3, "탄기의 기주"},
		{11, 3, "변속의 기주"}, {14, 3, "만뢰의 기주"}, {15, 1, "부동의 기주"},
		{17, 3, "결집의 기주"}, {18, 3, "가호의 기주"}, {19, 3, "강격의 기주"},
		{20, 3, "봉화의 기주"}, {21, 3, "봉수의 기주"}, {22, 3, "봉빙의 기주"},
		{23, 3, "봉뢰의 기주"}, {24, 3, "봉룡의 기주"}, {25, 2, "봉속의 기주"},
	}
	if len(divaBeadCatalog) != len(want) {
		t.Fatal("public bead count", len(divaBeadCatalog))
	}
	for _, w := range want {
		b, ok := divaBeadDefinitionFor(w.id)
		if !ok || b.MaxLevel != w.cap || b.KO != w.ko {
			t.Fatalf("effect %d: %+v", w.id, b)
		}
		for _, language := range supportedLangs {
			i := getLangStringsFor(language)
			if len(i.beads) != len(want) || i.beadName(w.id) == "" || i.beadDescription(w.id) == "" {
				t.Fatalf("missing effect %d in %s", w.id, language)
			}
		}
		for _, text := range []string{b.KO, b.DescriptionKO} {
			encoded, _, err := transform.String(korean.EUCKR.NewEncoder(), text)
			if err != nil {
				t.Fatalf("effect %d encoding: %v", w.id, err)
			}
			decoded, _, err := transform.String(korean.EUCKR.NewDecoder(), encoded)
			if err != nil || decoded != text {
				t.Fatalf("effect %d roundtrip: %q", w.id, decoded)
			}
		}
		encodedName, _, _ := transform.String(korean.EUCKR.NewEncoder(), b.KO)
		if len(encodedName)+1 > 32 {
			t.Fatalf("effect %d name overflows native field", w.id)
		}
		if bytes.Count([]byte(b.DescriptionKO), []byte{'\n'}) > 2 {
			t.Fatalf("effect %d description exceeds three lines", w.id)
		}
		for _, line := range bytes.Split([]byte(b.DescriptionKO), []byte{'\n'}) {
			s, _, err := transform.String(korean.EUCKR.NewEncoder(), string(line))
			if err != nil || len(s) > 34 {
				t.Fatalf("effect %d description exceeds 17 fullwidth columns", w.id)
			}
		}
	}
	for _, internal := range []int{0, 2, 5, 6, 7, 12, 13, 16, 26} {
		if _, ok := divaBeadDefinitionFor(internal); ok {
			t.Fatalf("internal/special effect %d exposed as public bead", internal)
		}
	}
}

func TestDivaBeadEveryRetailEffectUsesFourSlotWire(t *testing.T) {
	for start := range divaBeadCatalog {
		srv := createMockServer()
		srv.erupeConfig.Language = "ko"
		ids := make([]int, 4)
		for slot := range ids {
			ids[slot] = divaBeadCatalog[(start+slot)%len(divaBeadCatalog)].ID
		}
		srv.divaRepo = &mockDivaRepo{beads: ids}
		s := createMockSession(1, srv)
		handleMsgMhfGetKijuInfo(s, &mhfpacket.MsgMhfGetKijuInfo{AckHandle: 7})
		ack := readAck(t, s)
		p := ack.Payload
		if !ack.IsBufferResponse || len(p) != 1+4*546 || p[0] != 4 {
			t.Fatalf("wire resized: count/length %x/%d", p[:1], len(p))
		}
		for slot, id := range ids {
			entry := p[1+slot*546 : 1+(slot+1)*546]
			b, _ := divaBeadDefinitionFor(id)
			if !bytes.Equal(entry[:32], stringsupport.PaddedString(b.KO, 32, true)) ||
				!bytes.Equal(entry[32:544], stringsupport.PaddedString(b.DescriptionKO, 512, true)) ||
				entry[544] != byte(slot+1) || entry[545] != byte(id) {
				t.Fatalf("slot %d: effect ID %d confused with slot/color identity", slot+1, id)
			}
		}
		// The existing 22-byte song reply and its four per-effect counters
		// support every public ID without extending the client snapshot.
		p = divaBattleSongPayload(DivaBattleSongState{}, ids)
		if len(p) != 22 {
			t.Fatal("song reply resized", len(p))
		}
		for slot, id := range ids {
			if binary.BigEndian.Uint16(p[10+slot*3:]) != uint16(id) {
				t.Fatalf("song slot %d lost effect %d", slot+1, id)
			}
		}
	}
}

func TestDivaBeadRoundStillLimitedToFour(t *testing.T) {
	if !reflect.DeepEqual(defaultBeadTypes, []int{1, 3, 4, 8}) {
		t.Fatal("existing default quartet changed", defaultBeadTypes)
	}
	srv := createMockServer()
	r := &mockDivaRepo{}
	for _, b := range divaBeadCatalog {
		r.beads = append(r.beads, b.ID)
	}
	srv.divaRepo = r
	s := createMockSession(1, srv)
	handleMsgMhfGetKijuInfo(s, &mhfpacket.MsgMhfGetKijuInfo{AckHandle: 1})
	if p := readAck(t, s).Payload; len(p) != 2185 || p[0] != 4 {
		t.Fatal("more than four beads exposed", len(p))
	}
	handleMsgMhfSetKiju(s, &mhfpacket.MsgMhfSetKiju{AckHandle: 2, Unk1: 5})
	if p := readAck(t, s).Payload; !bytes.Equal(p, []byte{0xf9}) {
		t.Fatalf("fifth slot accepted: %x", p)
	}
}
