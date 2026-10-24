package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientOutputCannotOverwriteInputsOrFollowAnInputJunction(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "client", "dat")
	if err := os.MkdirAll(input, 0750); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{input, filepath.Join(input, "candidate"), filepath.Join(input, "new", "candidate")} {
		if err := isolatedOutput(output, []string{input}); err == nil {
			t.Fatal("accepted input-directory output", output)
		}
	}
	if err := isolatedOutput(filepath.Join(root, "candidate"), []string{input}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-data")
	if err := os.Symlink(input, link); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if err := isolatedOutput(filepath.Join(link, "new", "candidate"), []string{input}); err == nil {
		t.Fatal("followed a link into the client input directory")
	}
}

func TestClientBuilderRejectsInvalidBundleBeforeCreatingOutput(t *testing.T) {
	root := t.TempDir()
	input, bundle, output := filepath.Join(root, "client"), filepath.Join(root, "bundle"), filepath.Join(root, "output")
	for _, dir := range []string{input, bundle} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	dat, inf := filepath.Join(input, "mhfdat.bin"), filepath.Join(input, "mhfinf.bin")
	for _, path := range []string{dat, inf} {
		if err := os.WriteFile(path, []byte("unchanged input"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte(`{"kind":"unverified"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(dat, inf, bundle, output); err == nil {
		t.Fatal("accepted invalid bundle")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("created output before validation", err)
	}
	for _, path := range []string{dat, inf} {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != "unchanged input" {
			t.Fatal("changed input", err)
		}
	}
}
