package entranceserver

import (
	cfg "erupe-ce/config"
	"go.uber.org/zap"
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestKeyconfigWindowsClient(t *testing.T) {
	path := os.Getenv("KEYCONFIG_SYNC_CLIENT_TEST")
	if path == "" {
		t.Skip("compiled Windows client fixture not supplied")
	}
	m := &keyconfigMock{rows: make(map[[2]uint32][]byte), revs: make(map[[2]uint32]uint32)}
	s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
	s.sessionRepo = m
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.handleEntranceServerConnection(conn)
		}
	}()
	out, err := exec.Command(path, listener.Addr().String()).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	t.Log(string(out))
}
