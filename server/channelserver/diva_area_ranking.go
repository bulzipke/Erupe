package channelserver

import (
	"bytes"
	"fmt"
	"time"
	"unicode"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
)

const (
	divaAreaRankingRows         = 100
	divaAreaRankingNameBytes    = 32
	divaAreaRankingRowBytes     = 40
	divaAreaRankingPayloadBytes = 41 + divaAreaRankingRows*divaAreaRankingRowBytes
)

// divaAreaRank is an already ranked, published snapshot of actual area awards.
// Areas must not be derived from prayer points or completed-quest counts.
// Rank is supplied by the area ledger; this codec does not invent a tie rule.
type divaAreaRank struct {
	GuildID uint32
	Name    string
	Areas   int64
	Rank    uint32
}

func validateDivaAreaRank(row divaAreaRank, own bool) error {
	if row.GuildID == 0 || row.Name == "" {
		return fmt.Errorf("area ranking requires a guild ID and name")
	}
	// Native drawing formats both values as signed decimal integers.
	if row.Areas < 0 || row.Areas > 0x7fffffff || row.Rank > 0x7fffffff {
		return fmt.Errorf("area ranking exceeds native signed display range")
	}
	if (row.Rank == 0) != (row.Areas == 0) || (!own && row.Rank == 0) {
		return fmt.Errorf("area ranking has inconsistent rank and area count")
	}
	for _, r := range row.Name {
		if unicode.IsControl(r) {
			return fmt.Errorf("area ranking guild name contains a control character")
		}
	}
	// PaddedString uses this server's CP949 encoder. Reject a lossy conversion
	// or truncation, including a cut double-byte character, before serializing.
	name := stringsupport.PaddedString(row.Name, divaAreaRankingNameBytes, true)
	if stringsupport.SJISToUTF8Lossy(bytes.TrimRight(name, "\x00")) != row.Name {
		return fmt.Errorf("area ranking guild name does not fit CP949 name[32]")
	}
	return nil
}

// divaAreaRankingPayload implements ZZ 0x11536d30: own rank/areas/name[32],
// then count and count x (rank/areas/name[32]), all integers big-endian.
// 0x107a80b0 scans every cached row, so always overwrite all 100 slots.
// The own block is separate: a guild outside the top 100 must not replace a row.
// nil own means no guild; a named own guild with rank/areas 0 means unranked.
// Rows and own must come from the same published snapshot. No sorting or rank
// calculation is performed, and invalid input produces no partial payload.
func divaAreaRankingPayload(rows []divaAreaRank, own *divaAreaRank) ([]byte, error) {
	if len(rows) > divaAreaRankingRows {
		return nil, fmt.Errorf("area ranking has more than %d rows", divaAreaRankingRows)
	}
	if own != nil {
		if err := validateDivaAreaRank(*own, true); err != nil {
			return nil, fmt.Errorf("own guild: %w", err)
		}
	}
	seen := make(map[uint32]struct{}, len(rows))
	for i, row := range rows {
		if err := validateDivaAreaRank(row, false); err != nil {
			return nil, fmt.Errorf("area ranking row %d: %w", i, err)
		}
		if _, ok := seen[row.GuildID]; ok {
			return nil, fmt.Errorf("area ranking repeats guild %d", row.GuildID)
		}
		seen[row.GuildID] = struct{}{}
		if own != nil && row.GuildID == own.GuildID && row != *own {
			return nil, fmt.Errorf("own guild disagrees with area ranking snapshot")
		}
	}
	bf := byteframe.NewByteFrame()
	writeRow := func(row divaAreaRank) {
		bf.WriteUint32(row.Rank)
		bf.WriteUint32(uint32(row.Areas))
		bf.WriteBytes(stringsupport.PaddedString(row.Name, divaAreaRankingNameBytes, true))
	}
	if own == nil {
		writeRow(divaAreaRank{})
	} else {
		writeRow(*own)
	}
	bf.WriteUint8(divaAreaRankingRows)
	for i := 0; i < divaAreaRankingRows; i++ {
		if i < len(rows) {
			writeRow(rows[i])
		} else {
			writeRow(divaAreaRank{})
		}
	}
	return bf.Data(), nil
}

// divaAreaRankingCutoff selects the latest 04:00/12:00/20:00 UTC+9
// publication during [start,end). Unlike prayer rankings, interception has no
// first-day 18:00 exception. The caller must supply the actual interception
// window and verified final-publication instant; no unverified tally duration
// is built into this helper. Until that instant, the last regular publication
// remains visible, even if end itself falls on a regular publication boundary.
// A start cutoff represents an empty snapshot before the first publication.
// The repository defines inclusion at the boundary. Custom-v1 includes area
// awards timestamped at cutoff, after settling reports from before cutoff.
func divaAreaRankingCutoff(now, start, end, finalPublication time.Time) (time.Time, error) {
	if now.IsZero() || start.IsZero() || end.IsZero() || finalPublication.IsZero() ||
		!end.After(start) || finalPublication.Before(end) {
		return time.Time{}, fmt.Errorf("invalid area ranking publication window")
	}
	if !now.Before(finalPublication) {
		return end, nil
	}
	if !now.After(start) {
		return start, nil
	}
	if !now.Before(end) {
		now = end.Add(-time.Nanosecond)
	}
	local := now.In(divaLocation)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, divaLocation)
	cutoff := midnight.Add(-4 * time.Hour)
	for _, hour := range [...]int{4, 12, 20} {
		candidate := midnight.Add(time.Duration(hour) * time.Hour)
		if !candidate.After(now) {
			cutoff = candidate
		}
	}
	if cutoff.Before(start) {
		return start, nil
	}
	return cutoff, nil
}
