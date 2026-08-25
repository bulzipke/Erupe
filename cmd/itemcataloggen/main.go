// Command itemcataloggen converts the language-independent fields in a
// Ferias sozai/item.js snapshot into a compact Go lookup table.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	raritySuffixNone uint8 = iota
	raritySuffixM
	raritySuffixX
)

var (
	itemObjectStartPattern = regexp.MustCompile(`^var\s+setItem\s*=\s*function\s*\(\s*\)\s*\{\s*return\s*\{\s*$`)
	itemEntryPattern       = regexp.MustCompile(`^\s*(,?)"([0-9A-Fa-f]{4})":(\[.*\])\s*$`)
	raritySuffixPattern    = regexp.MustCompile(`^([1-7])([MX])$`)
)

type catalogRecord struct {
	ID           uint16
	Rarity       uint8
	RaritySuffix uint8
	PouchMax     uint8
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("itemcataloggen", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputPath := flags.String("input", "", "path to Ferias sozai/item.js")
	outputPath := flags.String("output", "", "path to catalog_generated.go")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *inputPath == "" {
		return errors.New("-input is required")
	}
	if *outputPath == "" {
		return errors.New("-output is required")
	}

	source, err := os.ReadFile(*inputPath)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	records, err := parseCatalog(source)
	if err != nil {
		return err
	}

	digest := sha256.Sum256(source)
	generated, err := renderCatalog(records, strings.ToUpper(hex.EncodeToString(digest[:])))
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outputPath, generated, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func parseCatalog(source []byte) ([]catalogRecord, error) {
	scanner := bufio.NewScanner(bytes.NewReader(source))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		records  []catalogRecord
		lineNo   int
		inObject bool
		ended    bool
	)
	seen := make(map[uint16]int)

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if !inObject {
			if itemObjectStartPattern.MatchString(trimmed) {
				inObject = true
			}
			continue
		}
		if ended {
			if trimmed != "" {
				return nil, fmt.Errorf("line %d: unexpected content after item object", lineNo)
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if trimmed == "};};" {
			ended = true
			continue
		}

		match := itemEntryPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("line %d: unexpected item object content", lineNo)
		}
		hasLeadingComma := match[1] == ","
		if len(records) == 0 && hasLeadingComma {
			return nil, fmt.Errorf("line %d: first item entry has a leading comma", lineNo)
		}
		if len(records) > 0 && !hasLeadingComma {
			return nil, fmt.Errorf("line %d: item entry is missing its leading comma", lineNo)
		}

		id64, err := strconv.ParseUint(match[2], 16, 16)
		if err != nil {
			return nil, fmt.Errorf("line %d: parse item ID: %w", lineNo, err)
		}
		id := uint16(id64)
		if id == 0 || id == ^uint16(0) {
			return nil, fmt.Errorf("line %d: reserved item ID %04X", lineNo, id)
		}
		if firstLine, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("line %d: duplicate item ID %04X (first seen on line %d)", lineNo, id, firstLine)
		}
		if len(records) > 0 && id <= records[len(records)-1].ID {
			return nil, fmt.Errorf("line %d: item ID %04X is not strictly after %04X", lineNo, id, records[len(records)-1].ID)
		}

		var fields []json.RawMessage
		if err := json.Unmarshal([]byte(match[3]), &fields); err != nil {
			return nil, fmt.Errorf("line %d item %04X: parse fields: %w", lineNo, id, err)
		}
		if len(fields) != 7 {
			return nil, fmt.Errorf("line %d item %04X: got %d fields, want 7", lineNo, id, len(fields))
		}

		rarity, suffix, err := decodeRarity(fields[1])
		if err != nil {
			return nil, fmt.Errorf("line %d item %04X: %w", lineNo, id, err)
		}
		pouchMax, err := decodeInteger(fields[3], "pouch maximum")
		if err != nil || pouchMax < 1 || pouchMax > 255 {
			if err != nil {
				return nil, fmt.Errorf("line %d item %04X: %w", lineNo, id, err)
			}
			return nil, fmt.Errorf("line %d item %04X: pouch maximum %d is outside 1..255", lineNo, id, pouchMax)
		}
		seen[id] = lineNo
		records = append(records, catalogRecord{
			ID:           id,
			Rarity:       rarity,
			RaritySuffix: suffix,
			PouchMax:     uint8(pouchMax),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan input: %w", err)
	}
	if !inObject {
		return nil, errors.New("Ferias setItem object start was not found")
	}
	if !ended {
		return nil, errors.New("Ferias setItem object end was not found")
	}
	if len(records) == 0 {
		return nil, errors.New("Ferias setItem object contained no items")
	}
	return records, nil
}

func decodeRarity(raw json.RawMessage) (uint8, uint8, error) {
	var numeric int
	if err := json.Unmarshal(raw, &numeric); err == nil {
		if numeric < 1 || numeric > 7 {
			return 0, 0, fmt.Errorf("rarity %d is outside 1..7", numeric)
		}
		return uint8(numeric), raritySuffixNone, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, 0, errors.New("rarity must be an integer or an M/X-suffixed string")
	}
	match := raritySuffixPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, 0, fmt.Errorf("invalid rarity %q", text)
	}
	rarity := uint8(match[1][0] - '0')
	suffix := raritySuffixM
	if match[2] == "X" {
		suffix = raritySuffixX
	}
	return rarity, suffix, nil
}

func decodeInteger(raw json.RawMessage, field string) (int, error) {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	return value, nil
}

func renderCatalog(records []catalogRecord, sourceSHA256 string) ([]byte, error) {
	if len(records) == 0 {
		return nil, errors.New("cannot render an empty catalog")
	}
	if len(sourceSHA256) != sha256.Size*2 {
		return nil, fmt.Errorf("source SHA-256 must have %d hexadecimal characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(sourceSHA256); err != nil {
		return nil, fmt.Errorf("invalid source SHA-256: %w", err)
	}

	var output bytes.Buffer
	fmt.Fprintln(&output, "// Code generated by cmd/itemcataloggen; DO NOT EDIT.")
	fmt.Fprintln(&output, "// Source: Ferias sozai/item.js")
	fmt.Fprintf(&output, "// Source SHA-256: %s\n\n", strings.ToUpper(sourceSHA256))
	fmt.Fprintln(&output, "package mhfitem")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "const (")
	fmt.Fprintf(&output, "\tItemCatalogSourceSHA256 = %q\n", strings.ToUpper(sourceSHA256))
	fmt.Fprintf(&output, "\tItemCatalogEntryCount = %d\n", len(records))
	fmt.Fprintf(&output, "\tItemCatalogMaxID uint16 = 0x%04X\n", records[len(records)-1].ID)
	fmt.Fprintln(&output, ")")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "var generatedItemCatalog = [1 << 16]uint16{")
	for _, record := range records {
		packed := uint16(record.PouchMax) |
			uint16(record.Rarity)<<8 |
			uint16(record.RaritySuffix)<<11 |
			0x8000
		fmt.Fprintf(&output, "\t0x%04X: 0x%04X,\n", record.ID, packed)
	}
	fmt.Fprintln(&output, "}")

	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return formatted, nil
}
