package mhfpallone

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"erupe-ce/common/decryption"
)

func clientDraftFixture(t *testing.T) ([]byte, []byte, map[uint16][]byte) {
	t.Helper()
	dat := routeBanksFixture(t)
	binary.LittleEndian.PutUint32(dat[16:], 0xa00)
	binary.LittleEndian.PutUint16(dat[0xd70:], RouteBankCount)
	for star := 0; star < RouteBankCount; star++ {
		count := uint16(1)
		if star == 0 {
			count = 2
		}
		binary.LittleEndian.PutUint16(dat[0xd72+star*2:], count)
	}
	retained := catalogueFixture(t)[0x900:]
	p := len(dat)
	dat = append(dat, retained...)
	binary.LittleEndian.PutUint32(dat[CatalogueHeaderOffset:], uint32(p))
	inf := make([]byte, 0x700)
	put := func(off int, v uint32) { binary.LittleEndian.PutUint32(inf[off:], v) }
	put(0, questInfoMagic)
	put(4, 6)
	put(12, questInfoZZHead)
	put(16, 0x88)
	put(20, 0x90)
	binary.LittleEndian.PutUint16(inf[0x88:], 2)
	binary.LittleEndian.PutUint16(inf[0x90:], 21100)
	binary.LittleEndian.PutUint16(inf[0x92:], 100)
	put(0x94, 0x100)
	binary.LittleEndian.PutUint16(inf[0x98:], 28000) // empty upper catch-all
	put(0x104, 0x300)
	binary.LittleEndian.PutUint16(inf[0x32e:], 21001)
	put(0x328, 0x500)
	for i := 0; i < 8; i++ {
		put(0x500+i*4, 0x530)
	}
	copy(inf[0x530:], "existing\x00")
	source, q, opts := draftFixture()
	body, _, err := BuildQuestDraft(source, q, opts)
	if err != nil {
		t.Fatal(err)
	}
	quests := make(map[uint16][]byte)
	for _, id := range []uint16{27438, 27479, 27348, 27365} {
		b := bytes.Clone(body)
		binary.LittleEndian.PutUint16(b[0x12e:], id)
		quests[id] = b
	}
	return dat, inf, quests
}

func checkClientDraft(t *testing.T, dat, inf []byte, quests map[uint16][]byte, got ClientDraft) {
	t.Helper()
	if got.BankOrdinal == 0 || got.Route.Index != int(got.BankOrdinal) || got.Route.Number != 1 || got.Route.Star != 0 ||
		!reflect.DeepEqual(got.Route.StageQuestIDs, []uint16{27438, 27479, 27348, 27365}) {
		t.Fatalf("wrong binding: %+v", got.Route)
	}
	// Old bytes may change only at table pointers and the relocation count.
	oldDat := bytes.Clone(got.Data[:len(dat)])
	copy(oldDat[RouteBanksHeaderOffset:RouteBanksHeaderOffset+4], dat[RouteBanksHeaderOffset:RouteBanksHeaderOffset+4])
	q := int(binary.LittleEndian.Uint32(dat[16:]))
	if binary.LittleEndian.Uint16(got.Data[q+0x372:]) != got.BankOrdinal+1 {
		t.Fatal("new route would not be relocated by the native loader")
	}
	copy(oldDat[q+0x372:q+0x374], dat[q+0x372:q+0x374])
	oldInf := bytes.Clone(got.Info[:len(inf)])
	copy(oldInf[16:24], inf[16:24])
	if !bytes.Equal(oldDat, dat) || !bytes.Equal(oldInf, inf) {
		t.Fatal("modified original client data outside the table pointers/count")
	}
	before, err := ParseRouteBanks(dat)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ParseRouteBanks(got.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatal("did not append exactly one historical route")
	}
	var existing []Route
	for _, route := range after {
		if route.Star == 0 && route.Index == got.Route.Index {
			if !reflect.DeepEqual(route, got.Route) {
				t.Fatal("wrong appended binding")
			}
		} else {
			existing = append(existing, route)
		}
	}
	if !reflect.DeepEqual(before, existing) {
		t.Fatal("changed an existing active route's metadata or ordinal")
	}
	retained, err := ParseCatalogue(dat)
	if err != nil {
		t.Fatal(err)
	}
	table := int(binary.LittleEndian.Uint32(dat[CatalogueHeaderOffset:]))
	pointers := int(binary.LittleEndian.Uint32(got.Data[RouteBanksHeaderOffset:]))
	bank := int(binary.LittleEndian.Uint32(got.Data[pointers:]))
	for _, route := range []Route{got.Route} {
		found := false
		for _, original := range retained {
			if route.NativeCode == original.NativeCode {
				src, dst := table+original.Index*RouteRecordSize, bank+route.Index*RouteRecordSize
				found = bytes.Equal(dat[src:src+RouteRecordSize], got.Data[dst:dst+RouteRecordSize])
			}
		}
		if !found {
			t.Fatal("changed historical route record bytes", route.Number)
		}
	}
	oldPointers := int(binary.LittleEndian.Uint32(dat[RouteBanksHeaderOffset:]))
	oldBank := int(binary.LittleEndian.Uint32(dat[oldPointers:]))
	n := int(got.BankOrdinal) * RouteRecordSize
	if !bytes.Equal(dat[oldBank:oldBank+n], got.Data[bank:bank+n]) {
		t.Fatal("changed bytes in existing active bank records")
	}
	oldRows, err := clientQuestCategories(inf)
	if err != nil {
		t.Fatal(err)
	}
	newRows, err := clientQuestCategories(got.Info)
	if err != nil || len(newRows) != len(oldRows)+2 {
		t.Fatal("wrong category count", err)
	}
	for _, old := range oldRows {
		found := false
		for _, row := range newRows {
			found = found || old == row
		}
		if !found {
			t.Fatal("changed an existing category", old)
		}
	}
	for id, packed := range quests {
		body, err := decodeContainer(packed)
		if err != nil {
			t.Fatal(err)
		}
		main := int(binary.LittleEndian.Uint32(body))
		want := bytes.Clone(body[main : main+0x140])
		record, err := ClientQuestRecordZZ(got.Info, id)
		if err != nil {
			t.Fatal(err)
		}
		text, err := clientQuestTexts(got.Info, record)
		wantText, wantErr := clientQuestTexts(body, want)
		if err != nil || wantErr != nil || !reflect.DeepEqual(text, wantText) {
			t.Fatal("text bytes lost during relocation", id, err, wantErr)
		}
		copy(want[0x28:0x2c], record[0x28:0x2c])
		if !bytes.Equal(want, record) {
			t.Fatal("ZZ body fields lost during list-record relocation", id)
		}
	}
}

