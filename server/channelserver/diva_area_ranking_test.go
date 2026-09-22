package channelserver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/stringsupport"
)

// The values in these fixtures test the native wire format, not a retail map,
// area threshold table, ranking tie policy, or recovered operational snapshot.
func TestDivaAreaRankingPayload(t *testing.T) {
	rows := []divaAreaRank{
		{GuildID: 11, Name: "한글 수렵단", Areas: 45, Rank: 1},
		{GuildID: 12, Name: "Tied", Areas: 45, Rank: 1},
		{GuildID: 13, Name: "Third", Areas: 42, Rank: 3},
	}
	own := divaAreaRank{GuildID: 99, Name: "Own", Areas: 3, Rank: 125}
	got, err := divaAreaRankingPayload(rows, &own)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4041 || len(got) != divaAreaRankingPayloadBytes || got[40] != 100 {
		t.Fatalf("native framing: size=%d count=%d", len(got), got[40])
	}
	checkRow := func(offset int, row divaAreaRank) {
		t.Helper()
		if binary.BigEndian.Uint32(got[offset:]) != row.Rank ||
			binary.BigEndian.Uint32(got[offset+4:]) != uint32(row.Areas) ||
			!bytes.Equal(got[offset+8:offset+40], stringsupport.PaddedString(row.Name, 32, true)) {
			t.Fatalf("incorrect row at %d: %x", offset, got[offset:offset+40])
		}
	}
	checkRow(0, own)
	for i, row := range rows {
		checkRow(41+i*40, row)
	}
	if !bytes.Equal(got[41+len(rows)*40:], make([]byte, (100-len(rows))*40)) {
		t.Fatal("unused native cached rows were not cleared")
	}
	if rows[0].Rank != 1 || rows[1].Rank != 1 || rows[2].Rank != 3 {
		t.Fatal("codec mutated supplied ranks")
	}
}

func TestDivaAreaRankingEmptyAndFull(t *testing.T) {
	empty, err := divaAreaRankingPayload(nil, nil)
	if err != nil || len(empty) != divaAreaRankingPayloadBytes || empty[40] != 100 {
		t.Fatalf("empty cache clear failed: %v", err)
	}
	for i, b := range empty {
		if i != 40 && b != 0 {
			t.Fatalf("fabricated empty rank at %d", i)
		}
	}
	if !bytes.Equal(empty, divaEmptyTacticsRankingPayload()) {
		t.Fatal("existing empty handler wrapper changed the protocol")
	}
	rows := make([]divaAreaRank, 100)
	for i := range rows {
		rows[i] = divaAreaRank{GuildID: uint32(i + 1), Name: fmt.Sprintf("Guild%d", i+1), Areas: int64(100 - i), Rank: uint32(i + 1)}
	}
	got, err := divaAreaRankingPayload(rows, &rows[99])
	if err != nil || len(got) != 4041 {
		t.Fatalf("full table: %v", err)
	}
	if binary.BigEndian.Uint32(got[4001:4005]) != 100 || binary.BigEndian.Uint32(got[4005:4009]) != 1 || got[4040] != 0 {
		t.Fatal("last row overflow or truncation")
	}
	if !bytes.Equal(got[:40], got[4001:4041]) {
		t.Fatal("own guild inside top100 differs from its published row")
	}
}

func TestDivaAreaRankingUnrankedAndDisplayBounds(t *testing.T) {
	own := divaAreaRank{GuildID: 1, Name: "Unranked"}
	got, err := divaAreaRankingPayload(nil, &own)
	if err != nil || !bytes.Equal(got[:8], make([]byte, 8)) || string(got[8:16]) != own.Name {
		t.Fatalf("named but unranked own guild: %v", err)
	}
	row := divaAreaRank{GuildID: 1, Name: strings.Repeat("a", 31), Areas: 0x7fffffff, Rank: 0x7fffffff}
	got, err = divaAreaRankingPayload([]divaAreaRank{row}, nil)
	if err != nil || binary.BigEndian.Uint32(got[41:45]) != 0x7fffffff || got[80] != 0 {
		t.Fatalf("valid signed display/name boundary: %v", err)
	}
	row.Name = strings.Repeat("가", 15) + "A" // Exactly 31 CP949 bytes plus NUL.
	if _, err := divaAreaRankingPayload([]divaAreaRank{row}, nil); err != nil {
		t.Fatalf("31-byte Korean name: %v", err)
	}
}

