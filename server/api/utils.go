package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

func inTrustedRoot(path string, trustedRoot string) error {
	relative, err := filepath.Rel(filepath.Clean(trustedRoot), filepath.Clean(path))
	if err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative) {
		return nil
	}
	return errors.New("path is outside of trusted root")
}

func verifyPath(path string, trustedRoot string, logger *zap.Logger) (string, error) {
	c, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		logger.Warn("Path verification failed", zap.Error(err))
		return filepath.Clean(path), errors.New("unsafe or invalid path specified")
	}
	root, err := filepath.Abs(filepath.Clean(trustedRoot))
	if err != nil {
		logger.Warn("Trusted root verification failed", zap.Error(err))
		return c, errors.New("unsafe or invalid path specified")
	}
	logger.Debug("Cleaned path", zap.String("path", c))

	if err := inTrustedRoot(c, root); err != nil {
		logger.Warn("Path outside trusted root", zap.Error(err))
		return c, errors.New("unsafe or invalid path specified")
	}

	// Reject symlinks below the configured root. This works for both existing
	// files and new output paths, without requiring EvalSymlinks to succeed on
	// every ancestor (which can return access denied on Windows temp roots).
	relative, err := filepath.Rel(root, c)
	if err != nil {
		logger.Warn("Path verification failed", zap.Error(err))
		return c, errors.New("unsafe or invalid path specified")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			logger.Warn("Path verification failed", zap.Error(statErr))
			return c, errors.New("unsafe or invalid path specified")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			logger.Warn("Symlink path rejected", zap.String("path", current))
			return c, errors.New("unsafe or invalid path specified")
		}
	}
	return c, nil
}