func TestBuildClientDraftPreservesExistingDataAndRelocatesZZRecords(t *testing.T) {
	dat, inf, quests := clientDraftFixture(t)
	datBefore, infBefore := bytes.Clone(dat), bytes.Clone(inf)
	got, err := BuildClientDraft(dat, inf, 1, quests)
	if err != nil {
		t.Fatal(err)
	}
	checkClientDraft(t, dat, inf, quests, got)
	if !bytes.Equal(dat, datBefore) || !bytes.Equal(inf, infBefore) {
		t.Fatal("input slices mutated")
	}
	for _, source := range [][]byte{inf, decryption.PackSimple(inf)} {
		again, err := BuildClientDraft(dat, source, 1, quests)
		if err != nil || !bytes.Equal(got.Info, again.Info) || !bytes.Equal(got.Data, again.Data) {
			t.Fatal("container decoding or map iteration changed deterministic output", err)
		}
	}
	if _, err := BuildClientDraft(got.Data, got.Info, 1, quests); err == nil {
		t.Fatal("replaced already restored quest categories")
	}
	oldRecord, err := ClientQuestRecordZZ(got.Info, 21001)
	if err != nil || !bytes.Equal(oldRecord, inf[0x300:0x440]) {
		t.Fatal("old quest lookup changed", err)
	}
}

func TestBuildClientDraftRefusesBadInputs(t *testing.T) {
	for _, name := range []string{"wrong version", "wrong category order", "missing sentinel", "bad category pointer", "missing body", "wrong embedded ID", "bad text table", "bad text pointer", "unknown route", "relocation count", "relocation pointer"} {
		t.Run(name, func(t *testing.T) {
			dat, inf, quests := clientDraftFixture(t)
			number := uint16(1)
			switch name {
			case "wrong version":
				binary.LittleEndian.PutUint32(inf[12:], 0x78)
			case "wrong category order":
				binary.LittleEndian.PutUint16(inf[0x98:], 21000)
			case "missing sentinel":
				inf[0xa0] = 1
			case "bad category pointer":
				binary.LittleEndian.PutUint32(inf[0x94:], 0xfffffff0)
			case "missing body":
				delete(quests, 27438)
			case "wrong embedded ID":
				quests[27438][0x12e] = 0
			case "bad text table":
				binary.LittleEndian.PutUint32(quests[27438][0x128:], 0xfffffff0)
			case "bad text pointer":
				b := quests[27438]
				binary.LittleEndian.PutUint32(b[binary.LittleEndian.Uint32(b[0x128:]):], 0xfffffff0)
			case "unknown route":
				number = 99
			case "relocation count":
				binary.LittleEndian.PutUint16(dat[0xd72:], 1)
			case "relocation pointer":
				binary.LittleEndian.PutUint32(dat[16:], 0xfffffff0)
			}
			if _, err := BuildClientDraft(dat, inf, number, quests); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

// Optional actual-asset check; no game/client folders are written or launched.
func TestClientDraftRetailAssets(t *testing.T) {
	datPath, infPath, bundle := os.Getenv("PALLONE_CLIENT_DAT"), os.Getenv("PALLONE_CLIENT_INF"), os.Getenv("PALLONE_DRAFT_BUNDLE")
	if datPath == "" || infPath == "" || bundle == "" {
		t.Skip("set PALLONE_CLIENT_DAT, PALLONE_CLIENT_INF and PALLONE_DRAFT_BUNDLE for actual-asset verification")
	}
	read := func(p string) []byte {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	dat, err := DecodeClientData(read(datPath))
	if err != nil {
		t.Fatal(err)
	}
	inf, err := decodeContainer(read(infPath))
	if err != nil {
		t.Fatal(err)
	}
	quests := make(map[uint16][]byte)
	for _, id := range []uint16{27438, 27479, 27348, 27365} {
		quests[id] = read(filepath.Join(bundle, "quests", fmt.Sprintf("%05dd0.bin", id)))
	}
	got, err := BuildClientDraft(dat, inf, 1, quests)
	if err != nil {
		t.Fatal(err)
	}
	checkClientDraft(t, dat, inf, quests, got)
	t.Logf("verified original 001 bank binding and four relocated ZZ records; sizes %d / %d", len(got.Data), len(got.Info))
}
