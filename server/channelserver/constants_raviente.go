package channelserver

// Raviente register type IDs (used in MsgSysLoadRegister / MsgSysNotifyRegister)
const (
	raviRegisterState   = uint32(0x40000)
	raviRegisterSupport = uint32(0x50000)
	raviRegisterGeneral = uint32(0x60000)
)

// ZZ writes the siege type to general register 10 during gathering setup
// (mhfo-hd.dll FUN_11557550, register descriptor 0x40).
const (
	raviGeneralSiegeType        = 10
	raviSiegeTypeExtreme        = 3
	raviSiegeTypeExtremeLimited = 4
)

// Raviente semaphore constants
const (
	raviSemaphoreStride = 0x10000     // ID spacing between hs_l0* semaphores
	raviSemaphoreMax    = uint16(127) // max players per Raviente semaphore
)
