package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// replaceScheduleFile finishes and syncs a same-directory temporary file before
// replacing the destination. A write/sync/close/replace error leaves the old
// destination intact. Replacement guarantees still depend on the OS/filesystem.
func replaceScheduleFile(target string, write func(*os.File) error) error {
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("schedule output must be a regular file, not a directory or symbolic link")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".questschedule-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := write(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}

func writeScheduleFile(target string, data []byte) error {
	return replaceScheduleFile(target, func(f *os.File) error {
		n, err := f.Write(data)
		if err == nil && n != len(data) {
			return io.ErrShortWrite
		}
		return err
	})
}
