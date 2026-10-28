// Package logging keeps Docker console output and persistent rotating logs.
package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Options applies the same retention policy independently to server and crash logs.
type Options struct {
	Enabled        bool
	Directory      string
	MaxSizeMB      int
	MaxBackups     int
	MaxAgeDays     int
	Compress       bool
	CaptureCrashes bool
}

func DefaultOptions() Options {
	return Options{Enabled: true, Directory: "logs", MaxSizeMB: 50,
		MaxBackups: 10, MaxAgeDays: 14, Compress: true, CaptureCrashes: true}
}

func (o Options) Validate() error {
	if !o.Enabled {
		return nil
	}
	if o.Directory == "" || o.MaxSizeMB < 1 || o.MaxSizeMB > 1024 ||
		o.MaxBackups < 1 || o.MaxAgeDays < 1 || o.MaxAgeDays > 36500 {
		return fmt.Errorf("Logging requires a directory, MaxSizeMB 1..1024, MaxBackups >= 1 and MaxAgeDays 1..36500")
	}
	return nil
}

type Logs struct {
	Logger *zap.Logger
	Stdout io.Writer
	Stderr io.Writer
	file   *lumberjack.Logger
}

func rollingFile(o Options, name string) *lumberjack.Logger {
	return &lumberjack.Logger{Filename: filepath.Join(o.Directory, name),
		MaxSize: o.MaxSizeMB, MaxBackups: o.MaxBackups, MaxAge: o.MaxAgeDays,
		Compress: o.Compress}
}

// New runs before starting servers. Failure to open the configured files is a
// startup error, rather than silently running without the requested evidence.
func New(o Options) (*Logs, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	l := &Logs{Stdout: os.Stdout, Stderr: os.Stderr}
	if o.Enabled {
		if err := os.MkdirAll(o.Directory, 0700); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
		l.file = rollingFile(o, "erupe.log")
		// Open now to report permissions/disk errors before opening listeners.
		if _, err := l.file.Write(nil); err != nil {
			_ = l.file.Close()
			return nil, fmt.Errorf("open server log: %w", err)
		}
		l.Stdout = mirrorWriter{os.Stdout, l.file}
		l.Stderr = mirrorWriter{os.Stderr, l.file}
	}
	zapConfig := zap.NewDevelopmentConfig()
	if o.Enabled && o.CaptureCrashes {
		// The supervisor reserves raw stderr for crash/diagnostic output.
		// Ordinary messages still appear in the Docker console via stdout.
		zapConfig.OutputPaths = []string{"stdout"}
	}
	logger, err := zapConfig.Build(zap.WrapCore(func(console zapcore.Core) zapcore.Core {
		if l.file == nil {
			return console
		}
		return zapcore.NewTee(console, zapcore.NewCore(
			zapcore.NewConsoleEncoder(zapConfig.EncoderConfig), zapcore.AddSync(l.file), zap.DebugLevel))
	}))
	if err != nil {
		if l.file != nil {
			_ = l.file.Close()
		}
		return nil, err
	}
	l.Logger = logger
	return l, nil
}

func openCrashFile(o Options) (*lumberjack.Logger, error) {
	if err := os.MkdirAll(o.Directory, 0700); err != nil {
		return nil, fmt.Errorf("create crash log directory: %w", err)
	}
	// Begin a fresh report after restart, and also cap stderr growth while the
	// server runs. This writer lives in the supervisor, outside the crash.
	rotator := rollingFile(o, "crash.log")
	info, err := os.Stat(rotator.Filename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect crash log: %w", err)
	}
	if info != nil && info.Size() > 0 {
		if err := rotator.Rotate(); err != nil {
			_ = rotator.Close()
			return nil, fmt.Errorf("archive previous crash log: %w", err)
		}
	} else if _, err := rotator.Write(nil); err != nil {
		_ = rotator.Close()
		return nil, fmt.Errorf("open crash log: %w", err)
	}
	return rotator, nil
}

// Close flushes/closes the ordinary log; crash output is owned by the supervisor.
func (l *Logs) Close() error {
	if l.Logger != nil {
		// Some terminals do not support Sync; file writes are unbuffered.
		_ = l.Logger.Sync()
	}
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// Always attempt both destinations, even if the console or disk write fails.
type mirrorWriter struct{ console, file io.Writer }

func (w mirrorWriter) Write(p []byte) (int, error) {
	n, consoleErr := w.console.Write(p)
	m, fileErr := w.file.Write(p)
	if n != len(p) && consoleErr == nil {
		consoleErr = io.ErrShortWrite
	}
	if m != len(p) && fileErr == nil {
		fileErr = io.ErrShortWrite
	}
	return min(n, m), errors.Join(consoleErr, fileErr)
}
