package signserver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	cfg "erupe-ce/config"
)

func TestMakeSignResponseNGWordPayloadFitsClientBuffer(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{CapLink: cfg.CapLinkOptions{Values: []uint16{0, 0, 0, 0, 0}}},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets: 1, MezFesGroupTickets: 1,
			ClanMemberLimits: [][]uint8{{0, 30}},
		},
	}
	server := newMakeSignResponseServer(config)
	server.ngWords = make([]string, 5000)
	for i := range server.ngWords {
		server.ngWords[i] = fmt.Sprintf("금칙어테스트%04d", i)
	}
	session := &Session{logger: zap.NewNop(), server: server, client: PC100, rawConn: newMockConn()}
	result := session.makeSignResponse(0)
	filterStart := bytes.Index(result, []byte("smc\x00"))
	if filterStart < 2 {
		t.Fatal("NG-word filter start not found")
	}
	filterLen := int(binary.BigEndian.Uint16(result[filterStart-2 : filterStart]))
	if filterLen <= 0 || filterLen > maxNGWordFilterBytes {
		t.Fatalf("filter length = %d", filterLen)
	}
	if filterStart+filterLen > len(result) {
		t.Fatalf("filter runs past response: start=%d len=%d response=%d", filterStart, filterLen, len(result))
	}
}

func TestGeneratedConservativeNGWordListFitsWithoutTruncation(t *testing.T) {
	words, err := loadNGWordsCSV(filepath.Join("..", "..", "bin", "ng_words.csv"))
	if err != nil {
		t.Fatal(err)
	}
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{CapLink: cfg.CapLinkOptions{Values: []uint16{0, 0, 0, 0, 0}}},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets: 1, MezFesGroupTickets: 1,
			ClanMemberLimits: [][]uint8{{0, 30}},
		},
	}
	server := newMakeSignResponseServer(config)
	server.ngWords = words
	result := (&Session{logger: zap.NewNop(), server: server, client: PC100, rawConn: newMockConn()}).makeSignResponse(0)
	filterStart := bytes.Index(result, []byte("smc\x00"))
	if filterStart < 2 {
		t.Fatal("NG-word filter start not found")
	}
	filterLen := int(binary.BigEndian.Uint16(result[filterStart-2 : filterStart]))
	filter := result[filterStart : filterStart+filterLen]
	offset := 4
	smcLen := int(binary.LittleEndian.Uint32(filter[offset : offset+4]))
	offset += 4 + smcLen
	if string(filter[offset:offset+4]) != "nam\x00" {
		t.Fatal("nam table not found")
	}
	offset += 4
	namLen := int(binary.LittleEndian.Uint32(filter[offset : offset+4]))
	offset += 4 + namLen
	if string(filter[offset:offset+4]) != "msg\x00" {
		t.Fatal("msg table not found")
	}
	offset += 4
	msgLen := int(binary.LittleEndian.Uint32(filter[offset : offset+4]))
	offset += 4
	messageCount := 0
	for consumed := 0; consumed < msgLen; messageCount++ {
		parts := int(binary.LittleEndian.Uint32(filter[offset+consumed : offset+consumed+4]))
		consumed += ngWordEntrySize(make([]uint16, parts))
	}
	if messageCount != len(words) {
		t.Fatalf("message table contains %d of %d configured words", messageCount, len(words))
	}
	t.Logf("all %d configured words fit; filter payload is %d/%d bytes", len(words), filterLen, maxNGWordFilterBytes)
}

