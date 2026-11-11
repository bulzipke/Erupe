package clientintegrity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

const maxManifestBytes = 48 * 1024 * 1024
const manifestPollInterval = time.Second

func readManifest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return nil, errors.New("integrity: manifest must be a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxManifestBytes {
		return nil, errors.New("integrity: manifest too large")
	}
	return raw, nil
}

// Validate the entire document before publishing ANY new build. In particular,
// a valid first entry followed by a corrupt second entry cannot partially apply.
func decodeManifest(raw []byte) (map[[32]byte][]byte, error) {
	var manifest Manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&manifest); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("integrity: trailing manifest data")
	}
	if manifest.Version != 1 || len(manifest.Builds) == 0 || len(manifest.Builds) > 8 {
		return nil, errors.New("integrity: invalid manifest")
	}
	builds := make(map[[32]byte][]byte, len(manifest.Builds))
	for _, b := range manifest.Builds {
		id := sha256.Sum256(b.Image)
		fileHash, err := hex.DecodeString(b.FileSHA256)
		if len(b.Image) == 0 || len(b.Image) > 4*1024*1024 || hex.EncodeToString(id[:]) != b.ID || err != nil || len(fileHash) != 32 {
			return nil, errors.New("integrity: invalid build image/digest")
		}
		if _, exists := builds[id]; exists {
			return nil, errors.New("integrity: duplicate build")
		}
		builds[id] = b.Image
	}
	return builds, nil
}

func (m *Manager) addManifest(raw []byte) (added, total int, err error) {
	builds, err := decodeManifest(raw)
	if err != nil {
		return 0, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, len(m.builds), errors.New("integrity: manager closed")
	}
	// Published images are immutable and never replaced or evicted. Existing
	// sessions keep their image and pending challenge throughout the update.
	for id, image := range builds {
		if _, exists := m.builds[id]; !exists {
			m.builds[id] = image
			added++
		}
	}
	return added, len(m.builds), nil
}

// WatchManifest adds approved builds without restarting or dropping sessions.
// It only reads ManifestFile; the accumulated history lives in memory and is
// intentionally lost on restart. The startup file must still be valid.
// Full-content hashes also catch same-size/same-timestamp replacements and
// work on network mounts where native change notifications may be absent.
func (m *Manager) WatchManifest(report func(added, total int, err error)) error {
	return m.watchManifest(manifestPollInterval, report)
}

func (m *Manager) watchManifest(interval time.Duration, report func(added, total int, err error)) error {
	m.mu.Lock()
	if m.closed || m.watchDone != nil || interval <= 0 {
		m.mu.Unlock()
		return errors.New("integrity: watcher already started, closed, or invalid interval")
	}
	m.watchDone = make(chan struct{})
	m.mu.Unlock()
	go func() {
		defer close(m.watchDone)
		timer := time.NewTicker(interval)
		defer timer.Stop()
		lastHash := m.manifestHash
		lastReadOK := true
		lastError := ""
		for {
			select {
			case <-m.stopped:
				return
			case <-timer.C:
			}
			raw, err := readManifest(m.manifestPath)
			added, total := 0, 0
			if err == nil {
				digest := sha256.Sum256(raw)
				if lastReadOK && digest == lastHash {
					continue
				}
				lastHash = digest
				lastReadOK = true
				added, total, err = m.addManifest(raw)
			} else {
				lastReadOK = false
			}
			// A stop racing a file read may not publish through addManifest, and must
			// not report a normal shutdown as a rejected operator update.
			select {
			case <-m.stopped:
				return
			default:
			}
			if err != nil {
				if err.Error() != lastError && report != nil {
					report(0, 0, err)
				}
				lastError = err.Error()
			} else {
				if (added > 0 || lastError != "") && report != nil {
					report(added, total, nil)
				}
				lastError = ""
			}
		}
	}()
	return nil
}
