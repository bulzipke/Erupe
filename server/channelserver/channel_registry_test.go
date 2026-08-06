package channelserver

import (
	"net"
	"sync"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/binpacket"
	"erupe-ce/network/mhfpacket"
)

func createTestChannels(count int) []*Server {
	channels := make([]*Server, count)
	for i := 0; i < count; i++ {
		s := createTestServer()
		s.ID = uint16(0x1010 + i)
		s.IP = "10.0.0.1"
		s.Port = uint16(54001 + i)
		s.GlobalID = "0101"
		s.userBinary = NewUserBinaryStore()
		channels[i] = s
	}
	return channels
}

func TestLocalRegistryFindSessionByCharID(t *testing.T) {
	channels := createTestChannels(2)
	reg := NewLocalChannelRegistry(channels)

	conn1 := &mockConn{}
	sess1 := createTestSessionForServer(channels[0], conn1, 100, "Alice")
	channels[0].Lock()
	channels[0].sessions[conn1] = sess1
	channels[0].Unlock()

	conn2 := &mockConn{}
	sess2 := createTestSessionForServer(channels[1], conn2, 200, "Bob")
	channels[1].Lock()
	channels[1].sessions[conn2] = sess2
	channels[1].Unlock()

	// Find on first channel
	found := reg.FindSessionByCharID(100)
	if found == nil || found.charID != 100 {
		t.Errorf("FindSessionByCharID(100) = %v, want session with charID 100", found)
	}

	// Find on second channel
	found = reg.FindSessionByCharID(200)
	if found == nil || found.charID != 200 {
		t.Errorf("FindSessionByCharID(200) = %v, want session with charID 200", found)
	}

	// Not found
	found = reg.FindSessionByCharID(999)
	if found != nil {
		t.Errorf("FindSessionByCharID(999) = %v, want nil", found)
	}
}

func TestLocalRegistryBroadcastWorldChat(t *testing.T) {
	channels := createTestChannels(2)
	registry := NewLocalChannelRegistry(channels)

	var sessions []*Session
	for i, channel := range channels {
		conn := &mockConn{}
		session := createTestSessionForServer(channel, conn, uint32(i+1), "Player")
		if i == 1 {
			session.stage = NewStage("sl1Qs123p0a0u2")
		}
		channel.Lock()
		channel.sessions[conn] = session
		channel.Unlock()
		sessions = append(sessions, session)
	}

	if err := registry.BroadcastWorldChat("WebUser", "Hello world"); err != nil {
		t.Fatalf("BroadcastWorldChat: %v", err)
	}

	for index, session := range sessions {
		select {
		case queued := <-session.sendPackets:
			frame := byteframe.NewByteFrameFromBytes(queued.data)
			if opcode := network.PacketID(frame.ReadUint16()); opcode != network.MSG_SYS_CASTED_BINARY {
				t.Fatalf("session %d opcode = %d", index, opcode)
			}
			casted := &mhfpacket.MsgSysCastedBinary{}
			if err := casted.Parse(frame, session.clientContext); err != nil {
				t.Fatalf("session %d parse casted: %v", index, err)
			}
			if casted.BroadcastType != 0 || casted.MessageType != BinaryMessageTypeChat {
				t.Fatalf("session %d casted metadata = %+v", index, casted)
			}
			payload := byteframe.NewByteFrameFromBytes(casted.RawDataPayload)
			payload.SetLE()
			chat := &binpacket.MsgBinChat{}
			if err := chat.Parse(payload); err != nil {
				t.Fatalf("session %d parse chat: %v", index, err)
			}
			if chat.SenderName != "WebUser" || chat.Message != "Hello world" || chat.Type != binpacket.ChatTypeWhisper || chat.Flags != chatFlagServer {
				t.Fatalf("session %d chat = %+v", index, chat)
			}
		default:
			t.Fatalf("session %d did not receive web chat", index)
		}
	}
}

func TestLocalRegistryFindChannelForStage(t *testing.T) {
	channels := createTestChannels(2)
	channels[0].GlobalID = "0101"
	channels[1].GlobalID = "0102"
	reg := NewLocalChannelRegistry(channels)

	channels[1].stages.Store("sl2Qs123p0a0u42", NewStage("sl2Qs123p0a0u42"))

	gid := reg.FindChannelForStage("u42")
	if gid != "0102" {
		t.Errorf("FindChannelForStage(u42) = %q, want %q", gid, "0102")
	}

	gid = reg.FindChannelForStage("u999")
	if gid != "" {
		t.Errorf("FindChannelForStage(u999) = %q, want empty", gid)
	}
}

func TestLocalRegistryDisconnectUser(t *testing.T) {
	channels := createTestChannels(1)
	reg := NewLocalChannelRegistry(channels)

	conn := &mockConn{}
	sess := createTestSessionForServer(channels[0], conn, 42, "Target")
	channels[0].Lock()
	channels[0].sessions[conn] = sess
	channels[0].Unlock()

	reg.DisconnectUser([]uint32{42})

	if !conn.WasClosed() {
		t.Error("DisconnectUser should have closed the connection for charID 42")
	}
}

