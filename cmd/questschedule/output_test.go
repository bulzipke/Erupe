package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func assertNoScheduleTemps(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".questschedule-*.tmp"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("staged export leaked temporary files: %v, %v", paths, err)
	}
}

func TestScheduleFileStagingKeepsPreviousExportOnFailure(t *testing.T) {
	sentinel := errors.New("injected partial write failure")
	for _, failure := range []string{"partial write", "sync after close"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "calendar.json")
			before := []byte("previous complete schedule\n")
			if err := os.WriteFile(target, before, 0o600); err != nil {
				t.Fatal(err)
			}
			err := replaceScheduleFile(target, func(f *os.File) error {
				if _, err := f.WriteString("incomplete replacement"); err != nil {
					return err
				}
				if failure == "partial write" {
					return sentinel
				}
				return f.Close()
			})
			if err == nil || (failure == "partial write" && !errors.Is(err, sentinel)) {
				t.Fatalf("injected failure lost: %v", err)
			}
			assertInputUnchanged(t, target, before)
			assertNoScheduleTemps(t, dir)
		})
	}
}

func TestScheduleFileStagingCreatesAndReplacesCompleteOutput(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "calendar.json")
	for _, value := range []string{"first complete schedule\n", "replacement complete schedule\n"} {
		if err := writeScheduleFile(target, []byte(value)); err != nil {
			t.Fatal(err)
		}
		assertInputUnchanged(t, target, []byte(value))
		assertNoScheduleTemps(t, dir)
	}
}

func TestScheduleFileStagingRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	called := false
	if err := replaceScheduleFile(dir, func(*os.File) error { called = true; return nil }); err == nil || called {
		t.Fatalf("directory destination was accepted or writer called: %v", err)
	}
	assertNoScheduleTemps(t, filepath.Dir(dir))
}
