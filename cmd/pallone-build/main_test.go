package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCannotOverwriteRuntimeOrExistingBundle(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{bin, filepath.Join(bin, "draft"), filepath.Join(bin, "nested", "draft"), root} {
		if err := validateOutput(bin, out); err == nil {
			t.Fatalf("accepted runtime/existing output %s", out)
		}
	}
	if err := validateOutput(bin, filepath.Join(root, "new-bundle")); err != nil {
		t.Fatal(err)
	}
}

func TestOutputRejectsJunctionIntoRuntime(t *testing.T) {
	root := t.TempDir()
	bin, link := filepath.Join(root, "bin"), filepath.Join(root, "alias")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := validateOutput(bin, filepath.Join(link, "draft")); err == nil {
		t.Fatal("accepted symlink into runtime")
	}
}
