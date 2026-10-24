package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"erupe-ce/cmd/protbot/scenario"
)

func expectedQuestFixture(world uint8, counter uint16, ids []uint16) []byte {
	data, err := json.Marshal(map[string]any{"schema": questExpectedSchema, "world": world, "counter": counter, "quest_ids": ids})
	if err != nil {
		panic(err)
	}
	return data
}

func TestQuestExpectationBindsSelectorsAndRejectsInvalidInput(t *testing.T) {
	good := expectedQuestFixture(2, 7, []uint16{65535, 19, 40001})
	expected, err := parseQuestExpectation(good, 2, 7)
	if err != nil || fmt.Sprint(expected.ids) != "[65535 19 40001]" || len(expected.sha256) != 64 {
		t.Fatalf("valid expectation failed: %+v %v", expected, err)
	}
	tests := map[string][]byte{
		"wrong world":           expectedQuestFixture(1, 7, []uint16{19}),
		"wrong counter":         expectedQuestFixture(2, 6, []uint16{19}),
		"duplicate ID":          expectedQuestFixture(2, 7, []uint16{19, 19}),
		"zero ID":               expectedQuestFixture(2, 7, []uint16{0}),
		"null IDs":              expectedQuestFixture(2, 7, nil),
		"missing field":         []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"quest_ids":[]}`),
		"duplicate field":       []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"world":2,"counter":7,"quest_ids":[]}`),
		"unknown field":         []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"counter":7,"quest_ids":[],"allow_all":true}`),
		"wrong schema":          []byte(`{"schema":"other","world":2,"counter":7,"quest_ids":[]}`),
		"negative selector":     []byte(`{"schema":"erupe-quest-expected-ids-v1","world":-1,"counter":7,"quest_ids":[]}`),
		"out of range selector": []byte(`{"schema":"erupe-quest-expected-ids-v1","world":256,"counter":7,"quest_ids":[]}`),
		"out of range ID":       []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"counter":7,"quest_ids":[65536]}`),
		"fractional ID":         []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"counter":7,"quest_ids":[1.5]}`),
		"null ID":               []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"counter":7,"quest_ids":[null]}`),
		"non array":             []byte(`{"schema":"erupe-quest-expected-ids-v1","world":2,"counter":7,"quest_ids":{}}`),
		"trailing JSON":         append(append([]byte{}, good...), []byte(` {}`)...),
		"trailing malformed":    append(append([]byte{}, good...), 'x'),
		"truncated":             good[:len(good)-1],
		"array envelope":        []byte(`[]`),
		"oversize":              []byte(strings.Repeat(" ", questExpectedLimit+1)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if got, err := parseQuestExpectation(data, 2, 7); err == nil || got != nil {
				t.Fatalf("accepted invalid input: %+v %v", got, err)
			}
		})
	}
	ids := make([]uint16, questReceiptCapacity)
	for i := range ids {
		ids[i] = uint16(i + 1)
	}
	if got, err := parseQuestExpectation(expectedQuestFixture(2, 7, ids), 2, 7); err != nil || len(got.ids) != questReceiptCapacity {
		t.Fatalf("capacity boundary rejected: %v", err)
	}
	ids = append(ids, 65535)
	if _, err := parseQuestExpectation(expectedQuestFixture(2, 7, ids), 2, 7); err == nil {
		t.Fatal("overcapacity expectation accepted")
	}
	if got, err := parseQuestExpectation(expectedQuestFixture(2, 7, []uint16{}), 2, 7); err != nil || len(got.ids) != 0 {
		t.Fatalf("explicit empty roster rejected: %v", err)
	}
}

func TestQuestReceiptDetectsServerShortRosterAndWrongIDs(t *testing.T) {
	expected, err := parseQuestExpectation(expectedQuestFixture(2, 7, []uint16{7, 9, 11}), 2, 7)
	if err != nil {
		t.Fatal(err)
	}
	options := questRunOptions{world: 2, counter: 7, expectation: expected}
	tests := []struct {
		name                string
		ids                 []uint16
		missing, unexpected string
		match               bool
	}{
		{"same set different order", []uint16{11, 7, 9}, "[]", "[]", true},
		{"server claims shorter complete total", []uint16{7}, "[9 11]", "[]", false},
		{"same count wrong IDs", []uint16{12, 7, 2}, "[9 11]", "[2 12]", false},
		{"valid empty snapshot but nonempty expectation", []uint16{}, "[7 9 11]", "[]", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &scenario.QuestEnumerationResult{Total: len(tt.ids), Pages: 1, PayloadBytes: 123, QuestIDs: tt.ids, Entries: []byte{1, 2}}
			report, err := buildQuestReceiptReport(options, result, nil)
			if !report.CompleteSnapshot || report.Received != len(tt.ids) || !report.ExpectedIDsChecked || report.ExpectedCount == nil || *report.ExpectedCount != 3 || report.MatchesExpected == nil || *report.MatchesExpected != tt.match || (err == nil) != tt.match {
				t.Fatalf("incorrect receipt: %+v %v", report, err)
			}
			if fmt.Sprint(report.QuestIDs) != fmt.Sprint(tt.ids) || fmt.Sprint(report.MissingIDs) != tt.missing || fmt.Sprint(report.UnexpectedIDs) != tt.unexpected || report.ExpectedFileSHA256 != expected.sha256 {
				t.Fatalf("lost ID evidence: %+v", report)
			}
		})
	}
}