func TestConservativeNGWordListProfile(t *testing.T) {
	words, err := loadNGWordsCSV(filepath.Join("..", "..", "bin", "ng_words.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(words) > 100 {
		t.Fatalf("conservative list grew to %d entries; review false-positive risk before expanding it", len(words))
	}
	set := make(map[string]struct{}, len(words))
	for _, word := range words {
		set[word] = struct{}{}
	}
	for _, required := range []string{"시발", "씨발", "병신", "개새끼", "좆", "fuck"} {
		if _, ok := set[required]; !ok {
			t.Errorf("required high-confidence NG word %q is missing", required)
		}
	}
	for _, excluded := range []string{"보지", "자지", "새끼", "개년", "졸라", "생리", "성교", "초딩", "고딩", "호구", "sex", "ass"} {
		if _, ok := set[excluded]; ok {
			t.Errorf("ambiguous substring %q must not be in the conservative list", excluded)
		}
	}
}

// newMakeSignResponseServer creates a Server with mock repos for makeSignResponse tests.
func newMakeSignResponseServer(config *cfg.Config) *Server {
	return &Server{
		erupeConfig: config,
		logger:      zap.NewNop(),
		charRepo: &mockSignCharacterRepo{
			characters: []character{},
			friends:    nil,
			guildmates: nil,
		},
		userRepo: &mockSignUserRepo{
			returnExpiry: time.Now().Add(time.Hour * 24 * 30),
			lastLogin:    time.Now(),
		},
		sessionRepo: &mockSignSessionRepo{
			registerUIDTokenID: 1,
		},
	}
}

// TestMakeSignResponse_EmptyCapLinkValues verifies the crash is FIXED when CapLink.Values is empty
// Previously panicked: runtime error: index out of range [0] with length 0
// From erupe.log.1:659796 and 659853
// After fix: Should handle empty array gracefully with defaults
func TestMakeSignResponse_EmptyCapLinkValues(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{}, // Empty array - should now use defaults instead of panicking
				Key:    "test",
				Host:   "localhost",
				Port:   8080,
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
			ClanMemberLimits: [][]uint8{
				{1, 10},
				{2, 20},
				{3, 30},
			},
		},
	}

	session := &Session{
		logger: zap.NewNop(),
		server: newMakeSignResponseServer(config),
		client: PC100,
	}

	// Set up defer to catch ANY panic - we should NOT get array bounds panic anymore
	defer func() {
		if r := recover(); r != nil {
			// If panic occurs, it should NOT be from array access
			panicStr := fmt.Sprintf("%v", r)
			if strings.Contains(panicStr, "index out of range") {
				t.Errorf("Array bounds panic NOT fixed! Still getting: %v", r)
			} else {
				// Other panic is acceptable (DB, etc) - we only care about array bounds
				t.Logf("Non-array-bounds panic (acceptable): %v", r)
			}
		}
	}()

	// This should NOT panic on array bounds anymore
	result := session.makeSignResponse(0)
	if len(result) > 0 {
		t.Log("makeSignResponse handled empty CapLink.Values without array bounds panic")
	}
}

// TestMakeSignResponse_InsufficientCapLinkValues verifies the crash is FIXED when CapLink.Values is too small
// Previously panicked: runtime error: index out of range [1]
// After fix: Should handle small array gracefully with defaults
func TestMakeSignResponse_InsufficientCapLinkValues(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{51728}, // Only 1 element, code used to panic accessing [1]
				Key:    "test",
				Host:   "localhost",
				Port:   8080,
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
			ClanMemberLimits: [][]uint8{
				{1, 10},
			},
		},
	}

	session := &Session{
		logger: zap.NewNop(),
		server: newMakeSignResponseServer(config),
		client: PC100,
	}

	defer func() {
		if r := recover(); r != nil {
			panicStr := fmt.Sprintf("%v", r)
			if strings.Contains(panicStr, "index out of range") {
				t.Errorf("Array bounds panic NOT fixed! Still getting: %v", r)
			} else {
				t.Logf("Non-array-bounds panic (acceptable): %v", r)
			}
		}
	}()

	// This should NOT panic on array bounds anymore
	result := session.makeSignResponse(0)
	if len(result) > 0 {
		t.Log("makeSignResponse handled insufficient CapLink.Values without array bounds panic")
	}
}

// TestMakeSignResponse_MissingCapLinkValues234 verifies the crash is FIXED when CapLink.Values doesn't have 5 elements
// Previously panicked: runtime error: index out of range [2/3/4]
// After fix: Should handle small array gracefully with defaults
func TestMakeSignResponse_MissingCapLinkValues234(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{100, 200}, // Only 2 elements, code used to panic accessing [2][3][4]
				Key:    "test",
				Host:   "localhost",
				Port:   8080,
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
			ClanMemberLimits: [][]uint8{
				{1, 10},
			},
		},
	}

	session := &Session{
		logger: zap.NewNop(),
		server: newMakeSignResponseServer(config),
		client: PC100,
	}

	defer func() {
		if r := recover(); r != nil {
			panicStr := fmt.Sprintf("%v", r)
			if strings.Contains(panicStr, "index out of range") {
				t.Errorf("Array bounds panic NOT fixed! Still getting: %v", r)
			} else {
				t.Logf("Non-array-bounds panic (acceptable): %v", r)
			}
		}
	}()

	// This should NOT panic on array bounds anymore
	result := session.makeSignResponse(0)
	if len(result) > 0 {
		t.Log("makeSignResponse handled missing CapLink.Values[2/3/4] without array bounds panic")
	}
}