func TestLocalRegistrySearchSessions(t *testing.T) {
	channels := createTestChannels(2)
	reg := NewLocalChannelRegistry(channels)

	// Add 3 sessions across 2 channels
	for i, ch := range channels {
		conn := &mockConn{}
		sess := createTestSessionForServer(ch, conn, uint32(i+1), "Player")
		sess.stage = NewStage("sl1Ns200p0a0u0")
		ch.Lock()
		ch.sessions[conn] = sess
		ch.Unlock()
	}
	conn3 := &mockConn{}
	sess3 := createTestSessionForServer(channels[0], conn3, 3, "Player")
	sess3.stage = NewStage("sl1Ns200p0a0u0")
	channels[0].Lock()
	channels[0].sessions[conn3] = sess3
	channels[0].Unlock()

	// Search all
	results := reg.SearchSessions(func(s SessionSnapshot) bool { return true }, 10)
	if len(results) != 3 {
		t.Errorf("SearchSessions(all) returned %d results, want 3", len(results))
	}

	// Search with max
	results = reg.SearchSessions(func(s SessionSnapshot) bool { return true }, 2)
	if len(results) != 2 {
		t.Errorf("SearchSessions(max=2) returned %d results, want 2", len(results))
	}

	// Search with predicate
	results = reg.SearchSessions(func(s SessionSnapshot) bool { return s.CharID == 1 }, 10)
	if len(results) != 1 {
		t.Errorf("SearchSessions(charID==1) returned %d results, want 1", len(results))
	}
}

func TestLocalRegistrySearchStages(t *testing.T) {
	channels := createTestChannels(1)
	reg := NewLocalChannelRegistry(channels)

	channels[0].stages.Store("sl2Ls210test1", NewStage("sl2Ls210test1"))
	channels[0].stages.Store("sl2Ls210test2", NewStage("sl2Ls210test2"))
	channels[0].stages.Store("sl1Ns200other", NewStage("sl1Ns200other"))

	results := reg.SearchStages("sl2Ls210", 10)
	if len(results) != 2 {
		t.Errorf("SearchStages(sl2Ls210) returned %d results, want 2", len(results))
	}

	results = reg.SearchStages("sl2Ls210", 1)
	if len(results) != 1 {
		t.Errorf("SearchStages(sl2Ls210, max=1) returned %d results, want 1", len(results))
	}
}

func TestLocalRegistryConcurrentAccess(t *testing.T) {
	channels := createTestChannels(2)
	reg := NewLocalChannelRegistry(channels)

	// Populate some sessions
	for _, ch := range channels {
		for i := 0; i < 10; i++ {
			conn := &mockConn{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50000 + i}}
			sess := createTestSessionForServer(ch, conn, uint32(i+1), "Player")
			sess.stage = NewStage("sl1Ns200p0a0u0")
			ch.Lock()
			ch.sessions[conn] = sess
			ch.Unlock()
		}
	}

	// Run concurrent operations
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(id int) {
			defer wg.Done()
			_ = reg.FindSessionByCharID(uint32(id%10 + 1))
		}(i)
		go func() {
			defer wg.Done()
			_ = reg.FindChannelForStage("u0")
		}()
		go func() {
			defer wg.Done()
			_ = reg.SearchSessions(func(s SessionSnapshot) bool { return true }, 5)
		}()
	}
	wg.Wait()
}

func TestLocalRegistryFindOtherCharacterSession(t *testing.T) {
	// Two channels, so the lookup is exercised across the whole world rather than
	// just the channel the new login landed on.
	channels := createTestChannels(2)
	reg := NewLocalChannelRegistry(channels)

	addSession := func(channel *Server, charID, userID uint32, name string, closed bool) {
		conn := &mockConn{}
		sess := createTestSessionForServer(channel, conn, charID, name)
		sess.userID = userID
		if closed {
			sess.closed.Store(true)
		}
		channel.Lock()
		channel.sessions[conn] = sess
		channel.Unlock()
	}

	addSession(channels[0], 100, 7, "Alice", false)
	addSession(channels[1], 200, 8, "Bob", false)
	addSession(channels[1], 300, 9, "GhostChar", true)

	t.Run("other character on same account is reported", func(t *testing.T) {
		name, found := reg.FindOtherCharacterSession(7, 101)
		if !found || name != "Alice" {
			t.Errorf("FindOtherCharacterSession(7, 101) = (%q, %v), want (\"Alice\", true)", name, found)
		}
	})

	t.Run("same character is ignored", func(t *testing.T) {
		// A channel transfer briefly holds two sessions for one character; treating
		// that as a conflict would make every channel change fail.
		if _, found := reg.FindOtherCharacterSession(7, 100); found {
			t.Error("FindOtherCharacterSession(7, 100) = true, want false for the same character")
		}
	})

	t.Run("unrelated account is ignored", func(t *testing.T) {
		if _, found := reg.FindOtherCharacterSession(42, 1); found {
			t.Error("FindOtherCharacterSession(42, 1) = true, want false")
		}
	})

	t.Run("closed session is ignored", func(t *testing.T) {
		// A session already torn down must not lock the account out while cleanup
		// finishes, or a crash-reconnect would be blocked for the timeout window.
		if _, found := reg.FindOtherCharacterSession(9, 301); found {
			t.Error("FindOtherCharacterSession(9, 301) = true, want false for a closed session")
		}
	})

	t.Run("zero user id never matches", func(t *testing.T) {
		// Sessions that have not logged in yet still carry userID 0.
		if _, found := reg.FindOtherCharacterSession(0, 1); found {
			t.Error("FindOtherCharacterSession(0, 1) = true, want false")
		}
	})
}
