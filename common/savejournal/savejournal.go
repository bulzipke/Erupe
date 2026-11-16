// Package savejournal keeps an append-only JSON Lines record of what every
// character save changed: the ones a client uploads and the ones the server
// writes itself.
//
// A character's savedata is a blob the client uploads and the server stores,
// so an edited client can upload any state it likes. The journal does not
// judge a save; it only preserves the evidence a later review needs: the exact
// bytes that changed, the few values the server can already name, and what the
// session did since its previous save.
package savejournal

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	// FileName is the live journal file inside the journal directory. Rotated
	// files keep the same stem with a timestamp.
	FileName = "savediff.jsonl"
	// RecordVersion is written to every record so a reader can tell layouts
	// apart after the format changes. Version 2 added server-written saves
	// (kind "server"), item totals, and stopped counting the packets that
	// accompany every save.
	RecordVersion = 2
	// DefaultByteBudget bounds the old+new bytes one record may carry.
	DefaultByteBudget = 64 << 10

	// Two changed runs closer than this are written as one range, so a value
	// whose bytes changed only in part still reads as one contiguous field.
	mergeGap = 8
	// A blob that differs almost everywhere is not reviewable byte by byte.
	maxRanges = 4096
	// Events kept per window when a session goes a long time without saving.
	maxEvents = 128
)

