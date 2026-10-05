package main

import (
	"encoding/binary"
	"strings"
	"testing"
)

func appendShopTable(data []byte, count int, firstID uint16, price uint32) []byte {
	for i := 0; i < count; i++ {
		r := make([]byte, shopRecordSize)
		binary.LittleEndian.PutUint16(r[0:], firstID+uint16(i))
		binary.LittleEndian.PutUint32(r[4:], price)
		r[8], r[9], r[10] = 6, 6, 1
		data = append(data, r...)
	}
	return append(data, make([]byte, 16)...)
}

func TestExtractShopPricesKeepsLowestPrice(t *testing.T) {
	data := make([]byte, 64)
	data = appendShopTable(data, minShopRecords, 1, 100)
	data = appendShopTable(data, minShopRecords, 1, 20)
	data = appendShopTable(data, 10, 5000, 1) // too short to be a shop table

	prices, runs, err := extractShopPrices(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].offset != 64 || runs[0].count != minShopRecords {
		t.Fatalf("runs %v", runs)
	}
	if len(prices) != minShopRecords || prices[1] != 20 {
		t.Fatalf("got %d prices, item 1 = %d", len(prices), prices[1])
	}
	if _, ok := prices[5000]; ok {
		t.Fatal("short run was extracted")
	}

	source, err := generate(prices, runs, "ABC")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "\t0x0001: 20,\n") || !strings.Contains(string(source), "Source SHA-256: ABC") {
		t.Fatalf("unexpected source:\n%s", source[:300])
	}
}

func TestExtractShopPricesRequiresATable(t *testing.T) {
	if _, _, err := extractShopPrices(make([]byte, 1024)); err == nil {
		t.Fatal("expected an error without shop tables")
	}
}
