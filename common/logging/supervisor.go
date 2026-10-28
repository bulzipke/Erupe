package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const supervisedEnv = "ERUPE_LOG_SUPERVISED"

// Supervise re-executes the server once and captures its raw stderr in a parent
// process. The caller exits with the returned code when handled is true. The
// parent waits for all stderr bytes before exiting and forwards shutdown signals
// to the server, which remains responsible for its usual countdown/drain/save.
//
// An in-process tee can die before draining panic output. SetCrashOutput also
// misses the fatal error message on Go 1.25 (golang/go#76859). Capturing stderr
// outside the crashing process preserves both the cause and complete stack.
func Supervise(o Options) (code int, handled bool, err error) {
	if os.Getenv(supervisedEnv) == "1" {
		// Do not accidentally propagate the bypass to other programs we launch.
		if err := os.Unsetenv(supervisedEnv); err != nil {
			return 1, true, err
		}
		return 0, false, nil
	}
	if !o.Enabled || !o.CaptureCrashes {
		return 0, false, nil
	}
	if err := o.Validate(); err != nil {
		return 1, true, err
	}
	crash, err := openCrashFile(o)
	if err != nil {
		return 1, true, err
	}
	defer func() {
		if err := crash.Close(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Close crash output: %v\n", err)
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return 1, true, err
	}
	cmd := exec.Command(executable, os.Args[1:]...)
	cmd.Env = append(os.Environ(), supervisedEnv+"=1")
	cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
	stderr := &crashWriter{console: os.Stderr, file: crash}
	cmd.Stderr = stderr

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return 1, true, fmt.Errorf("start supervised server: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-signals:
			if err := cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
				_, _ = fmt.Fprintf(os.Stderr, "Forward shutdown signal: %v\n", err)
			}
		case err := <-done:
			// os/exec.Wait also waits for its stderr copy goroutine to reach EOF.
			if err == nil {
				return 0, true, nil
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code := exit.ExitCode()
				if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					code = 128 + int(status.Signal())
				}
				_, _ = fmt.Fprintf(stderr, "\n%s Erupe process %d exited with status %d\n",
					time.Now().UTC().Format(time.RFC3339Nano), cmd.Process.Pid, code)
				return code, true, nil
			}
			return 1, true, fmt.Errorf("wait for supervised server: %w", err)
		}
	}
}

// Keep draining even if storage fails: returning a write error to os/exec would
// close the pipe and could interrupt the server. Report the failure once and
// continue preserving the other destination, especially Docker console output.
type crashWriter struct {
	console, file io.Writer
	once          sync.Once
}

func (w *crashWriter) Write(p []byte) (int, error) {
	n, fileErr := w.file.Write(p)
	if fileErr != nil || n != len(p) {
		w.once.Do(func() {
			_, _ = fmt.Fprintf(w.console, "Failed to save crash output: wrote %d/%d bytes: %v\n", n, len(p), fileErr)
		})
	}
	_, _ = w.console.Write(p)
	return len(p), nil
}
