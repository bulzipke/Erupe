package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"erupe-ce/common/questdelivery"
)

// listingInventory binds declared listing-source IDs to the exact manifest
// bytes. It does not certify that those sources are installed or playable.
type listingInventory struct {
	Schema         string `json:"schema"`
	ManifestSHA256 string `json:"manifest_sha256"`
	QuestIDs       []int  `json:"quest_ids"`
}

func checkListingInventory(path string, manifest []byte, catalog *questdelivery.Catalog) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	const maxBytes = 16 << 20
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return errors.New("listing inventory must be a regular file no larger than 16 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxBytes {
		return errors.New("listing inventory exceeds 16 MiB")
	}
	var inventory listingInventory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		return fmt.Errorf("decode listing inventory: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("listing inventory must contain exactly one JSON document")
	}
	digest := sha256.Sum256(manifest)
	if inventory.Schema != "quest-listing-inventory-v1" || inventory.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("listing inventory schema or exact manifest SHA256 mismatch")
	}
	if inventory.QuestIDs == nil || len(inventory.QuestIDs) > 65535 {
		return errors.New("listing inventory requires an explicit array of at most 65535 quest IDs")
	}
	seen := make(map[int]bool, len(inventory.QuestIDs))
	for _, id := range inventory.QuestIDs {
		if id < 1 || id > 65535 || seen[id] {
			return fmt.Errorf("invalid or duplicate listing inventory quest ID %d", id)
		}
		seen[id] = true
	}
	if missing := catalog.MissingListingQuestIDs(inventory.QuestIDs); len(missing) != 0 {
		shown := missing
		if len(shown) > 16 {
			shown = shown[:16]
		}
		return fmt.Errorf("listing inventory is missing %d enabled managed quests (first IDs %v)", len(missing), shown)
	}
	return nil
}
