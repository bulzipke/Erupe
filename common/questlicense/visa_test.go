package questlicense

import (
	"slices"
	"testing"
)

func TestVisaCatalogIsDisabledApprovedProposalWithoutAllocatedID(t *testing.T) {
	// This number is a test-only argument, never a reserved native campaign ID.
	const testCampaignID = 123
	catalog, err := NewVisaCatalog(testCampaignID)
	if err != nil {
		t.Fatal(err)
	}
	offer, ok := catalog.Offer(testCampaignID)
	if !ok || offer.Enabled || offer.Price != 10 || !offer.StartsAt.IsZero() || !offer.EndsAt.IsZero() ||
		!slices.Equal(offer.LicenseKeys, []string{"visa_veil"}) ||
		!slices.Equal(offer.StarterKeys, []string{"visa_gothic_base_v1"}) {
		t.Fatal("approved draft invented activation or changed its benefit", offer)
	}
	if !slices.Equal(catalog.QuestIDsForOffer(testCampaignID), []int{40081, 40142, 40143, 40161, 40173, 40174, 40201, 40218}) {
		t.Fatal("approved VISA quest coverage changed")
	}
	starter, ok := catalog.Starter("visa_gothic_base_v1")
	if !ok || starter.ItemID != 1859 || starter.Quantity != 1 ||
		!slices.Equal(starter.LicenseKeys, []string{"visa_veil"}) ||
		!slices.Equal(starter.LegacyCampaignIDs, []uint32{492821212}) {
		t.Fatal("starter differs from the approved character policy", starter)
	}
	if digest, ok := catalog.OfferDigest(testCampaignID); !ok || digest == [32]byte{} {
		t.Fatal("disabled proposal has no bound offer digest")
	}
	for _, invalidID := range []uint32{0, 1 << 31} {
		if _, err := NewVisaCatalog(invalidID); err == nil {
			t.Fatal("accepted an invalid provisional campaign ID", invalidID)
		}
	}
	starter.LegacyCampaignIDs[0] = 1
	starter.LicenseKeys[0] = "changed"
	other, err := NewVisaCatalog(testCampaignID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := other.Starter(VisaStarterKey)
	if fresh.LegacyCampaignIDs[0] != VisaLegacyCampaignID || fresh.LicenseKeys[0] != VisaLicenseKey {
		t.Fatal("VISA constructor shares mutable policy data across catalogs")
	}
}
