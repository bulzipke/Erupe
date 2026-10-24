package questlicense

// These values describe the approved VISA bundle and its separate character
// starter. They neither reserve a native campaign ID nor activate a sale.
const (
	VisaLicenseKey       = "visa_veil"
	VisaStarterKey       = "visa_gothic_base_v1"
	VisaPriceGoldG       = uint32(10)
	VisaStarterItemID    = uint16(1859)
	VisaStarterQuantity  = uint16(1)
	VisaLegacyCampaignID = uint32(492821212)
)

// NewVisaCatalog builds a disabled local proposal with no operating dates. The
// caller must independently verify and allocate campaignID; numeric validation
// here does not establish that the native namespace is unused. Existing item or
// quest reward histories are not imported, and no account/character is granted
// anything by constructing the catalog.
func NewVisaCatalog(campaignID uint32) (*Catalog, error) {
	price := VisaPriceGoldG
	return New(Document{
		Version: "visa-approved-20261023-v1",
		Licenses: []Definition{{Key: VisaLicenseKey,
			QuestIDs: []int{40081, 40142, 40143, 40161, 40173, 40174, 40201, 40218}}},
		Products: []Product{{CampaignID: campaignID, Price: &price,
			LicenseKeys: []string{VisaLicenseKey}, StarterKeys: []string{VisaStarterKey}}},
		Starters: []StarterDefinition{{Key: VisaStarterKey, LicenseKeys: []string{VisaLicenseKey},
			ItemID: VisaStarterItemID, Quantity: VisaStarterQuantity,
			LegacyCampaignIDs: []uint32{VisaLegacyCampaignID}}},
	})
}
