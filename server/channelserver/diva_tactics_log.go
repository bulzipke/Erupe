package channelserver

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

const divaTacticsLogMaxRows = 200

// Server-custom presentation milestones, not a recovered retail reward table.
// They never change personal points, area progress, or reward eligibility.
const divaTacticsLogPersonalStep = 10000

type divaTacticsLogEntry struct {
	Kind   uint8
	Branch uint8
	CharID uint32
	Name   string
	Value  uint32
	At     time.Time
}

type DivaTacticsLogRepository interface {
	GetDivaTacticsLog(charID uint32, now time.Time) ([]divaTacticsLogEntry, error)
}

// Names are presentation-only snapshots of the current character name. The
// native renderer formats its completed line a second time as a printf format,
// so neither percent signs nor client text-control syntax may reach that line.
func divaTacticsLogName(name string) string {
	var result strings.Builder
	used := 0
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune("%~{}", r) {
			continue
		}
		part := stringsupport.UTF8ToSJIS(string(r))
		if len(part) == 0 || stringsupport.SJISToUTF8Lossy(part) != string(r) {
			continue
		}
		if used+len(part) > 31 {
			break
		}
		result.WriteRune(r)
		used += len(part)
	}
	if result.Len() == 0 {
		return "헌터"
	}
	return result.String()
}

// Native 11536f50 reads a u8 count and 46 bytes per row, then clears all unused
// cache entries up to 200. No success byte or fixed 200-row padding is present.
func divaTacticsLogPayload(rows []divaTacticsLogEntry) ([]byte, error) {
	if len(rows) > divaTacticsLogMaxRows {
		return nil, fmt.Errorf("too many diva tactics log rows")
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(uint8(len(rows)))
	for _, row := range rows {
		switch row.Kind {
		case 0, 1, 2, 3, 4, 5, 6:
		default:
			return nil, fmt.Errorf("unsupported diva log kind")
		}
		if row.At.Unix() <= 0 || row.At.Unix() > math.MaxUint32 || row.Value > math.MaxInt32 {
			return nil, fmt.Errorf("diva log value exceeds native range")
		}
		if (row.Kind == 6) != (row.Branch > 0) {
			return nil, fmt.Errorf("invalid diva log branch")
		}
		if row.Kind == 0 || row.Kind == 2 || row.Kind == 6 {
			if row.CharID == 0 || row.Name == "" || row.Name != divaTacticsLogName(row.Name) || row.Value == 0 {
				return nil, fmt.Errorf("invalid diva log hunter")
			}
		} else if row.CharID != 0 || row.Name != "" || (row.Kind != 1 && row.Value != 0) {
			return nil, fmt.Errorf("invalid diva log system entry")
		}
		name := stringsupport.PaddedString(row.Name, 32, true)
		if stringsupport.SJISToUTF8Lossy(bytes.TrimRight(name, "\x00")) != row.Name {
			return nil, fmt.Errorf("invalid diva log name encoding")
		}
		bf.WriteUint8(row.Kind)
		bf.WriteUint8(row.Branch)
		bf.WriteUint32(row.CharID)
		bf.WriteBytes(name)
		bf.WriteUint32(row.Value)
		bf.WriteUint32(uint32(row.At.Unix()))
	}
	return bf.Data(), nil
}

// Input is newest first so the cap retains the most recent actual events.
// The native UI renders server order and opens at the bottom: send the retained
// events oldest first so its initial view contains the latest events. Insert
// UTC+9 date headers after reversing; headers still count toward the 200-row cap.
func divaTacticsLogWithDates(rows []divaTacticsLogEntry) []divaTacticsLogEntry {
	selected, count := 0, 0
	var previousDay int64 = -1
	for _, row := range rows {
		day := (row.At.Unix() + 9*60*60) / (24 * 60 * 60)
		needed := 1
		if day != previousDay {
			needed++
		}
		if count+needed > divaTacticsLogMaxRows {
			break
		}
		selected++
		count += needed
		previousDay = day
	}
	result := make([]divaTacticsLogEntry, 0, count)
	previousDay = -1
	for i := selected - 1; i >= 0; i-- {
		row := rows[i]
		day := (row.At.Unix() + 9*60*60) / (24 * 60 * 60)
		if day != previousDay {
			result = append(result, divaTacticsLogEntry{Kind: 5, At: row.At})
			previousDay = day
		}
		result = append(result, row)
	}
	return result
}

func handleDivaTacticsLog(s *Session, ack uint32) {
	var rows []divaTacticsLogEntry
	var err error
	options := s.server.erupeConfig
	if options.RealClientMode == cfg.ZZ && options.DebugOptions.DivaOverride != 0 && options.DebugOptions.InGameTimeOverrideHour == nil && s.charID != 0 {
		if repo, ok := s.server.divaRepo.(DivaTacticsLogRepository); ok {
			if _, err = s.divaEvent(); err == nil {
				rows, err = repo.GetDivaTacticsLog(s.charID, time.Time{})
			}
		}
	}
	if err != nil {
		s.logger.Warn("Failed to read Diva tactics log", zap.Error(err))
		rows = nil
	}
	data, err := divaTacticsLogPayload(rows)
	if err != nil {
		s.logger.Warn("Rejected unsafe Diva tactics log", zap.Error(err))
		data = []byte{0}
	}
	// Empty success also clears an old guild's cached log after a transfer.
	doAckBufSucceed(s, ack, data)
}