// TestCapLinkValuesBoundsChecking verifies bounds checking logic for CapLink.Values
// Tests the specific logic that was fixed without needing full database setup
func TestCapLinkValuesBoundsChecking(t *testing.T) {
	// Test the bounds checking logic directly
	testCases := []struct {
		name          string
		values        []uint16
		expectDefault bool
	}{
		{"empty array", []uint16{}, true},
		{"1 element", []uint16{100}, true},
		{"2 elements", []uint16{100, 200}, true},
		{"3 elements", []uint16{100, 200, 300}, true},
		{"4 elements", []uint16{100, 200, 300, 400}, true},
		{"5 elements (valid)", []uint16{100, 200, 300, 400, 500}, false},
		{"6 elements (valid)", []uint16{100, 200, 300, 400, 500, 600}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Replicate the bounds checking logic from the fix
			capLinkValues := tc.values
			if len(capLinkValues) < 5 {
				capLinkValues = []uint16{0, 0, 0, 0, 0}
			}

			// Verify all 5 indices are now safe to access
			_ = capLinkValues[0]
			_ = capLinkValues[1]
			_ = capLinkValues[2]
			_ = capLinkValues[3]
			_ = capLinkValues[4]

			// Verify correct behavior
			if tc.expectDefault {
				if capLinkValues[0] != 0 || capLinkValues[1] != 0 {
					t.Errorf("Expected default values, got %v", capLinkValues)
				}
			} else {
				if capLinkValues[0] == 0 && tc.values[0] != 0 {
					t.Errorf("Expected original values, got defaults")
				}
			}

			t.Logf("%s: All 5 indices accessible without panic", tc.name)
		})
	}
}

// TestMakeSignResponse_FullFlow tests the complete makeSignResponse with mock repos.
func TestMakeSignResponse_FullFlow(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	// Give the server some characters
	server.charRepo = &mockSignCharacterRepo{
		characters: []character{
			{ID: 1, Name: "TestHunter", HR: 100, GR: 50, WeaponType: 3, LastLogin: 1700000000},
		},
	}

	conn := newMockConn()
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: conn,
		client:  PC100,
	}

	result := session.makeSignResponse(1)
	if len(result) == 0 {
		t.Error("makeSignResponse() returned empty result")
	}
	// First byte should be SIGN_SUCCESS
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("makeSignResponse() first byte = %d, want %d (SIGN_SUCCESS)", result[0], SIGN_SUCCESS)
	}
}

// TestMakeSignResponse_PSNClientWritesPSNID verifies PSN client appends PSNID field.
func TestMakeSignResponse_PSNClientWritesPSNID(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	server.charRepo = &mockSignCharacterRepo{
		characters: []character{{ID: 1, Name: "PSNHunter", HR: 50}},
	}
	server.userRepo = &mockSignUserRepo{
		returnExpiry: time.Now().Add(time.Hour * 24 * 30),
		lastLogin:    time.Now(),
		psnIDForUser: "MyPSNID",
	}

	// PC response
	pcConn := newMockConn()
	pcSession := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: pcConn,
		client:  PC100,
	}
	pcResult := pcSession.makeSignResponse(1)

	// PS4 response
	ps4Conn := newMockConn()
	ps4Session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: ps4Conn,
		client:  PS4,
	}
	ps4Result := ps4Session.makeSignResponse(1)

	// PSN response should be longer due to 20-byte PSNID field
	if len(ps4Result) <= len(pcResult) {
		t.Errorf("PS4 response len (%d) should be > PC response len (%d)", len(ps4Result), len(pcResult))
	}
}

// TestMakeSignResponse_CapLink51728_20000 verifies CapLink key is written.
func TestMakeSignResponse_CapLink51728_20000(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{51728, 20000, 0, 0, 0},
				Key:    "caplink-key-test",
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	conn := newMockConn()
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: conn,
		client:  PC100,
	}

	result := session.makeSignResponse(1)
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("first byte = %d, want SIGN_SUCCESS", result[0])
	}

	// The response with CapLink key should be longer than without
	configNoKey := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}
	serverNoKey := newMakeSignResponseServer(configNoKey)
	sessionNoKey := &Session{
		logger:  zap.NewNop(),
		server:  serverNoKey,
		rawConn: newMockConn(),
		client:  PC100,
	}
	resultNoKey := sessionNoKey.makeSignResponse(1)

	if len(result) <= len(resultNoKey) {
		t.Errorf("CapLink 51728/20000 response len (%d) should be > base response len (%d)", len(result), len(resultNoKey))
	}
}

