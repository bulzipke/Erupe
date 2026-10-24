package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"erupe-ce/cmd/protbot/scenario"
)

const (
	questExpectedSchema  = "erupe-quest-expected-ids-v1"
	questReceiptSchema   = "erupe-quest-receipt-v1"
	questExpectedLimit   = 512 * 1024
	questReceiptCapacity = 16384
)

type questExpectation struct {
	ids    []uint16
	sha256 string
}

type questRunOptions struct {
	world       uint8
	counter     uint16
	expectation *questExpectation
	reportPath  string
}

// Expected IDs are intentionally bound to the exact enumerate selectors. They
// must also reflect the test account's original rights and progression. This is
// an operator-supplied assertion, never a way to bypass those conditions.
func parseQuestExpectation(data []byte, world uint8, counter uint16) (*questExpectation, error) {
	if len(data) > questExpectedLimit {
		return nil, fmt.Errorf("quest expected-ID file exceeds %d bytes", questExpectedLimit)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("quest expected-ID file must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid quest expected-ID field: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid quest expected-ID field name")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate quest expected-ID field %q", key)
		}
		switch key {
		case "schema", "world", "counter", "quest_ids":
		default:
			return nil, fmt.Errorf("unknown quest expected-ID field %q", key)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("invalid quest expected-ID field %q: %w", key, err)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("quest expected-ID field %q cannot be null", key)
		}
		fields[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("invalid quest expected-ID object: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing data in quest expected-ID file")
	}
	if len(fields) != 4 {
		return nil, fmt.Errorf("quest expected-ID file requires schema, world, counter and quest_ids")
	}
	var schema string
	var expectedWorld uint8
	var expectedCounter uint16
	var ids []uint16
	if json.Unmarshal(fields["schema"], &schema) != nil || schema != questExpectedSchema ||
		json.Unmarshal(fields["world"], &expectedWorld) != nil ||
		json.Unmarshal(fields["counter"], &expectedCounter) != nil ||
		json.Unmarshal(fields["quest_ids"], &ids) != nil {
		return nil, fmt.Errorf("invalid quest expected-ID schema or field types")
	}
	if expectedWorld != world || expectedCounter != counter {
		return nil, fmt.Errorf("quest expected selectors world=%d counter=%d differ from requested world=%d counter=%d", expectedWorld, expectedCounter, world, counter)
	}
	if len(ids) > questReceiptCapacity {
		return nil, fmt.Errorf("quest expected-ID count exceeds %d", questReceiptCapacity)
	}
	seen := make(map[uint16]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			return nil, fmt.Errorf("zero or duplicate quest expected ID %d", id)
		}
		seen[id] = true
	}
	hash := sha256.Sum256(data)
	return &questExpectation{ids: ids, sha256: hex.EncodeToString(hash[:])}, nil
}