// Options configures the journal file. Directory may be empty, in which case
// the journal is written beside the server log.
type Options struct {
	Enabled    bool
	Directory  string
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

// DefaultOptions keeps roughly half a year of saves.
func DefaultOptions() Options {
	return Options{Enabled: true, MaxSizeMB: 50, MaxBackups: 200, MaxAgeDays: 180, Compress: true}
}

// Validate rejects settings that would silently keep nothing.
func (o Options) Validate() error {
	if !o.Enabled {
		return nil
	}
	if o.MaxSizeMB < 1 || o.MaxSizeMB > 1024 || o.MaxBackups < 1 ||
		o.MaxAgeDays < 1 || o.MaxAgeDays > 36500 {
		return fmt.Errorf("SaveJournal requires MaxSizeMB 1..1024, MaxBackups >= 1 and MaxAgeDays 1..36500")
	}
	return nil
}

// Journal serializes records from every channel into one file. A nil Journal
// is a disabled journal: every method is a no-op.
type Journal struct {
	mu  sync.Mutex
	out io.WriteCloser
}

// Open creates the journal file. It returns (nil, nil) when the journal is
// disabled. logDirectory is used when Options.Directory is empty.
func Open(o Options, logDirectory string) (*Journal, error) {
	if !o.Enabled {
		return nil, nil
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	dir := o.Directory
	if dir == "" {
		dir = logDirectory
	}
	if dir == "" {
		dir = "logs"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create save journal directory: %w", err)
	}
	file := &lumberjack.Logger{Filename: filepath.Join(dir, FileName),
		MaxSize: o.MaxSizeMB, MaxBackups: o.MaxBackups, MaxAge: o.MaxAgeDays,
		Compress: o.Compress}
	// Open now so a permissions or disk problem is a startup error instead of
	// a journal that quietly records nothing.
	if _, err := file.Write(nil); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("open save journal: %w", err)
	}
	return &Journal{out: file}, nil
}

// NewWriter wraps an existing destination, for tests and tools.
func NewWriter(out io.WriteCloser) *Journal {
	return &Journal{out: out}
}

// Enabled reports whether records are being kept.
func (j *Journal) Enabled() bool {
	return j != nil
}

// Write appends one record as a single line.
func (j *Journal) Write(rec *Record) error {
	if j == nil || rec == nil {
		return nil
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode save journal record: %w", err)
	}
	line = append(line, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	// One Write per record: the rotating file only splits between writes, so a
	// record is never cut across two files.
	if _, err := j.out.Write(line); err != nil {
		return fmt.Errorf("write save journal record: %w", err)
	}
	return nil
}

// Close flushes and closes the journal file.
func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.out.Close()
}

// Region names a span of the savedata whose meaning the server knows.
type Region struct {
	Name string
	Off  int
	Len  int
	// Numeric marks a little-endian unsigned value of 1, 2, 4 or 8 bytes,
	// which is also reported decoded in Record.Fields.
	Numeric bool
	// Stacks marks a table of eight-byte item stacks: item id u16, quantity
	// u16, then four bytes the client leaves zero. The quantity held of each
	// item is also reported in Record.Items.
	Stacks bool
}

// stackSize is the size of one entry of a Stacks region.
const stackSize = 8

// StackTotals reports, for every Stacks region, the items whose quantity
// summed over the whole table differs between the blobs, as
// region name -> item id -> [old, new]. A total ignores where a stack sits,
// so sorting or rearranging a box reports nothing.
func StackTotals(oldBlob, newBlob []byte, regions []Region) map[string]map[string][2]uint32 {
	var out map[string]map[string][2]uint32
	for _, r := range regions {
		if !r.Stacks {
			continue
		}
		oldTable, newTable := regionBytes(oldBlob, r), regionBytes(newBlob, r)
		if bytes.Equal(oldTable, newTable) {
			continue
		}
		before, after := sumStacks(oldTable), sumStacks(newTable)
		var changed map[string][2]uint32
		note := func(id uint16) {
			if before[id] == after[id] {
				return
			}
			if changed == nil {
				changed = make(map[string][2]uint32)
			}
			changed[strconv.Itoa(int(id))] = [2]uint32{before[id], after[id]}
		}
		for id := range before {
			note(id)
		}
		for id := range after {
			note(id)
		}
		if changed != nil {
			if out == nil {
				out = make(map[string]map[string][2]uint32)
			}
			out[r.Name] = changed
		}
	}
	return out
}

func regionBytes(blob []byte, r Region) []byte {
	if r.Off < 0 || r.Len <= 0 || r.Off >= len(blob) {
		return nil
	}
	return blob[r.Off:min(r.Off+r.Len, len(blob))]
}

func sumStacks(table []byte) map[uint16]uint32 {
	totals := make(map[uint16]uint32)
	for off := 0; off+stackSize <= len(table); off += stackSize {
		// Item 0 is an empty stack.
		if id := uint16(table[off]) | uint16(table[off+1])<<8; id != 0 {
			totals[id] += uint32(table[off+2]) | uint32(table[off+3])<<8
		}
	}
	return totals
}

// Range is one contiguous run of changed bytes.
type Range struct {
	Off int `json:"off"`
	// Old and New are hex. Either may be empty when the blob grew or shrank.
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
	// OldLen and NewLen replace the bytes once the record's byte budget is spent.
	OldLen int `json:"old_len,omitempty"`
	NewLen int `json:"new_len,omitempty"`
	// At lists the known regions this range touches.
	At string `json:"at,omitempty"`
}

// DiffResult is the byte-level difference between two blobs.
type DiffResult struct {
	Ranges []Range
	// Changed counts byte positions that differ, including bytes added or
	// removed by a length change. It is exact even when Truncated is set.
	Changed int
	// Truncated means some ranges carry lengths only, or were not listed.
	Truncated bool
}

// Diff lists where newBlob differs from oldBlob. budget bounds the number of
// old+new bytes written out; ranges beyond it keep their offset and length.
func Diff(oldBlob, newBlob []byte, regions []Region, budget int) DiffResult {
	var res DiffResult
	emit := func(off int, oldPart, newPart []byte) {
		if len(res.Ranges) >= maxRanges {
			res.Truncated = true
			return
		}
		r := Range{Off: off, At: regionsAt(regions, off, off+max(len(oldPart), len(newPart)))}
		if cost := len(oldPart) + len(newPart); cost <= budget {
			budget -= cost
			r.Old = hex.EncodeToString(oldPart)
			r.New = hex.EncodeToString(newPart)
		} else {
			res.Truncated = true
			r.OldLen, r.NewLen = len(oldPart), len(newPart)
		}
		res.Ranges = append(res.Ranges, r)
	}

	common := min(len(oldBlob), len(newBlob))
	for i := 0; i < common; {
		if oldBlob[i] == newBlob[i] {
			i++
			continue
		}
		start, end := i, i+1 // end is one past the last differing byte
		res.Changed++
		j := i + 1
		for ; j < common; j++ {
			if oldBlob[j] != newBlob[j] {
				res.Changed++
				end = j + 1
			} else if j-end >= mergeGap {
				break
			}
		}
		emit(start, oldBlob[start:end], newBlob[start:end])
		i = j
	}
	if len(oldBlob) != len(newBlob) {
		res.Changed += max(len(oldBlob), len(newBlob)) - common
		emit(common, oldBlob[common:], newBlob[common:])
	}
	return res
}

func regionsAt(regions []Region, start, end int) string {
	var names []string
	for _, r := range regions {
		if r.Len > 0 && r.Off < end && start < r.Off+r.Len {
			names = append(names, r.Name)
		}
	}
	return strings.Join(names, ",")
}

// Fields decodes every numeric region whose value differs between the blobs,
// as name -> [old, new].
func Fields(oldBlob, newBlob []byte, regions []Region) map[string][2]uint64 {
	var out map[string][2]uint64
	for _, r := range regions {
		if !r.Numeric {
			continue
		}
		before, okBefore := readUint(oldBlob, r.Off, r.Len)
		after, okAfter := readUint(newBlob, r.Off, r.Len)
		if !okBefore || !okAfter || before == after {
			continue
		}
		if out == nil {
			out = make(map[string][2]uint64)
		}
		out[r.Name] = [2]uint64{before, after}
	}
	return out
}

func readUint(blob []byte, off, size int) (uint64, bool) {
	if (size != 1 && size != 2 && size != 4 && size != 8) || off < 0 || off > len(blob)-size {
		return 0, false
	}
	var v uint64
	for i := size - 1; i >= 0; i-- {
		v = v<<8 | uint64(blob[off+i])
	}
	return v, true
}

// Record is one line of the journal: one save written to the database.
type Record struct {
	V      int    `json:"v"`
	Time   string `json:"t"` // UTC, RFC 3339 with milliseconds
	CharID uint32 `json:"char"`
	UserID uint32 `json:"user,omitempty"`
	Name   string `json:"name,omitempty"`
	// Kind is where the save came from: "diff" (a client patch against its
	// last upload), "blob" (the whole savedata from the client) or "server"
	// (the server changed the stored save itself: logout playtime, a reward
	// written while the character was away, a repaired blob).
	Kind string `json:"kind"`
	// Baseline marks a save with no meaningful predecessor (character
	// creation). It carries hashes and sizes but no ranges.
	Baseline bool `json:"baseline,omitempty"`
	OldLen   int  `json:"old_len"`
	NewLen   int  `json:"new_len"`
	// OldSHA256 and NewSHA256 are over the decompressed savedata. A record's
	// OldSHA256 equals the character's previous record's NewSHA256 unless the
	// stored save was changed outside the server (an operator edit, a restored
	// backup) or a journal write failed.
	OldSHA256 string `json:"old_sha256,omitempty"`
	NewSHA256 string `json:"new_sha256"`
	// Fields holds the named numeric values that changed, as [old, new].
	Fields map[string][2]uint64 `json:"fields,omitempty"`
	// Items holds, per item table, the items whose total quantity changed,
	// as table name -> item id -> [old, new].
	Items     map[string]map[string][2]uint32 `json:"items,omitempty"`
	Changed   int                             `json:"changed"`
	Ranges    []Range                         `json:"ranges,omitempty"`
	Truncated bool                            `json:"truncated,omitempty"`
	// Activity is absent on "server" records: they do not close the window.
	Activity *ActivityRecord `json:"activity,omitempty"`
}

// ActivityRecord is what the session did between the previous journaled
// client save (or its login) and this one.
type ActivityRecord struct {
	Seconds int64 `json:"window_s"`
	// Ops counts received packets by opcode name.
	Ops     map[string]uint32 `json:"ops,omitempty"`
	Events  []Event           `json:"events,omitempty"`
	Dropped int               `json:"events_dropped,omitempty"`
}

// Event is a notable moment inside an activity window.
type Event struct {
	At   int64  `json:"at_s"` // seconds since the window began
	Kind string `json:"k"`
	// Quest fields, set for "quest_enter" and "quest_result".
	Quest   uint16 `json:"quest,omitempty"`
	Name    string `json:"name,omitempty"`
	Frames  uint32 `json:"frames,omitempty"`
	Cleared *bool  `json:"cleared,omitempty"`
	// Environment fields, set for "env": what the client reported about its
	// own PC with an integrity proof. Tools lists known memory editors it saw
	// ("-" when none), Debugger a debugger on the game, and Span how many
	// seconds back the report reaches (0 on the first report of a connection).
	Tools    string `json:"tools,omitempty"`
	Debugger bool   `json:"dbg,omitempty"`
	Span     int64  `json:"span_s,omitempty"`
}

// Activity accumulates one session's activity between saves. The zero value
// is ready to use.
type Activity struct {
	mu      sync.Mutex
	start   time.Time
	ops     map[uint16]uint32
	events  []Event
	dropped int
}

// Window is the activity taken out of an Activity by Take.
type Window struct {
	Seconds int64
	Ops     map[uint16]uint32
	Events  []Event
	Dropped int
}

// Count records one received packet.
func (a *Activity) Count(opcode uint16, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.begin(now)
	if a.ops == nil {
		a.ops = make(map[uint16]uint32)
	}
	a.ops[opcode]++
}

// Add records an event, stamping it relative to the window start.
func (a *Activity) Add(e Event, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.begin(now)
	if len(a.events) >= maxEvents {
		a.dropped++
		return
	}
	e.At = int64(now.Sub(a.start) / time.Second)
	a.events = append(a.events, e)
}

// Take returns the accumulated window and starts a new one at now.
func (a *Activity) Take(now time.Time) Window {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.begin(now)
	w := Window{Seconds: int64(now.Sub(a.start) / time.Second),
		Ops: a.ops, Events: a.events, Dropped: a.dropped}
	a.start, a.ops, a.events, a.dropped = now, nil, nil, 0
	return w
}

func (a *Activity) begin(now time.Time) {
	if a.start.IsZero() {
		a.start = now
	}
}
