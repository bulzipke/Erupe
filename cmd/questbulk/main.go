// questbulk creates reviewable SQL files offline; it has no DB connection mode.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"erupe-ce/common/questbulk"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("questbulk", flag.ContinueOnError)
	input := f.String("input", "", "reviewed bulk plan JSON")
	output := f.String("output-dir", "", "new output directory (must not exist)")
	sourceRoot := f.String("source-root", ".", "base directory for relative source-evidence paths")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *input == "" || *output == "" {
		return fmt.Errorf("-input and -output-dir are required")
	}
	info, err := os.Stat(*input)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return fmt.Errorf("plan must be a regular file up to 16 MiB")
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	p, err := questbulk.Parse(data)
	if err != nil {
		return err
	}
	if err := verifySources(p, *sourceRoot); err != nil {
		return err
	}
	bundle, err := questbulk.Build(p)
	if err != nil {
		return err
	}
	if err := os.Mkdir(*output, 0755); err != nil {
		return err
	}
	type entry struct {
		Name   string `json:"name"`
		Bytes  int    `json:"bytes"`
		SHA256 string `json:"sha256"`
	}
	entries := []entry{}
	for _, item := range []struct{ name, body string }{{"rehearse.sql", bundle.RehearseSQL}, {"apply.sql", bundle.ApplySQL}, {"rollback.sql", bundle.RollbackSQL}} {
		path := filepath.Join(*output, item.name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(file, item.body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		hash := sha256.Sum256([]byte(item.body))
		entries = append(entries, entry{item.name, len(item.body), hex.EncodeToString(hash[:])})
	}
	// A failed preparation is incomplete. Recheck evidence and the exact saved
	// SQL bytes before writing the final manifest, which contains expected hashes.
	if err := verifySources(p, *sourceRoot); err != nil {
		return err
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(*output, entry.Name))
		if err != nil {
			return err
		}
		hash := sha256.Sum256(raw)
		if len(raw) != entry.Bytes || hex.EncodeToString(hash[:]) != entry.SHA256 {
			return fmt.Errorf("saved SQL changed before manifest: %s", entry.Name)
		}
	}
	manifest := struct {
		Schema           string  `json:"schema"`
		Complete         bool    `json:"complete"`
		PlanSHA256       string  `json:"plan_sha256"`
		BackupTable      string  `json:"backup_table"`
		Activated        int     `json:"activated"`
		Added            int     `json:"added"`
		Existing         int     `json:"existing"`
		DatabaseExecuted bool    `json:"database_executed"`
		Limits           string  `json:"limits"`
		Files            []entry `json:"files"`
	}{"erupe-quest-bulk-sql-bundle-v1", true, bundle.PlanSHA256, bundle.BackupTable, len(p.ActivateQuestIDs), len(p.Additions), len(p.Existing), false, "SQL only. Managed publication schedules and original eligibility are unchanged. Listing metadata and binary/NAS evidence still need roster review.", entries}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(filepath.Join(*output, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	_, err = fmt.Fprintf(stdout, "Prepared %d quests (%d additions, %d existing); no database connection.\n", len(p.ActivateQuestIDs), len(p.Additions), len(p.Existing))
	return err
}

func verifySources(p questbulk.Plan, root string) error {
	sources := []questbulk.Source{p.SnapshotSource}
	for _, addition := range p.Additions {
		sources = append(sources, addition.Evidence)
	}
	seen := map[string]questbulk.Source{}
	for _, source := range sources {
		path := source.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if previous, ok := seen[path]; ok {
			if previous.Bytes != source.Bytes || previous.SHA256 != source.SHA256 {
				return fmt.Errorf("conflicting source fingerprints: %s", source.Path)
			}
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return statErr
		}
		if !info.Mode().IsRegular() || info.Size() != source.Bytes {
			file.Close()
			return fmt.Errorf("source size/type changed: %s", source.Path)
		}
		hash := sha256.New()
		count, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if count != source.Bytes || hex.EncodeToString(hash.Sum(nil)) != source.SHA256 {
			return fmt.Errorf("source SHA256 changed: %s", source.Path)
		}
		seen[path] = source
	}
	return nil
}
