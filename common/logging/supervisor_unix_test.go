//go:build unix

package logging

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSupervisorSignalHelper(t *testing.T) {
	if os.Getenv("ERUPE_LOG_SIGNAL_HELPER") == "" {
		return
	}
	o := DefaultOptions()
	o.Directory = os.Getenv("ERUPE_LOG_TEST_DIRECTORY")
	if code, handled, err := Supervise(o); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := os.WriteFile(filepath.Join(o.Directory, "ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	<-signals
	_, _ = fmt.Fprintln(os.Stderr, "shutdown-drain-complete")
	os.Exit(23)
}

func TestSupervisorForwardsSignalAndPreservesExitStatus(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorSignalHelper$")
	cmd.Env = append(os.Environ(), "ERUPE_LOG_SIGNAL_HELPER=1", "ERUPE_LOG_TEST_DIRECTORY="+dir)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	}()
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "ready"))
		return err == nil
	})
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err == nil || cmd.ProcessState.ExitCode() != 23 {
		t.Fatalf("exit status not preserved: %v: %s", err, output.Bytes())
	}
	data, err := os.ReadFile(filepath.Join(dir, "crash.log"))
	if err != nil || !bytes.Contains(data, []byte("shutdown-drain-complete")) {
		t.Fatalf("final stderr lost after forwarded shutdown: %v: %s", err, data)
	}
}
