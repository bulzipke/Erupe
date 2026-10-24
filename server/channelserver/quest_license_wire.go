package channelserver

import "erupe-ce/common/questlicense"

// Map the private repository result to the staging client contract explicitly.
// Unknown, conflicting, expired or unavailable requests never become success.
// There is no native packet registration for this adapter in this stage.
func questLicenseWireStatus(status QuestLicensePurchaseStatus) uint32 {
	switch status {
	case QuestLicensePurchaseOK:
		return questlicense.WireOK
	case QuestLicensePurchaseInsufficient:
		return questlicense.WireInsufficient
	case QuestLicensePurchasePriceChanged:
		return questlicense.WirePriceChanged
	case QuestLicensePurchaseAlreadyOwned:
		return questlicense.WireAlreadyOwned
	case QuestLicensePurchasePartialOwned:
		return questlicense.WirePartialOwned
	default:
		return questlicense.WireUnavailable
	}
}