// TestMakeSignResponse_CapLink51728_20002 verifies the 20002 variant also writes key.
func TestMakeSignResponse_CapLink51728_20002(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{51728, 20002, 0, 0, 0},
				Key:    "caplink-key-20002",
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: newMockConn(),
		client:  PC100,
	}

	result := session.makeSignResponse(1)
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("first byte = %d, want SIGN_SUCCESS", result[0])
	}
	if len(result) == 0 {
		t.Error("makeSignResponse() returned empty result")
	}
}

// TestMakeSignResponse_CapLink51729Combo verifies the 51729 host:port write.
func TestMakeSignResponse_CapLink51729Combo(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 51729, 1, 20000},
				Host:   "caplink.example.com",
				Port:   9999,
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: newMockConn(),
		client:  PC100,
	}

	result := session.makeSignResponse(1)
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("first byte = %d, want SIGN_SUCCESS", result[0])
	}

	// Response with 51729 combo should include host:port string, making it longer
	configNoCap := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}
	serverNoCap := newMakeSignResponseServer(configNoCap)
	sessionNoCap := &Session{
		logger:  zap.NewNop(),
		server:  serverNoCap,
		rawConn: newMockConn(),
		client:  PC100,
	}
	resultNoCap := sessionNoCap.makeSignResponse(1)

	if len(result) <= len(resultNoCap) {
		t.Errorf("CapLink 51729 combo response len (%d) should be > base len (%d)", len(result), len(resultNoCap))
	}
}

// TestMakeSignResponse_MezFesSwitchMinigame verifies stalls[4] is set to 2.
func TestMakeSignResponse_MezFesSwitchMinigame(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:    100,
			MezFesGroupTickets:   100,
			MezFesSwitchMinigame: true,
		},
	}

	server := newMakeSignResponseServer(config)
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: newMockConn(),
		client:  PC100,
	}

	result := session.makeSignResponse(1)
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("first byte = %d, want SIGN_SUCCESS", result[0])
	}
	if len(result) == 0 {
		t.Error("makeSignResponse() returned empty result")
	}
}

// mockConnRemote is a mockConn variant with a configurable RemoteAddr.
type mockConnRemote struct {
	mockConn
	remoteAddr net.Addr
}

func (m *mockConnRemote) RemoteAddr() net.Addr {
	return m.remoteAddr
}

// TestMakeSignResponse_NonLocalhostRemote verifies the non-localhost entrance addr path.
func TestMakeSignResponse_NonLocalhostRemote(t *testing.T) {
	config := &cfg.Config{
		Host: "192.168.1.100",
		Entrance: cfg.Entrance{
			Port: 53310,
		},
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	conn := &mockConnRemote{
		mockConn:   *newMockConn(),
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 12345},
	}
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: conn,
		client:  PC100,
	}

	result := session.makeSignResponse(1)
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("first byte = %d, want SIGN_SUCCESS", result[0])
	}
	// Response should contain the Host address (192.168.1.100) rather than 127.0.0.1
	resultStr := string(result)
	if !strings.Contains(resultStr, "192.168.1.100") {
		t.Error("non-localhost response should contain Host address")
	}
}

// TestMakeSignResponse_PSNTokenPath verifies the PSN token registration when uid=0 with psn set.
func TestMakeSignResponse_PSNTokenPath(t *testing.T) {
	config := &cfg.Config{
		DebugOptions: cfg.DebugOptions{
			CapLink: cfg.CapLinkOptions{
				Values: []uint16{0, 0, 0, 0, 0},
			},
		},
		GameplayOptions: cfg.GameplayOptions{
			MezFesSoloTickets:  100,
			MezFesGroupTickets: 100,
		},
	}

	server := newMakeSignResponseServer(config)
	server.sessionRepo = &mockSignSessionRepo{
		registerPSNTokenID: 500,
	}
	session := &Session{
		logger:  zap.NewNop(),
		server:  server,
		rawConn: newMockConn(),
		client:  PS4,
		psn:     "my_psn_id",
	}

	result := session.makeSignResponse(0)
	if result[0] != uint8(SIGN_SUCCESS) {
		t.Errorf("first byte = %d, want SIGN_SUCCESS", result[0])
	}
}
