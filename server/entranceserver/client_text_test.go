package entranceserver

import (
	"bytes"
	"strings"
	"testing"

	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

func TestEncodeServerInfoKoreanTextFitsWireField(t *testing.T) {
	for _, mode := range []cfg.Mode{cfg.G1, cfg.ZZ} {
		for _, longName := range []bool{false, true} {
			name := "한글"
			if longName {
				name = strings.Repeat("한", 200)
			}
			config := &cfg.Config{
				RealClientMode: mode,
				Entrance: cfg.Entrance{Entries: []cfg.EntranceServerInfo{{
					Name: name, Description: strings.Repeat("한", 200),
				}}},
			}
			server := &Server{logger: zap.NewNop(), erupeConfig: config}
			data := encodeServerInfo(config, server, true)
			// IP, server ID, unknown, channel count, type, icon, recommended.
			const header = 4 + 2 + 2 + 2 + 1 + 1 + 1
			var text []byte
			if mode == cfg.G1 {
				text = data[header+1 : header+1+int(data[header])]
				if len(text) != 255 {
					t.Fatalf("variable text length = %d, want 255", len(text))
				}
			} else {
				text = data[header+1:header+1+65]
			}
			separator := bytes.IndexByte(text, 0)
			if separator < 0 {
				t.Fatalf("mode %v: missing name/description separator", mode)
			}
			if mode == cfg.ZZ && separator >= 64 {
				t.Fatal("server name consumed the description's terminating NUL")
			}
			for _, part := range [][]byte{text[:separator], bytes.TrimRight(text[separator+1:], "\x00")} {
				decoded := stringsupport.SJISToUTF8Lossy(part)
				if !bytes.Equal(stringsupport.UTF8ToSJIS(decoded), part) {
					t.Fatalf("mode %v: split CP949 character in %x", mode, part)
				}
			}
		}
	}
}
