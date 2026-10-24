// pallone-client builds an offline ZZ client candidate beside an audited quest
// bundle. It never installs assets, starts a client, or enables a server event.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"erupe-ce/common/decryption"
	"erupe-ce/common/mhfpallone"
)

type questBundle struct {
	Kind    string   `json:"kind"`
	Order   []uint16 `json:"stage_order"`
	Live    bool     `json:"live_client_verified"`
	Enabled bool     `json:"event_enabled"`
	Files   []struct {
		Filename string `json:"filename"`
		SHA256   string `json:"packed_sha256"`
	} `json:"files"`
}

func main() {
	dat := flag.String("dat", "", "read-only ZZ mhfdat.bin")
	inf := flag.String("inf", "", "read-only ZZ mhfinf.bin")
	bundle := flag.String("bundle", "", "offline route-001 quest bundle")
	out := flag.String("out", "", "new isolated output directory")
	flag.Parse()
	if err := run(*dat, *inf, *bundle, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func run(datPath, infPath, bundle, output string) error {
	if datPath == "" || infPath == "" || bundle == "" || output == "" {
		return fmt.Errorf("usage: pallone-client -dat file -inf file -bundle offline-directory -out new-directory")
	}
	if err := isolatedOutput(output, []string{filepath.Dir(datPath), filepath.Dir(infPath), bundle}); err != nil {
		return err
	}
	dat, err := os.ReadFile(datPath)
	if err != nil {
		return err
	}
	inf, err := os.ReadFile(infPath)
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		return err
	}
	var plan questBundle
	if err := json.Unmarshal(manifest, &plan); err != nil {
		return err
	}
	if plan.Kind != "offline_reconstructed_route_bundle" || plan.Live || plan.Enabled ||
		!reflect.DeepEqual(plan.Order, []uint16{27438, 27479, 27348, 27365}) || len(plan.Files) != 24 {
		return fmt.Errorf("pallone-client: expected unarmed reconstructed route-001 bundle with 24 files")
	}
	expected := make(map[string]bool)
	for _, id := range plan.Order {
		for _, suffix := range []string{"d0", "d1", "d2", "n0", "n1", "n2"} {
			expected[fmt.Sprintf("%05d%s.bin", id, suffix)] = true
		}
	}
	files := make(map[string][]byte)
	for _, entry := range plan.Files {
		if !expected[entry.Filename] || files[entry.Filename] != nil {
			return fmt.Errorf("pallone-client: invalid/duplicate bundle filename")
		}
		b, err := os.ReadFile(filepath.Join(bundle, "quests", entry.Filename))
		if err != nil {
			return err
		}
		if digest(b) != entry.SHA256 {
			return fmt.Errorf("pallone-client: bundle hash mismatch for %s", entry.Filename)
		}
		files[entry.Filename] = b
	}
	day := make(map[uint16][]byte)
	for _, id := range plan.Order {
		day[id] = files[fmt.Sprintf("%05dd0.bin", id)]
	}
	draft, err := mhfpallone.BuildClientDraft(dat, inf, 1, day)
	if err != nil {
		return err
	}
	packedDat, err := decryption.EncodeECD(decryption.PackSimple(draft.Data), decryption.DefaultECDKey)
	if err != nil {
		return err
	}
	packedInf, err := decryption.EncodeECD(decryption.PackSimple(draft.Info), decryption.DefaultECDKey)
	if err != nil {
		return err
	}
	checkDat, err := mhfpallone.DecodeClientData(packedDat)
	if err != nil || !bytes.Equal(checkDat, draft.Data) {
		return fmt.Errorf("pallone-client: mhfdat container round-trip failed")
	}
	checkInf, err := mhfpallone.DecodeQuestInfoZZ(packedInf)
	if err != nil || !bytes.Equal(checkInf, draft.Info) {
		return fmt.Errorf("pallone-client: mhfinf container round-trip failed")
	}
	delivery := []mhfpallone.RouteDelivery{{Star: draft.Route.Star, Ordinal: draft.BankOrdinal}}
	// Event ID 1 is a serialization example, never an event registration.
	response, err := mhfpallone.EncodeRouteDelivery(1, delivery)
	if err != nil {
		return err
	}
	files[filepath.Join("client", "dat", "mhfdat.bin")] = packedDat
	files[filepath.Join("client", "dat", "mhfinf.bin")] = packedInf
	result := map[string]any{
		"kind": "offline_route_001_client_candidate", "live_client_verified": false,
		"event_enabled": false, "runtime_installed": false, "score_receipt_binding_verified": false,
		"native_route_key": draft.BankOrdinal, "overall_ranking_key": 0,
		"source_mhfdat_sha256": digest(dat), "source_mhfinf_sha256": digest(inf),
		"source_quest_bundle_manifest_sha256": digest(manifest),
		"route":                               draft.Route, "delivery_rows": delivery,
		"operation_3_example_event_id": 1, "operation_3_example_body_hex": hex.EncodeToString(response),
		"quest_text_encoding": "original Shift-JIS bytes, display unverified on localized client",
		"remaining":           []string{"departure and ordered-stage receipt capture", "live client menu/key verification and departure/transitions/result", "original spawn conditions and unknown donor fields", "event rewards"},
	}
	hashes := make(map[string]string)
	for name, data := range files {
		if expected[name] {
			name = filepath.Join("server", "quests", name)
		}
		hashes[filepath.ToSlash(name)] = digest(data)
	}
	result["files_sha256"] = hashes
	resultBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	// Complete validation/construction before creating any output directory.
	if err := os.MkdirAll(filepath.Dir(output), 0750); err != nil {
		return err
	}
	if err := os.Mkdir(output, 0750); err != nil {
		return err
	}
	for name, data := range files {
		if expected[name] {
			name = filepath.Join("server", "quests", name)
		}
		p := filepath.Join(output, name)
		if err := os.MkdirAll(filepath.Dir(p), 0750); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(output, "quest-bundle-manifest.json"), manifest, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "manifest.json"), append(resultBytes, '\n'), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "server", "pallone-client-profile.json"), append(resultBytes, '\n'), 0600); err != nil {
		return err
	}
	readme := fmt.Sprintf("OFFLINE RECONSTRUCTION CANDIDATE - NOT PLAYABLE-VERIFIED\n\nclient/dat: isolated ZZ mhfdat.bin + mhfinf.bin candidates.\nserver/quests: 24 audited reconstructed stage variants.\nserver/pallone-client-profile.json: matching server publication profile.\nNo files were installed. No event was registered or enabled.\nOnly original route 001 is selected for festival delivery (star 0, bank ordinal/key %d).\nAll existing G Caravan bank records keep their original bytes and positions.\nThe historical route is appended; overall-ranking key 0 stays separate.\nThe server publishes 001 only for a matching registered/selected event and matching stage hashes.\nThe server cannot verify which client assets a user installed.\nNative completion-receipt capture and rewards remain unarmed.\nQuest text remains original Shift-JIS; localized-client display is unverified.\nSee docs/pallone-festival.md for provenance and remaining validation.\n", draft.BankOrdinal)
	if err := os.WriteFile(filepath.Join(output, "README.txt"), []byte(readme), 0600); err != nil {
		return err
	}
	fmt.Printf("Created offline client candidate: %s (2 client files, 24 stage files; event disabled)\n", output)
	return nil
}

func isolatedOutput(output string, protected []string) error {
	if _, err := os.Lstat(output); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("pallone-client: output must be a new directory")
	}
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	parent, tail := filepath.Dir(out), filepath.Base(out)
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			out = filepath.Join(resolved, tail)
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(parent) == parent {
			return err
		}
		tail, parent = filepath.Join(filepath.Base(parent), tail), filepath.Dir(parent)
	}
	for _, dir := range protected {
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, out)
		if err != nil {
			// Different Windows volumes cannot contain one another.
			if !strings.EqualFold(filepath.VolumeName(root), filepath.VolumeName(out)) {
				continue
			}
			return err
		}
		if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("pallone-client: output must be outside input client/bundle directories")
		}
	}
	return nil
}
