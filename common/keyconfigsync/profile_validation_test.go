package keyconfigsync

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestHeaderBeforePayload(t *testing.T) {
	b := make([]byte, 80)
	copy(b, RequestMagic)
	binary.BigEndian.PutUint32(b[8:], 123)
	binary.BigEndian.PutUint32(b[12:], 456)
	copy(b[48:], Sign(b[:48], "0123456789abcdef"))
	if !AuthenticateHeader(b, "0123456789abcdef") {
		t.Fatal("header auth")
	}
	for i := range b {
		b[i] ^= 1
		if AuthenticateHeader(b, "0123456789abcdef") {
			t.Fatal("header tamper", i)
		}
		b[i] ^= 1
	}
	binary.BigEndian.PutUint32(b[16:], Save)
	binary.BigEndian.PutUint32(b[20:], 2)
	binary.BigEndian.PutUint32(b[28:], MaxIniBytes+1)
	if _, err := ParseHeader(b[:48]); err == nil {
		t.Fatal("oversized INI header accepted")
	}
}
func TestNativeProfileValidation(t *testing.T) {
	key := fixture(t, "keyconfig.cfg")
	ini := fixture(t, "mhf.ini")
	if !ValidProfile(0, key) || !ValidProfile(1, key) || !ValidProfile(2, ini) {
		t.Fatal("native fixtures rejected")
	}
	if !ValidProfile(2, []byte("[video]\nFPS_60=1\n")) {
		t.Fatal("Windows section case-insensitivity")
	}
	for _, bad := range [][]byte{
		[]byte("<keyconfig>"), []byte("<keyconfig>\x00</keyconfig>"), []byte("<keyconfig>" + string(bytes.Repeat([]byte{'x'}, MaxBytes-40)) + "</keyconfig>"),
		bytes.Replace(key, []byte("<keyboard"), []byte("<unknown"), 1), bytes.Replace(key, []byte("0x00000001"), []byte("a-command"), 1),
		bytes.Replace(key, []byte("</action>"), []byte(""), 1), append(append([]byte(nil), key...), []byte("meaningless trailer")...),
		bytes.Replace(key, []byte("\t\t\t<keydata key=\"0x00000001\" subkey=\"0x00000000\">\r\n"), nil, 1),
		append(bytes.Repeat([]byte("<?xml version=\"1.0\" ?>\n"), 100), key...),
	} {
		if ValidProfile(0, bad) {
			t.Fatal("malformed native profile accepted")
		}
	}
	for _, bad := range [][]byte{key, []byte("garbage"), []byte("[VIDEO]\nno equals"), []byte("[VIDEO]\nFPS_60=1\x00"), append(append([]byte(nil), ini...), bytes.Repeat([]byte{'x'}, MaxIniBytes)...), []byte("[VIDEO]\nFPS_60=" + string(bytes.Repeat([]byte{'x'}, 1025)))} {
		if ValidProfile(2, bad) {
			t.Fatal("malformed INI accepted")
		}
	}
	if !ValidProfile(2, append(ini, []byte("[FIXED_SENTENCE]\r\nTEXT_1=\x81\x82\r\n")...)) {
		t.Fatal("legacy text rejected")
	}
}
func TestInstalledProfileReadOnly(t *testing.T) {
	root := os.Getenv("PROFILE_FIXTURE_ROOT")
	if root == "" {
		t.Skip("optional read-only real-file fixture not supplied")
	}
	for i, name := range []string{"keyconfig.cfg", "mhf.ini"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		slot := uint32(0)
		if i == 1 {
			slot = 2
		}
		if !ValidProfile(slot, b) {
			t.Fatalf("real %s rejected", name)
		}
		t.Log(name, len(b))
	}
}
func FuzzProfileValidation(f *testing.F) {
	for _, name := range []string{"keyconfig.cfg", "mhf.ini"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint32(0), b)
		f.Add(uint32(2), b)
	}
	f.Fuzz(func(t *testing.T, slot uint32, b []byte) {
		if ValidProfile(slot, b) && (slot >= SlotCount || len(b) > int(Limit(slot))) {
			t.Fatal("accepted outside storage quota")
		}
	})
}
