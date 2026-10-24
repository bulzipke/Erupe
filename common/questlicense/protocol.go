package questlicense

// Staging Event House wire contract for a future license-aware client. The
// ordinary paid-item marker is 0x80000000; its client must never accept this
// different high word as an inventory kit. The low word carries the gold G
// price. These constants do not enable, publish, or route any native campaign.
const (
	NativeProductMarker uint32 = 0x80010000
	NativeProductMask   uint32 = 0xffff0000

	WireOK           uint32 = 0
	WireInsufficient uint32 = 0x514c0001
	WirePriceChanged uint32 = 0x514c0002
	WireAlreadyOwned uint32 = 0x514c0003
	WirePartialOwned uint32 = 0x514c0004
	WireUnavailable  uint32 = 0x514c0005
)
