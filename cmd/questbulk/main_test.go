package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"erupe-ce/common/questbulk"
)

func fixture(t *testing.T) (string, questbulk.Plan) {
	t.Helper()
	root := t.TempDir()
	evidence := []byte("raw snapshot and listing evidence fixture\n")
	if err := os.WriteFile(filepath.Join(root, "evidence.json"), evidence, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(evidence)
	s := questbulk.Source{Path: "evidence.json", Bytes: int64(len(evidence)), SHA256: hex.EncodeToString(hash[:])}
	p := questbulk.Plan{Schema: questbulk.Schema, BatchID: "offline-local-test", Capacity: 16384, SnapshotSource: s, Existing: []questbulk.Existing{}, Additions: []questbulk.Addition{{QuestID: 1234, MaxPlayers: 4, QuestType: 18, Mark: 1, Flags: -1, Evidence: s}}, ActivateQuestIDs: []int{1234}}
	return root, p
}

func writePlan(t *testing.T, root string, p questbulk.Plan) string {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plan.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOfflineBundleAndNoOverwrite(t *testing.T) {
	root, p := fixture(t)
	input := writePlan(t, root, p)
	output := filepath.Join(root, "bundle")
	var stdout bytes.Buffer
	if err := run([]string{"-input", input, "-source-root", root, "-output-dir", output}, &stdout); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Complete         bool `json:"complete"`
		DatabaseExecuted bool `json:"database_executed"`
		Files            []struct {
			Name   string `json:"name"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !m.Complete || m.DatabaseExecuted || len(m.Files) != 3 {
		t.Fatal("incorrect bundle state")
	}
	for _, f := range m.Files {
		body, err := os.ReadFile(filepath.Join(output, f.Name))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(body)
		if len(body) != f.Bytes || hex.EncodeToString(hash[:]) != f.SHA256 {
			t.Fatal("saved SQL does not match manifest")
		}
	}
	if err := run([]string{"-input", input, "-source-root", root, "-output-dir", output}, &stdout); err == nil {
		t.Fatal("overwrote existing bundle")
	}
	after, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("existing manifest changed")
	}
}

func TestChangedEvidenceCreatesNoOutput(t *testing.T) {
	root, p := fixture(t)
	input := writePlan(t, root, p)
	output := filepath.Join(root, "bundle")
	if err := os.WriteFile(filepath.Join(root, "evidence.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-input", input, "-source-root", root, "-output-dir", output}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted stale evidence")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("created output before evidence validation")
	}
}
