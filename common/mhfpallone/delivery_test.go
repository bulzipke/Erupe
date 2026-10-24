package mhfpallone

import (
	"encoding/hex"
	"testing"
)

func TestEncodeRouteDeliveryNativeWords(t *testing.T) {
	got, err := EncodeRouteDelivery(42, []RouteDelivery{{Star: 0, Ordinal: 0}})
	if err != nil {
		t.Fatal(err)
	}
	want := "0000002a00000000000000000000000100000000000000000000000000000d480000000000000000"
	if hex.EncodeToString(got) != want {
		t.Fatalf("delivery = %x", got)
	}
	for _, routes := range [][]RouteDelivery{
		{{Star: 11}}, {{Ordinal: 200}}, {{GroupFlag: 2}}, {{}, {}},
	} {
		if _, err := EncodeRouteDelivery(42, routes); err == nil {
			t.Fatal("accepted invalid delivery", routes)
		}
	}
	rows := make([]RouteDelivery, 13)
	for i := range rows {
		rows[i].Ordinal = uint16(i)
	}
	if _, err := EncodeRouteDelivery(42, rows); err == nil {
		t.Fatal("overran the native per-star sentinel")
	}
}
