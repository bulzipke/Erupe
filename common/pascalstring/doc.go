// Package pascalstring writes length-prefixed, null-terminated strings into a
// ByteFrame. The prefix width is selectable (uint8, uint16, or uint32) and
// strings are optionally encoded to the Korean client's CP949 before writing.
package pascalstring
