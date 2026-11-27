package channelserver

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestMezFesMatchingJoinsExistingRecruitment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stageID string
		game    byte
		local   bool
	}{
		{"PartnyaSameChannel", "sl2Ls463p1a0u0", 1, true},
		{"VolpakkunOtherChannel", "sl2Ls465p2a0u0", 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostChannel := setupTransitServer()
			hostChannel.worldType = 6
			hostChannel.worldNumber = 1
			hostChannel.erupeConfig.RealClientMode = cfg.ZZ
			searchChannel := hostChannel
			channels := []*Server{hostChannel}
			remoteIP := "127.0.0.1"
			advertisedIP := net.IPv4(127, 0, 0, 1).To4()
			if !tc.local {
				searchChannel = setupTransitServer()
				searchChannel.Port++
				searchChannel.worldType = 6
				searchChannel.worldNumber = 1
				searchChannel.erupeConfig.RealClientMode = cfg.ZZ
				channels = append(channels, searchChannel)
				remoteIP = "192.168.1.50"
				advertisedIP = net.ParseIP(hostChannel.IP).To4()
			}
			registry := NewLocalChannelRegistry(channels)
			hostChannel.Registry, searchChannel.Registry = registry, registry
			host := setupTransitSession(10, hostChannel, remoteIP)
			searcher := setupTransitSession(20, searchChannel, remoteIP)

			handleMsgSysCreateStage(host, &mhfpacket.MsgSysCreateStage{StageID: tc.stageID, PlayerCount: 4})
			setup := make([]byte, 40)
			copy(setup, []byte{1, 1, 1, 6})
			setup[17] = tc.game
			handleMsgSysSetStageBinary(host, &mhfpacket.MsgSysSetStageBinary{
				StageID: tc.stageID, BinaryType0: 1, BinaryType1: 3, RawDataPayload: setup,
			})
			// No room is advertised between setup and the host entering the stage.
			assertMezFesSearchEmpty(t, searcher, []byte{1, 0, 128, 6, 1, 0, tc.game})
			handleMsgSysEnterStage(host, &mhfpacket.MsgSysEnterStage{StageID: tc.stageID})

			// ZZ sends search type 4 with a single two-byte filter value.
			request := &mhfpacket.MsgMhfTransitMessage{}
			wire := []byte{0, 0, 0, 42, 4, 0, 0, 4, 0, 7, 1, 0, 128, 6, 1, 0, tc.game}
			if err := request.Parse(byteframe.NewByteFrameFromBytes(wire), searcher.clientContext); err != nil {
				t.Fatal(err)
			}
			searchChannel.handlerTable[request.Opcode()](searcher, request)
			resp := mezFesSearchAck(t, searcher)

			// Offsets match the native ZZ decoder. Both optional strings must
			// carry a terminator for the decoder to accept the room.
			stageLen := len(tc.stageID) + 1
			if len(resp) != 2+6+32+stageLen+2 || binary.BigEndian.Uint16(resp) != 1 {
				t.Fatalf("expected one complete lobby record, got %x", resp)
			}
			if binary.BigEndian.Uint32(resp[2:]) != binary.LittleEndian.Uint32(advertisedIP) ||
				binary.BigEndian.Uint16(resp[6:]) != hostChannel.Port {
				t.Fatalf("wrong room address in %x", resp[2:8])
			}
			entry := resp[8:]
			if binary.BigEndian.Uint16(entry[4:]) != 1 || binary.BigEndian.Uint16(entry[6:]) != 4 ||
				binary.BigEndian.Uint16(entry[8:]) != 0 ||
				!bytes.Equal(entry[13:16], []byte{byte(stageLen), 1, 1}) ||
				!bytes.Equal(entry[16:30], setup[4:18]) ||
				!bytes.Equal(entry[32:], append([]byte(tc.stageID), 0, 0, 0)) {
				t.Fatalf("invalid lobby record %x", entry)
			}

			handleMsgMhfTransitMessage(searcher, &mhfpacket.MsgMhfTransitMessage{
				SearchType: 3, MessageData: buildTransitSearchByLobby(advertisedIP, hostChannel.Port, tc.stageID, 4),
			})
			if roster := mezFesSearchAck(t, searcher); len(roster) < 12 ||
				binary.BigEndian.Uint16(roster) != 1 || binary.BigEndian.Uint32(roster[8:]) != host.charID {
				t.Fatalf("could not locate the waiting host, got %x", roster)
			}

			guest := searcher
			if !tc.local {
				guest = setupTransitSession(searcher.charID, hostChannel, remoteIP)
			}
			selectedID := string(entry[32 : 32+stageLen-1])
			handleMsgSysEnterStage(guest, &mhfpacket.MsgSysEnterStage{StageID: selectedID})
			if guest.stage == nil || guest.stage != host.stage || len(host.stage.clients) != 2 {
				t.Fatal("guest did not enter the same room as the host")
			}
			for _, id := range []uint32{30, 40} {
				participant := setupTransitSession(id, hostChannel, remoteIP)
				handleMsgSysEnterStage(participant, &mhfpacket.MsgSysEnterStage{StageID: selectedID})
			}
			if len(host.stage.clients) != 4 {
				t.Fatal("room did not accept four players")
			}
			assertMezFesSearchEmpty(t, setupTransitSession(50, searchChannel, remoteIP), wire[10:])
		})
	}
}