func TestDivaAreaRankingRejectsInvalidRows(t *testing.T) {
	valid := divaAreaRank{GuildID: 1, Name: "Guild", Areas: 12, Rank: 1}
	for _, tt := range []struct {
		name   string
		mutate func(*divaAreaRank)
	}{
		{"no guild", func(r *divaAreaRank) { r.GuildID = 0 }},
		{"no name", func(r *divaAreaRank) { r.Name = "" }},
		{"embedded nul", func(r *divaAreaRank) { r.Name = "G\x00uild" }},
		{"control", func(r *divaAreaRank) { r.Name = "G\nuild" }},
		{"unencodable", func(r *divaAreaRank) { r.Name = "Guild😀" }},
		{"ascii too long", func(r *divaAreaRank) { r.Name = strings.Repeat("a", 32) }},
		{"double byte cut", func(r *divaAreaRank) { r.Name = strings.Repeat("가", 16) }},
		{"negative areas", func(r *divaAreaRank) { r.Areas = -1 }},
		{"area overflow", func(r *divaAreaRank) { r.Areas = 0x80000000 }},
		{"rank overflow", func(r *divaAreaRank) { r.Rank = 0x80000000 }},
		{"unranked areas", func(r *divaAreaRank) { r.Rank = 0 }},
		{"ranked no areas", func(r *divaAreaRank) { r.Areas = 0 }},
		{"unranked top row", func(r *divaAreaRank) { r.Areas, r.Rank = 0, 0 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := valid
			tt.mutate(&row)
			if got, err := divaAreaRankingPayload([]divaAreaRank{row}, nil); err == nil || got != nil {
				t.Fatalf("invalid row emitted a payload: err=%v", err)
			}
		})
	}
	if got, err := divaAreaRankingPayload(make([]divaAreaRank, 101), nil); err == nil || got != nil {
		t.Fatal("accepted more than 100 rows")
	}
	if got, err := divaAreaRankingPayload([]divaAreaRank{valid, valid}, nil); err == nil || got != nil {
		t.Fatal("accepted duplicate guild")
	}
	own := valid
	own.Areas++
	if got, err := divaAreaRankingPayload([]divaAreaRank{valid}, &own); err == nil || got != nil {
		t.Fatal("accepted own block from a different snapshot")
	}
	if got, err := divaAreaRankingPayload(nil, &divaAreaRank{}); err == nil || got != nil {
		t.Fatal("accepted invalid own row; no guild must use nil")
	}
}

func TestDivaAreaRankingCutoff(t *testing.T) {
	localTime := func(day, hour int) time.Time {
		return time.Date(2026, 9, day, hour, 0, 0, 0, divaLocation)
	}
	start, end := localTime(16, 16), localTime(23, 12)
	// The synthetic 13:00 instant exercises an explicit tally boundary. It is
	// not a claim that retail interception final rankings publish at 13:00.
	finalPublication := localTime(23, 13)
	for _, tt := range []struct {
		name      string
		now, want time.Time
	}{
		{"before window", start.Add(-time.Hour), start},
		{"start", start, start},
		{"no prayer 18 exception", localTime(16, 18), start},
		{"first 20 before", localTime(16, 20).Add(-time.Nanosecond), start},
		{"first 20 exact", localTime(16, 20), localTime(16, 20)},
		{"midnight", localTime(17, 0), localTime(16, 20)},
		{"04 before", localTime(17, 4).Add(-time.Nanosecond), localTime(16, 20)},
		{"04 exact", localTime(17, 4), localTime(17, 4)},
		{"12 exact", localTime(17, 12), localTime(17, 12)},
		{"ordinary 18", localTime(17, 18), localTime(17, 12)},
		{"20 UTC input", localTime(17, 20).UTC(), localTime(17, 20)},
		{"end before", end.Add(-time.Nanosecond), localTime(23, 4)},
		{"end exact withheld", end, localTime(23, 4)},
		{"final before withheld", finalPublication.Add(-time.Nanosecond), localTime(23, 4)},
		{"final exact", finalPublication, end},
		{"after final", finalPublication.Add(30 * 24 * time.Hour), end},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := divaAreaRankingCutoff(tt.now, start, end, finalPublication)
			if err != nil || !got.Equal(tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	// First-day 04/12 publications are normal when the actual window starts
	// early; the prayer rule must not suppress them.
	earlyStart := localTime(16, 0)
	if got, err := divaAreaRankingCutoff(localTime(16, 4), earlyStart, end, finalPublication); err != nil || !got.Equal(localTime(16, 4)) {
		t.Fatalf("first-day 04:00 was suppressed: %v %v", got, err)
	}
	if got, err := divaAreaRankingCutoff(end, start, end, end); err != nil || !got.Equal(end) {
		t.Fatalf("explicit zero tally interval rejected: %v %v", got, err)
	}
}

func TestDivaAreaRankingCutoffRejectsInvalidWindow(t *testing.T) {
	start := time.Date(2026, 9, 16, 16, 0, 0, 0, divaLocation)
	end := start.Add(24 * time.Hour)
	final := end.Add(time.Hour)
	for _, times := range [][4]time.Time{
		{time.Time{}, start, end, final},
		{start, time.Time{}, end, final},
		{start, start, time.Time{}, final},
		{start, start, end, time.Time{}},
		{start, start, start, final},
		{start, start, start.Add(-time.Hour), final},
		{start, start, end, end.Add(-time.Nanosecond)},
	} {
		if got, err := divaAreaRankingCutoff(times[0], times[1], times[2], times[3]); err == nil || !got.IsZero() {
			t.Fatalf("invalid window produced cutoff %v", got)
		}
	}
}
