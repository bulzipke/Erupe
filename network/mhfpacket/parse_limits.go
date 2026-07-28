package mhfpacket

// Client-originated batches that trigger one or more database operations per
// entry are intentionally bounded. Normal game requests contain only a small
// number of entries; this prevents a syntactically valid packet from turning
// into tens of thousands of synchronous database calls.
const maxClientBatchEntries = 256

// Terminal logs are diagnostic and may legitimately be larger than gameplay
// batches, but must not permit a single packet to emit tens of thousands of
// synchronous log records.
const maxTerminalLogEntries = 1024