func TestQuestReceiptNoExpectationAndEnumerationFailure(t *testing.T) {
	result := &scenario.QuestEnumerationResult{Total: 1, Pages: 1, QuestIDs: []uint16{19}}
	report, err := buildQuestReceiptReport(questRunOptions{}, result, nil)
	if err != nil || !report.CompleteSnapshot || report.ExpectedIDsChecked || report.MatchesExpected != nil || report.ExpectedCount != nil {
		t.Fatalf("unrequested expectation claimed: %+v %v", report, err)
	}
	failure := errors.New("offline malformed page")
	report, err = buildQuestReceiptReport(questRunOptions{}, result, failure)
	if !errors.Is(err, failure) || report.CompleteSnapshot || len(report.QuestIDs) != 0 || report.Error != failure.Error() {
		t.Fatalf("failed page claimed receipt: %+v %v", report, err)
	}
	for _, bad := range []*scenario.QuestEnumerationResult{nil, {Total: 2, Pages: 1, QuestIDs: []uint16{1}}, {Total: 1, Pages: 0, QuestIDs: []uint16{1}}, {Total: 1, Pages: 1, QuestIDs: []uint16{0}}, {Total: 2, Pages: 1, QuestIDs: []uint16{1, 1}}} {
		if report, err := buildQuestReceiptReport(questRunOptions{}, bad, nil); err == nil || report.CompleteSnapshot {
			t.Fatalf("invalid result accepted: %+v %v", report, err)
		}
	}
}

func TestQuestOptionsPreflightFilesBeforeLogin(t *testing.T) {
	dir := t.TempDir()
	expectedPath := filepath.Join(dir, "expected.json")
	reportPath := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(expectedPath, expectedQuestFixture(2, 7, []uint16{19}), 0600); err != nil {
		t.Fatal(err)
	}
	options, err := prepareQuestRunOptions("quests", 2, 7, expectedPath, reportPath)
	if err != nil || options.world != 2 || options.counter != 7 || options.expectation == nil || options.reportPath != reportPath {
		t.Fatalf("preflight failed: %+v %v", options, err)
	}
	if _, err := os.Lstat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("preflight created receipt: %v", err)
	}
	tests := []struct {
		name, action     string
		world, counter   uint
		expected, report string
	}{
		{"wrong action", "login", 0, 0, expectedPath, ""},
		{"world range", "quests", 256, 0, "", ""},
		{"counter range", "quests", 0, 65536, "", ""},
		{"missing expected file", "quests", 2, 7, filepath.Join(dir, "absent.json"), ""},
		{"selector mismatch", "quests", 0, 0, expectedPath, ""},
		{"missing report parent", "quests", 0, 0, "", filepath.Join(dir, "absent", "receipt.json")},
		{"existing report file", "quests", 0, 0, "", expectedPath},
		{"existing report directory", "quests", 0, 0, "", dir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := prepareQuestRunOptions(tt.action, tt.world, tt.counter, tt.expected, tt.report); err == nil {
				t.Fatal("invalid preflight accepted")
			}
		})
	}
}

func TestQuestReceiptWriterNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	report := questReceiptReport{Schema: questReceiptSchema, QuestIDs: []uint16{19}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- writeQuestReceiptReport(path, report) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("exclusive create successes=%d", successes)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded questReceiptReport
	if err := json.Unmarshal(before, &decoded); err != nil || fmt.Sprint(decoded.QuestIDs) != "[19]" {
		t.Fatalf("invalid written receipt: %s %v", before, err)
	}
	if err := writeQuestReceiptReport(path, questReceiptReport{Schema: "replacement"}); err == nil {
		t.Fatal("overwritten receipt")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("existing receipt changed")
	}
}
