package mhfpallone

import (
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"erupe-ce/common/decryption"
)

func catalogueFixture(t *testing.T) []byte {
	t.Helper()
	// Retained ZZ route 001 from mhfdat's 0x878 bank. Only its title
	// pointer is relocated into this small fixture; numeric metadata,
	// stage ordering and 0x10 stage strides are the original bytes.
	record, err := hex.DecodeString(
		"7c472e0031372e004c372e00112701002c010000dc050000720100000a070000000001000000000000001100110000000000803eb81e053e0d020000000000006666663fcdcc4c3f0000803f" +
			"2e6b0000d73b2e0090372e00952b2000576b0000063e2e0031382e0049c71f00d46a0000b54c2e0090372e00ba4c2e00e56a000097382e0090372e00d71a0300" +
			"0000000023372e0023372e0023372e000000000023372e0023372e0023372e000000000023372e0023372e0023372e000000000023372e0023372e0023372e00" +
			"0000000023372e0023372e0023372e000000000023372e0023372e0023372e000000000023372e0023372e0023372e000000000023372e0023372e0023372e00")
	if err != nil {
		t.Fatal(err)
	}
	if len(record) != 268 {
		t.Fatalf("record fixture has %d bytes", len(record))
	}
	data := make([]byte, 0x900+268+16)
	binary.LittleEndian.PutUint32(data, 0x1a66686d)
	binary.LittleEndian.PutUint32(data[0x878:], 0x900)
	copy(data[0x880:], "route\x00")
	copy(data[0x900:], record)
	binary.LittleEndian.PutUint32(data[0x900:], 0x880)
	return data
}

func TestParseRetainedZZCatalogue(t *testing.T) {
	routes, err := ParseCatalogue(catalogueFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []Route{{Index: 0, Star: 0, Number: 1, NativeCode: 10001,
		StageQuestIDs: []uint16{27438, 27479, 27348, 27365}}}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes = %+v, want %+v", routes, want)
	}
}

func TestCatalogueRejectsBrokenPointersAndRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:0x87b] }},
		{"wrong file", func(b []byte) []byte { b[0] = 'p'; return b }},
		{"header points into itself", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x878:], 4); return b }},
		{"overflowing pointer", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x878:], 0xffffffff); return b }},
		{"truncated route", func(b []byte) []byte { return b[:0x950] }},
		{"missing terminator", func(b []byte) []byte { return b[:0x900+268] }},
		{"invalid title pointer", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x900:], uint32(len(b))); return b }},
		{"invalid star", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x90c:], 0x000c2711); return b }},
		{"wrong native code", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x90c:], 0x00010001); return b }},
		{"oversize quest id", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x94c:], 65536); return b }},
		{"stage after sentinel", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x95c:], 0); return b }},
		{"all stage slots occupied", func(b []byte) []byte {
			for p := 0x94c; p < 0xa0c; p += 16 {
				binary.LittleEndian.PutUint32(b[p:], 27438)
			}
			return b
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseCatalogue(tt.mutate(catalogueFixture(t))); err == nil {
				t.Fatal("accepted malformed catalogue")
			}
		})
	}
}

func TestDecodeClientDataContainers(t *testing.T) {
	raw := catalogueFixture(t)
	packed := decryption.PackSimple(raw)
	ecd, err := decryption.EncodeECD(packed, decryption.DefaultECDKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{raw, packed, ecd} {
		got, err := DecodeClientData(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, raw) {
			t.Fatal("decoded catalogue differs")
		}
	}
	for _, data := range [][]byte{nil, raw[:3], ecd[:12], packed[:10]} {
		if _, err := DecodeClientData(data); err == nil {
			t.Fatal("accepted truncated client data")
		}
	}
	bad := append([]byte(nil), packed...)
	binary.LittleEndian.PutUint32(bad[12:], 0xffffffff)
	if _, err := DecodeClientData(bad); err == nil {
		t.Fatal("accepted oversized JPK allocation")
	}
}

func routeBanksFixture(t *testing.T) []byte {
	t.Helper()
	data := make([]byte, 0x1000+12*RouteRecordSize+16)
	binary.LittleEndian.PutUint32(data, clientDataMagic)
	binary.LittleEndian.PutUint32(data[RouteBanksHeaderOffset:], 0x940)
	copy(data[0x980:], "route\x00")
	record := catalogueFixture(t)[0x900 : 0x900+RouteRecordSize]
	pos := 0x1000
	for star := 0; star < RouteBankCount; star++ {
		binary.LittleEndian.PutUint32(data[0x940+star*4:], uint32(pos))
		count := 1
		if star == 0 {
			count = 2
		}
		for i := 0; i < count; i++ {
			copy(data[pos:], record)
			binary.LittleEndian.PutUint32(data[pos:], 0x980)
			binary.LittleEndian.PutUint32(data[pos+12:], uint32(star+1)<<16|uint32(10001+star*1000+i))
			pos += RouteRecordSize
		}
	}
	return data
}

func TestRouteBanksStopAtAdjacentStarWithoutZeroPadding(t *testing.T) {
	routes, err := ParseRouteBanks(routeBanksFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 12 || routes[0].Index != 0 || routes[1].Index != 1 || routes[2].Index != 0 || routes[2].Star != 1 || routes[11].NativeCode != 20001 {
		t.Fatalf("route bank boundary or local index is wrong: %+v", routes)
	}
	if !reflect.DeepEqual(routes[0].StageQuestIDs, []uint16{27438, 27479, 27348, 27365}) {
		t.Fatalf("stage order changed: %+v", routes[0])
	}
}

func TestRouteBanksRejectBrokenData(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:RouteBanksHeaderOffset+3] }},
		{"missing bank pointers", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[RouteBanksHeaderOffset:], 0xffffffff); return b }},
		{"truncated bank pointers", func(b []byte) []byte { return b[:0x940+10*4] }},
		{"invalid bank pointer", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x940:], 0xffffffff); return b }},
		{"empty bank", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x100c:], 0); return b }},
		{"truncated record", func(b []byte) []byte { return b[:0x1080] }},
		{"invalid stage", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x104c:], 70000); return b }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseRouteBanks(tt.mutate(routeBanksFixture(t))); err == nil {
				t.Fatal("accepted malformed route banks")
			}
		})
	}
}

func TestRouteBankCodesDoNotReplaceOrdinalAddresses(t *testing.T) {
	b := routeBanksFixture(t)
	binary.LittleEndian.PutUint32(b[0x100c+RouteRecordSize:], 0x12711)
	binary.LittleEndian.PutUint32(b[0x104c+RouteRecordSize:], 23303)
	routes, err := ParseRouteBanks(b)
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].NativeCode != routes[1].NativeCode || routes[0].Index != 0 || routes[1].Index != 1 || routes[1].StageQuestIDs[0] != 23303 {
		t.Fatal("confused a displayed/native code with a bank ordinal")
	}
}