func TestMezFesMatchSearchFiltersClosedOtherGameAndOtherWorld(t *testing.T) {
	channel := setupTransitServer()
	channel.worldType = 6
	channel.worldNumber = 1
	channel.erupeConfig.RealClientMode = cfg.ZZ
	otherWorld := setupTransitServer()
	otherWorld.Port++
	otherWorld.worldType = 6
	otherWorld.worldNumber = 2
	channel.Registry = NewLocalChannelRegistry([]*Server{channel, otherWorld})
	searcher := setupTransitSession(1, channel, "192.168.1.50")
	host := setupTransitSession(2, channel, "192.168.1.50")
	foreignHost := setupTransitSession(3, otherWorld, "192.168.1.50")
	for _, tc := range []struct {
		id        string
		locked    bool
		published bool
	}{
		{"sl2Ls463p1a0u0", true, true},
		{"sl2Ls463p2a0u0", false, false},
		{"sl2Ls465p1a0u0", false, true},
		{"sl2Ls210p1a0u0", false, true},
	} {
		stage := NewStage(tc.id)
		stage.maxPlayers = 4
		stage.clients[host] = host.charID
		stage.locked = tc.locked
		if tc.published {
			stage.rawBinaryData[stageBinaryKey{1, 3}] = make([]byte, 40)
		}
		channel.stages.Store(tc.id, stage)
	}
	stage := NewStage("sl2Ls463p3a0u0")
	stage.maxPlayers = 4
	stage.clients[foreignHost] = foreignHost.charID
	stage.rawBinaryData[stageBinaryKey{1, 3}] = make([]byte, 40)
	otherWorld.stages.Store(stage.id, stage)

	assertMezFesSearchEmpty(t, searcher, []byte{1, 0, 128, 6, 1, 0, 1})
	assertMezFesSearchEmpty(t, searcher, []byte{1, 0, 128, 6, 1, 0, 2})
	assertMezFesSearchEmpty(t, searcher, []byte{1, 0, 128, 6, 1, 0})
	if doStageTransfer(searcher, 1, "sl2Ls463p1a0u0") ||
		doStageTransfer(searcher, 2, "sl2Ls463p3a0u0") || searcher.stage != nil {
		t.Fatal("an expired search result allowed entry into a closed or missing room")
	}
}

func assertMezFesSearchEmpty(t *testing.T, session *Session, payload []byte) {
	t.Helper()
	handleMsgMhfTransitMessage(session, &mhfpacket.MsgMhfTransitMessage{SearchType: 4, MessageData: payload})
	if resp := mezFesSearchAck(t, session); !bytes.Equal(resp, []byte{0, 0}) {
		t.Fatalf("expected an empty search, got %x", resp)
	}
}

func mezFesSearchAck(t *testing.T, session *Session) []byte {
	t.Helper()
	select {
	case pkt := <-session.sendPackets:
		if len(pkt.data) < ackBufDataOffset || pkt.data[6] != 1 || pkt.data[7] != 0 {
			t.Fatalf("expected a successful buffer ACK, got %x", pkt.data)
		}
		return pkt.data[ackBufDataOffset:]
	default:
		t.Fatal("missing buffer ACK")
		return nil
	}
}