func prepareQuestRunOptions(action string, world, counter uint, expectedPath, reportPath string) (questRunOptions, error) {
	var options questRunOptions
	if world > 255 || counter > 65535 {
		return options, fmt.Errorf("quest world must be 0..255 and counter 0..65535")
	}
	if action != "quests" {
		if world != 0 || counter != 0 || expectedPath != "" || reportPath != "" {
			return options, fmt.Errorf("quest selector, expectation and report flags require --action quests")
		}
		return options, nil
	}
	options.world, options.counter = uint8(world), uint16(counter)
	if expectedPath != "" {
		file, err := os.Open(expectedPath)
		if err != nil {
			return options, fmt.Errorf("open quest expected-ID file: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, questExpectedLimit+1))
		closeErr := file.Close()
		if readErr != nil {
			return options, fmt.Errorf("read quest expected-ID file: %w", readErr)
		}
		if closeErr != nil {
			return options, fmt.Errorf("close quest expected-ID file: %w", closeErr)
		}
		options.expectation, err = parseQuestExpectation(data, options.world, options.counter)
		if err != nil {
			return options, err
		}
	}
	if reportPath != "" {
		absolute, err := filepath.Abs(reportPath)
		if err != nil {
			return options, fmt.Errorf("quest report path: %w", err)
		}
		if _, err := os.Lstat(absolute); !os.IsNotExist(err) {
			if err == nil {
				return options, fmt.Errorf("quest report already exists: %s", absolute)
			}
			return options, fmt.Errorf("inspect quest report path: %w", err)
		}
		parent, err := os.Stat(filepath.Dir(absolute))
		if err != nil || !parent.IsDir() {
			return options, fmt.Errorf("quest report parent must be an existing directory")
		}
		options.reportPath = absolute
	}
	return options, nil
}

type questReceiptReport struct {
	Schema             string   `json:"schema"`
	World              uint8    `json:"world"`
	Counter            uint16   `json:"counter"`
	CompleteSnapshot   bool     `json:"complete_snapshot"`
	AnnouncedTotal     int      `json:"announced_total"`
	Received           int      `json:"received"`
	Pages              int      `json:"pages"`
	ACKPayloadBytes    int      `json:"ack_payload_bytes"`
	EntryBytes         int      `json:"entry_bytes"`
	QuestIDs           []uint16 `json:"quest_ids"`
	ExpectedIDsChecked bool     `json:"expected_ids_checked"`
	ExpectedCount      *int     `json:"expected_count"`
	ExpectedFileSHA256 string   `json:"expected_file_sha256,omitempty"`
	MatchesExpected    *bool    `json:"matches_expected"`
	MissingIDs         []uint16 `json:"missing_ids"`
	UnexpectedIDs      []uint16 `json:"unexpected_ids"`
	Error              string   `json:"error,omitempty"`
	EligibilityScope   string   `json:"eligibility_scope"`
}

func buildQuestReceiptReport(options questRunOptions, result *scenario.QuestEnumerationResult, enumerationErr error) (questReceiptReport, error) {
	report := questReceiptReport{
		Schema: questReceiptSchema, World: options.world, Counter: options.counter,
		QuestIDs: []uint16{}, MissingIDs: []uint16{}, UnexpectedIDs: []uint16{},
		EligibilityScope: "Expected IDs must reflect these selectors and the test account's original entry rights and progression.",
	}
	if enumerationErr != nil {
		report.Error = enumerationErr.Error()
		return report, enumerationErr
	}
	if result == nil || result.Total < 0 || result.Total > questReceiptCapacity || result.Total != len(result.QuestIDs) || result.Pages < 1 {
		err := fmt.Errorf("invalid complete quest enumeration result")
		report.Error = err.Error()
		return report, err
	}
	seen := make(map[uint16]bool, len(result.QuestIDs))
	for _, id := range result.QuestIDs {
		if id == 0 || seen[id] {
			err := fmt.Errorf("invalid zero or duplicate received quest ID %d", id)
			report.Error = err.Error()
			return report, err
		}
		seen[id] = true
	}
	report.CompleteSnapshot = true
	report.AnnouncedTotal, report.Received = result.Total, len(result.QuestIDs)
	report.Pages, report.ACKPayloadBytes, report.EntryBytes = result.Pages, result.PayloadBytes, len(result.Entries)
	report.QuestIDs = append(report.QuestIDs, result.QuestIDs...)
	if options.expectation == nil {
		return report, nil
	}
	report.ExpectedIDsChecked = true
	count := len(options.expectation.ids)
	report.ExpectedCount, report.ExpectedFileSHA256 = &count, options.expectation.sha256
	expected := make(map[uint16]bool, count)
	for _, id := range options.expectation.ids {
		expected[id] = true
		if !seen[id] {
			report.MissingIDs = append(report.MissingIDs, id)
		}
	}
	for _, id := range result.QuestIDs {
		if !expected[id] {
			report.UnexpectedIDs = append(report.UnexpectedIDs, id)
		}
	}
	sort.Slice(report.MissingIDs, func(i, j int) bool { return report.MissingIDs[i] < report.MissingIDs[j] })
	sort.Slice(report.UnexpectedIDs, func(i, j int) bool { return report.UnexpectedIDs[i] < report.UnexpectedIDs[j] })
	matched := len(report.MissingIDs) == 0 && len(report.UnexpectedIDs) == 0
	report.MatchesExpected = &matched
	if !matched {
		err := fmt.Errorf("quest IDs differ from expected roster: missing=%d unexpected=%d", len(report.MissingIDs), len(report.UnexpectedIDs))
		report.Error = err.Error()
		return report, err
	}
	return report, nil
}

func writeQuestReceiptReport(path string, report questReceiptReport) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create quest receipt report: %w", err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write quest receipt report: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close quest receipt report: %w", closeErr)
	}
	return nil
}
