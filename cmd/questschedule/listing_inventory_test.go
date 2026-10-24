package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inventoryBytes(t *testing.T, manifest []byte, ids []int) []byte {
	t.Helper()
	digest := sha256.Sum256(manifest)
	data, err := json.Marshal(listingInventory{
		Schema: "quest-listing-inventory-v1", ManifestSHA256: hex.EncodeToString(digest[:]), QuestIDs: ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunListingInventoryPreservesPublicExport(t *testing.T) {
	input := writeManifest(t)
	inventory := filepath.Join(t.TempDir(), "inventory.json")
	// Disabled 40 needs no source; unmanaged 99 is allowed in a full source inventory.
	data := inventoryBytes(t, []byte(testManifest), []int{99, 30, 10, 20})
	if err := os.WriteFile(inventory, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-input", input, "-from", "2026-10-24", "-days", "1"}
	var before, checked bytes.Buffer
	if err := run(args, &before); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "-listing-ids", inventory), &checked); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), checked.Bytes()) {
		t.Fatal("the optional inventory check changed the public export")
	}
	assertInputUnchanged(t, inventory, data)
	assertInputUnchanged(t, input, []byte(testManifest))
}

func TestRunRejectsIncompleteOrInvalidListingInventoryWithoutReplacingOutput(t *testing.T) {
	valid := inventoryBytes(t, []byte(testManifest), []int{10, 20, 30})
	tests := map[string]struct {
		data []byte
		want string
	}{
		"missing future source outside export window": {inventoryBytes(t, []byte(testManifest), []int{10, 20}), "missing 1 enabled managed quests (first IDs [30])"},
		"empty inventory":      {inventoryBytes(t, []byte(testManifest), []int{}), "missing 3 enabled managed quests"},
		"null inventory":       {inventoryBytes(t, []byte(testManifest), nil), "explicit array"},
		"wrong manifest bytes": {inventoryBytes(t, []byte(testManifest+"\n"), []int{10, 20, 30}), "SHA256 mismatch"},
		"wrong schema":         {bytes.Replace(valid, []byte("quest-listing-inventory-v1"), []byte("future-schema"), 1), "schema"},
		"duplicate ID":         {inventoryBytes(t, []byte(testManifest), []int{10, 20, 30, 30}), "duplicate"},
		"zero ID":              {inventoryBytes(t, []byte(testManifest), []int{10, 20, 30, 0}), "invalid"},
		"negative ID":          {inventoryBytes(t, []byte(testManifest), []int{-1, 10, 20, 30}), "invalid"},
		"overflow ID":          {inventoryBytes(t, []byte(testManifest), []int{10, 20, 30, 65536}), "invalid"},
		"unknown field":        {append([]byte(`{"extra":true,`), valid[1:]...), "unknown field"},
		"string ID":            {bytes.Replace(valid, []byte("[10,20,30]"), []byte(`[10,20,"30"]`), 1), "decode listing inventory"},
		"second document":      {append(append([]byte{}, valid...), []byte("\n{}")...), "exactly one JSON document"},
		"trailing junk":        {append(append([]byte{}, valid...), '!'), "exactly one JSON document"},
		"malformed JSON":       {valid[:len(valid)-1], "decode listing inventory"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			input := writeManifest(t)
			dir := t.TempDir()
			inventory := filepath.Join(dir, "inventory.json")
			output := filepath.Join(dir, "calendar.json")
			previous := []byte("previous published schedule\n")
			if err := os.WriteFile(inventory, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(output, previous, 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			err := run([]string{"-input", input, "-listing-ids", inventory, "-output", output, "-from", "2026-10-24", "-days", "1"}, &stdout)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if stdout.Len() != 0 {
				t.Fatal("rejected inventory wrote partial stdout")
			}
			assertInputUnchanged(t, output, previous)
			assertInputUnchanged(t, inventory, tc.data)
			assertInputUnchanged(t, input, []byte(testManifest))
			assertNoScheduleTemps(t, dir)
		})
	}
}

func TestRunListingInventoryRejectsNonFilesAndOversizedInputs(t *testing.T) {
	input := writeManifest(t)
	dir := t.TempDir()
	large := filepath.Join(dir, "large.json")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(16<<20 + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, large, filepath.Join(dir, "missing.json")} {
		var stdout bytes.Buffer
		if err := run([]string{"-input", input, "-listing-ids", path}, &stdout); err == nil || stdout.Len() != 0 {
			t.Fatalf("bad inventory path accepted or produced output: %v", err)
		}
	}
}

func TestRunRejectsOutputAliasesOfListingInventory(t *testing.T) {
	for _, alias := range []string{"identical", "dot directory", "hard link"} {
		t.Run(alias, func(t *testing.T) {
			input := writeManifest(t)
			inventory := filepath.Join(t.TempDir(), "inventory.json")
			data := inventoryBytes(t, []byte(testManifest), []int{10, 20, 30})
			if err := os.WriteFile(inventory, data, 0o600); err != nil {
				t.Fatal(err)
			}
			output := inventory
			switch alias {
			case "dot directory":
				output = filepath.Dir(inventory) + string(os.PathSeparator) + "." + string(os.PathSeparator) + filepath.Base(inventory)
			case "hard link":
				output = filepath.Join(filepath.Dir(inventory), "inventory-hardlink.json")
				if err := os.Link(inventory, output); err != nil {
					t.Skipf("filesystem does not support a test hard link: %v", err)
				}
			}
			err := run([]string{"-input", input, "-listing-ids", inventory, "-output", output, "-from", "2026-10-24"}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "inventory and output must be different") {
				t.Fatalf("output alias of inventory accepted: %v", err)
			}
			assertInputUnchanged(t, inventory, data)
		})
	}
}
