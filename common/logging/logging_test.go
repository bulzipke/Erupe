package logging

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	o := DefaultOptions()
	o.Directory = t.TempDir()
	o.CaptureCrashes = false // Process-global runtime hooks are tested in subprocesses.
	return o
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("log retention/compression did not finish")
}

func TestServerLogsAndConcurrentWriters(t *testing.T) {
	o := testOptions(t)
	l, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for i := range 20 {
				l.Logger.Named("channel").Info(fmt.Sprintf("zap-%d-%d", worker, i))
				if _, err := fmt.Fprintf(l.Stdout, "http-%d-%d\n", worker, i); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(o.Directory, "erupe.log"))
	if err != nil {
		t.Fatal(err)
	}
	for worker := range 8 {
		for i := range 20 {
			for _, prefix := range []string{"zap", "http"} {
				if !bytes.Contains(data, []byte(fmt.Sprintf("%s-%d-%d\n", prefix, worker, i))) {
					t.Errorf("missing %s-%d-%d", prefix, worker, i)
				}
			}
		}
	}
}

func TestSizeRotationCompressionAndRetention(t *testing.T) {
	o := testOptions(t)
	o.MaxSizeMB, o.MaxBackups = 1, 2
	l, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	payload := bytes.Repeat([]byte("x"), 600*1024)
	for range 5 {
		if _, err := l.file.Write(payload); err != nil {
			t.Fatal(err)
		}
		// Lumberjack backup names have millisecond precision.
		time.Sleep(5 * time.Millisecond)
	}
	var backups []string
	waitFor(t, func() bool {
		backups, _ = filepath.Glob(filepath.Join(o.Directory, "erupe-*.log*"))
		return len(backups) == 2 && strings.HasSuffix(backups[0], ".gz") && strings.HasSuffix(backups[1], ".gz")
	})
	for _, name := range backups {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		z, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		data, err := io.ReadAll(z)
		_ = z.Close()
		_ = f.Close()
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("rotated content was lost: %v", err)
		}
	}
	info, err := os.Stat(filepath.Join(o.Directory, "erupe.log"))
	if err != nil || info.Size() > 1024*1024 {
		t.Fatalf("active log exceeds size limit: %v", err)
	}
}

func TestOldBackupsArePrunedOnStartup(t *testing.T) {
	o := testOptions(t)
	old := filepath.Join(o.Directory, "erupe-2000-01-01T00-00-00.000.log")
	if err := os.WriteFile(old, []byte("expired"), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	waitFor(t, func() bool {
		files, _ := filepath.Glob(old + "*")
		return len(files) == 0
	})
}

func TestCrashOutputHasSizeAndBackupLimits(t *testing.T) {
	o := testOptions(t)
	o.MaxSizeMB, o.MaxBackups, o.Compress = 1, 2, false
	w, err := openCrashFile(o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	payload := bytes.Repeat([]byte("stderr"), 100*1024)
	for range 5 {
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(t, func() bool {
		backups, _ := filepath.Glob(filepath.Join(o.Directory, "crash-*.log"))
		return len(backups) == 2
	})
	info, err := os.Stat(filepath.Join(o.Directory, "crash.log"))
	if err != nil || info.Size() > 1024*1024 {
		t.Fatalf("raw stderr was not size-limited: %v", err)
	}
}

func TestUnwritableDestinationFailsAndDisabledCreatesNothing(t *testing.T) {
	o := testOptions(t)
	blocker := filepath.Join(o.Directory, "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	o.Directory = filepath.Join(blocker, "logs")
	if _, err := New(o); err == nil {
		t.Fatal("expected startup failure for an unusable directory")
	}
	o.Enabled = false
	l, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMirrorKeepsOtherDestinationOnWriteFailure(t *testing.T) {
	var console, file bytes.Buffer
	if _, err := (mirrorWriter{failedWriter{}, &file}).Write([]byte("disk survives")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if _, err := (mirrorWriter{&console, failedWriter{}}).Write([]byte("console survives")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if file.String() != "disk survives" || console.String() != "console survives" {
		t.Fatal("one failed destination prevented the other from receiving the log")
	}
}

func TestCrashWriterDrainsAfterStorageFailure(t *testing.T) {
	var console bytes.Buffer
	w := &crashWriter{console: &console, file: failedWriter{}}
	for _, message := range []string{"first-report", "last-stack-line"} {
		if n, err := w.Write([]byte(message)); err != nil || n != len(message) {
			t.Fatalf("stderr reader would stop prematurely: %d, %v", n, err)
		}
	}
	if !strings.Contains(console.String(), "first-reportlast-stack-line") ||
		strings.Count(console.String(), "Failed to save crash output") != 1 {
		t.Fatal("console fallback lost stderr or repeatedly reported the same storage failure")
	}
}

// This helper deliberately terminates its own test process. It never runs the
// game server or connects to a production database.
func TestCrashHelper(t *testing.T) {
	mode := os.Getenv("ERUPE_LOG_CRASH_HELPER")
	if mode == "" {
		return
	}
	o := DefaultOptions()
	o.Directory = os.Getenv("ERUPE_LOG_TEST_DIRECTORY")
	o.Compress = false
	if code, handled, err := Supervise(o); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
	l, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	l.Logger.Info("before-crash")
	defer func() { _ = l.Close() }()
	switch mode {
	case "panic":
		go func() { panic("logging-panic-evidence") }()
		select {}
	case "main-panic":
		panic("logging-panic-evidence")
	case "fatal":
		var mutex sync.Mutex
		mutex.Unlock() // fatal runtime error; recover cannot intercept it.
	case "clean":
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeCrashOutputAndRestartArchive(t *testing.T) {
	for _, mode := range []string{"panic", "main-panic", "fatal"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			run := func(mode string) ([]byte, error) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
				cmd.Env = append(os.Environ(), "ERUPE_LOG_CRASH_HELPER="+mode,
					"ERUPE_LOG_TEST_DIRECTORY="+dir, "GOTRACEBACK=single")
				return cmd.CombinedOutput()
			}
			console, err := run(mode)
			if err == nil {
				t.Fatal("expected helper process to crash")
			}
			crashPath := filepath.Join(dir, "crash.log")
			data, err := os.ReadFile(crashPath)
			if err != nil {
				t.Fatalf("%v: %s", err, console)
			}
			marker := "logging-panic-evidence"
			if mode == "fatal" {
				marker = "sync: unlock of unlocked mutex"
			}
			if !bytes.Contains(data, []byte(marker)) || !bytes.Contains(data, []byte("goroutine")) ||
				!bytes.Contains(console, []byte(marker)) {
				t.Fatalf("crash evidence missing from file or console: %s / %s", data, console)
			}
			if output, err := run("clean"); err != nil {
				t.Fatalf("restart failed: %v: %s", err, output)
			}
			archives, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
			if len(archives) != 1 {
				t.Fatalf("expected previous crash archive, got %v", archives)
			}
			archived, err := os.ReadFile(archives[0])
			if err != nil || !bytes.Equal(archived, data) {
				t.Fatal("restart did not preserve the complete crash report")
			}
			active, err := os.ReadFile(crashPath)
			if err != nil || len(active) != 0 {
				t.Fatalf("new crash output was not empty: %v", err)
			}
		})
	}
}
