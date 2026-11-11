package clientintegrity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

const maxManifestBytes = 48 * 1024 * 1024
const reloadAddress = "127.0.0.1:53311"

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

// StartReloadHook serves operator requests on the Erupe network namespace's
// loopback. The NAS deploy tool reaches it through the existing SSH login.
func (m *Manager) StartReloadHook(report func(added, total int, err error)) (*http.Server, error) {
	listener, err := net.Listen("tcp4", reloadAddress)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Handler:           m.reloadHandler(report),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    4096,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			report(0, 0, err)
		}
	}()
	return server, nil
}

type reloadRequest struct {
	SHA256 string `json:"sha256"`
}

type reloadReceipt struct {
	SHA256         string `json:"sha256"`
	AddedBuilds    int    `json:"addedBuilds"`
	AcceptedBuilds int    `json:"acceptedBuilds"`
}

func (m *Manager) reloadHandler(report func(added, total int, err error)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
		d.DisallowUnknownFields()
		var req reloadRequest
		if err := d.Decode(&req); err != nil || len(req.SHA256) != 64 || d.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "expected manifest SHA-256", http.StatusBadRequest)
			return
		}
		if _, err := hex.DecodeString(req.SHA256); err != nil {
			http.Error(w, "invalid SHA-256", http.StatusBadRequest)
			return
		}
		raw, err := readManifest(m.manifestPath)
		if err != nil {
			report(0, 0, err)
			http.Error(w, "manifest unavailable", http.StatusUnprocessableEntity)
			return
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != req.SHA256 {
			http.Error(w, "manifest hash mismatch", http.StatusConflict)
			return
		}
		added, total, err := m.addManifest(raw)
		report(added, total, err)
		if err != nil {
			http.Error(w, "manifest rejected", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reloadReceipt{req.SHA256, added, total})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject requests routed to loopback using a foreign browser origin.
		if r.Host != reloadAddress || r.Header.Get("Origin") != "" {
			http.Error(w, "loopback operator requests only", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
