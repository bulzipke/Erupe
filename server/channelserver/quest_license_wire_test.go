package channelserver

import (
	"testing"

	"erupe-ce/common/questlicense"
)

func TestQuestLicenseWireDoesNotExposeInternalResultNumbers(t *testing.T) {
	for _, test := range []struct {
		status QuestLicensePurchaseStatus
		wire   uint32
	}{
		{QuestLicensePurchaseOK, 0},
		{QuestLicensePurchaseInsufficient, 0x514c0001},
		{QuestLicensePurchasePriceChanged, 0x514c0002},
		{QuestLicensePurchaseAlreadyOwned, 0x514c0003},
		{QuestLicensePurchasePartialOwned, 0x514c0004},
		{QuestLicensePurchaseUnavailable, 0x514c0005},
		{QuestLicensePurchaseConflict, 0x514c0005},
		{QuestLicensePurchaseStatus(255), 0x514c0005},
	} {
		if got := questLicenseWireStatus(test.status); got != test.wire {
			t.Fatal(test, got)
		}
	}
	if questlicense.NativeProductMarker|10 != 0x8001000a || questlicense.NativeProductMask != 0xffff0000 {
		t.Fatal("typed license marker differs from client staging contract")
	}
}
