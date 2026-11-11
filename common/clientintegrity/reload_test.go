package clientintegrity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"sync"
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
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition timed out")
}

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

func TestWatcherOverwritesRenameRemovalRecoveryAndShutdown(t *testing.T) {
	m, _, old, _ := fixture(t)
	info, err := os.Stat(m.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	errorsSeen, successes := 0, 0
	report := func(added, total int, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errorsSeen++
		} else {
			successes++
		}
	}
	if err = m.watchManifest(5*time.Millisecond, report); err != nil {
		t.Fatal(err)
	}
	if err = m.WatchManifest(report); err == nil {
		t.Fatal("duplicate watcher")
	}
	// Equal length and preserved timestamps must not suppress a new build.
	sameLength := append([]byte(nil), old...)
	sameLength[0] ^= 1
	writeManifest(t, m.manifestPath, manifestBytes(t, sameLength))
	if err = os.Chtimes(m.manifestPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return hasBuild(m, sameLength) })
	writeManifest(t, m.manifestPath, []byte(`{"version":1`))
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return errorsSeen > 0 })
	if buildCount(m) != 2 || !hasBuild(m, old) {
		t.Fatal("partial upload removed old builds")
	}
	// Identical broken bytes are not reparsed/reported every tick.
	mu.Lock()
	count := errorsSeen
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	unchanged := errorsSeen == count
	mu.Unlock()
	if !unchanged {
		t.Fatal("invalid file log spam")
	}
	third := []byte("atomic replacement")
	temp := m.manifestPath + ".upload"
	writeManifest(t, temp, manifestBytes(t, third))
	if err = os.Rename(temp, m.manifestPath); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return hasBuild(m, third) })
	if err = os.Remove(m.manifestPath); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return errorsSeen > count })
	if buildCount(m) != 3 {
		t.Fatal("missing file cleared approvals")
	}
	writeManifest(t, m.manifestPath, manifestBytes(t, third))
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return successes >= 3 })
	m.Close()
	m.Close()
	select {
	case <-m.watchDone:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
	if _, _, err = m.addManifest(manifestBytes(t, []byte("after close"))); err == nil {
		t.Fatal("closed manager accepted update")
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
