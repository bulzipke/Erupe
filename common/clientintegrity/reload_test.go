package clientintegrity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func manifestBytes(t *testing.T, images ...[]byte) []byte {
	t.Helper()
	manifest := Manifest{Version: 1}
	for _, image := range images {
		id := sha256.Sum256(image)
		h := hex.EncodeToString(id[:])
		manifest.Builds = append(manifest.Builds, Build{h, h, image})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func writeManifest(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func hasBuild(m *Manager, image []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.builds[sha256.Sum256(image)]
	return ok
}
func buildCount(m *Manager) int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.builds) }
func TestReloadAddsDeduplicatesAndRestartUsesOnlyCurrentFile(t *testing.T) {
	m, _, old, _ := fixture(t)
	newImage := []byte("second approved image")
	raw := manifestBytes(t, newImage)
	writeManifest(t, m.manifestPath, raw)
	added, total, err := m.addManifest(raw)
	if err != nil || added != 1 || total != 2 {
		t.Fatalf("reload %d %d %v", added, total, err)
	}
	if !hasBuild(m, old) || !hasBuild(m, newImage) {
		t.Fatal("replacement lost a build")
	}
	added, total, err = m.addManifest(raw)
	if err != nil || added != 0 || total != 2 {
		t.Fatal("duplicate added again")
	}
	// No approval history or rewritten union is stored beside the source file.
	stored, err := os.ReadFile(m.manifestPath)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("source manifest was rewritten")
	}
	restarted, err := Load(m.manifestPath, m.signer, 60*time.Second, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if buildCount(restarted) != 1 || hasBuild(restarted, old) || !hasBuild(restarted, newImage) {
		t.Fatal("restart retained in-memory history")
	}
	for i := byte(0); i < 12; i++ {
		if _, _, err = m.addManifest(manifestBytes(t, []byte{i, 42})); err != nil {
			t.Fatal(err)
		}
	}
	if buildCount(m) != 14 || !hasBuild(m, old) {
		t.Fatal("cumulative builds were capped or evicted")
	}
}

func TestReloadRejectsEntireInvalidDocument(t *testing.T) {
	m, _, old, _ := fixture(t)
	newImage := []byte("not yet accepted")
	var manifest Manifest
	json.Unmarshal(manifestBytes(t, newImage, []byte("bad image")), &manifest)
	manifest.Builds[1].ID = "invalid"
	raw, _ := json.Marshal(manifest)
	for _, b := range [][]byte{raw, []byte(`{"version":1,"builds":`), append(manifestBytes(t, newImage), []byte(` {}`)...), manifestBytes(t, newImage, newImage)} {
		if _, _, err := m.addManifest(b); err == nil {
			t.Fatal("invalid document accepted")
		}
		if buildCount(m) != 1 || !hasBuild(m, old) || hasBuild(m, newImage) {
			t.Fatal("invalid update partially published")
		}
	}
}

func TestReloadHookChecksFileDigestBeforeAccepting(t *testing.T) {
	m, _, old, _ := fixture(t)
	handler := m.reloadHandler(func(added, total int, err error) {})
	request := func(method, host, path string, raw []byte, origin string) *httptest.ResponseRecorder {
		hash := sha256.Sum256(raw)
		body, _ := json.Marshal(reloadRequest{SHA256: hex.EncodeToString(hash[:])})
		r := httptest.NewRequest(method, "http://"+host+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, reloadAddress, "/ready", nil, "").Code; got != http.StatusNoContent {
		t.Fatalf("ready status %d", got)
	}
	updated := []byte("build installed by operator")
	raw := manifestBytes(t, updated)
	writeManifest(t, m.manifestPath, raw)
	if hasBuild(m, updated) {
		t.Fatal("file accepted before reload hook")
	}

	for _, tc := range []struct {
		host, origin string
		raw          []byte
		status       int
	}{
		{"public.example", "", raw, http.StatusForbidden},
		{reloadAddress, "http://public.example", raw, http.StatusForbidden},
		{reloadAddress, "", []byte("different file"), http.StatusConflict},
	} {
		if got := request(http.MethodPost, tc.host, "/reload", tc.raw, tc.origin).Code; got != tc.status {
			t.Fatalf("reload status %d, want %d", got, tc.status)
		}
		if hasBuild(m, updated) {
			t.Fatal("rejected hook added a build")
		}
	}
	invalid := []byte(`{"version":1,"builds":[]}`)
	writeManifest(t, m.manifestPath, invalid)
	if got := request(http.MethodPost, reloadAddress, "/reload", invalid, "").Code; got != http.StatusUnprocessableEntity || buildCount(m) != 1 {
		t.Fatalf("invalid manifest status=%d builds=%d", got, buildCount(m))
	}
	writeManifest(t, m.manifestPath, raw)
	for _, want := range []int{1, 0} {
		w := request(http.MethodPost, reloadAddress, "/reload", raw, "")
		var receipt reloadReceipt
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &receipt) != nil ||
			receipt.SHA256 != fmt.Sprintf("%x", sha256.Sum256(raw)) || receipt.AddedBuilds != want || receipt.AcceptedBuilds != 2 {
			t.Fatalf("reload status=%d receipt=%+v", w.Code, receipt)
		}
	}
	if !hasBuild(m, old) || !hasBuild(m, updated) {
		t.Fatal("accepted builds missing")
	}
}

func TestReloadPreservesLiveOldSessionAndAdmitsNewBuild(t *testing.T) {
	m, h, old, key := fixture(t)
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- m.Serve(server, h, testToken) }()
	round(t, client, old, key)
	newImage := []byte("new live build")
	if _, _, err := m.addManifest(manifestBytes(t, newImage)); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	lease := m.sessions[SessionKey(123, 456, testToken)]
	m.mu.Unlock()
	lease.wake <- struct{}{}
	round(t, client, old, key) // In-flight sessions still measure against their own immutable image.
	raw := append([]byte(nil), h.Raw...)
	raw[15]++ // Distinct authenticated character fixture.
	id := sha256.Sum256(newImage)
	copy(raw[32:64], id[:])
	copy(raw[64:], mac(raw[:64], testToken))
	next, err := ParseHello(raw)
	if err != nil {
		t.Fatal(err)
	}
	s2, c2 := net.Pipe()
	defer c2.Close()
	done2 := make(chan error, 1)
	go func() { done2 <- m.Serve(s2, next, testToken) }()
	round(t, c2, newImage, key)
	m.Close()
	wait(t, done)
	wait(t, done2)
}

func TestConcurrentReloadAndHandshake(t *testing.T) {
	m, h, old, key := fixture(t)
	raw := manifestBytes(t, []byte("concurrently accepted build"))
	result := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if _, _, err := m.addManifest(raw); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}()
	for i := 0; i < 5; i++ {
		s, c := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- m.Serve(s, h, testToken) }()
		round(t, c, old, key)
		c.Close()
		wait(t, done)
	}
	if err := wait(t, result); err != nil {
		t.Fatal(err)
	}
	if buildCount(m) != 2 {
		t.Fatal("concurrent duplicate growth")
	}
}
