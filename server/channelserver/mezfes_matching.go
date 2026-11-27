package channelserver

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"

	"erupe-ce/common/byteframe"
)

const (
	mezFesMatchFilter     = 6
	mezFesMatchCapacity   = 4
	mezFesMatchResultCap  = 128
	mezFesRoomSetupSize   = 0x28
	mezFesPartnyaPrefix   = "sl2Ls463p"
	mezFesVolpakkunPrefix = "sl2Ls465p"
)

type mezFesMatchRoom struct {
	serverIP    net.IP
	serverPort  uint16
	stageID     string
	playerCount uint16
	settings    [14]byte // Seven big-endian words in the native 1:3 stage binary.
	title       []byte
	comment     []byte
}

func isMezFesMatchRoom(id string) bool {
	return strings.HasPrefix(id, mezFesPartnyaPrefix) || strings.HasPrefix(id, mezFesVolpakkunPrefix)
}

func handleMezFesRoomSearch(s *Session, ackHandle uint32, local bool, maxResults uint16, request *byteframe.ByteFrame) {
	valueCount := request.ReadUint8()
	game := request.ReadUint16()
	var stagePrefix string
	switch game {
	case 1:
		stagePrefix = mezFesPartnyaPrefix
	case 3:
		stagePrefix = mezFesVolpakkunPrefix
	}

	var rooms []mezFesMatchRoom
	if valueCount == 1 && request.Err() == nil && stagePrefix != "" && s.server.worldType == 6 {
		rooms = s.server.Registry.SearchMezFesRooms(s.server, stagePrefix, int(min(maxResults, mezFesMatchResultCap)))
	}
	resp := byteframe.NewByteFrame()
	resp.WriteUint16(uint16(len(rooms)))
	for _, room := range rooms {
		ip := localhostAddrLE
		if !local {
			ip = binary.LittleEndian.Uint32(room.serverIP)
		}
		resp.WriteUint32(ip)
		resp.WriteUint16(room.serverPort)
		resp.WriteUint16(0)
		resp.WriteUint16(0)
		resp.WriteUint16(room.playerCount)
		resp.WriteUint16(mezFesMatchCapacity)
		resp.WriteUint16(0) // Quest reservations.
		resp.WriteUint8(0)
		resp.WriteUint8(mezFesMatchCapacity)
		resp.WriteUint8(1)
		resp.WriteUint8(uint8(len(room.stageID) + 1))
		resp.WriteUint8(uint8(len(room.title)))
		resp.WriteUint8(uint8(len(room.comment)))
		resp.WriteBytes(room.settings[:])
		resp.WriteUint16(0)
		resp.WriteNullTerminatedBytes([]byte(room.stageID))
		resp.WriteBytes(room.title)
		resp.WriteBytes(room.comment)
	}
	doAckBufSucceed(s, ackHandle, resp.Data())
}

func (r *LocalChannelRegistry) SearchMezFesRooms(source *Server, stagePrefix string, max int) []mezFesMatchRoom {
	var rooms []mezFesMatchRoom
	for _, channel := range r.channels {
		if len(rooms) >= max {
			break
		}
		if channel.worldType != 6 || channel.worldNumber != source.worldNumber ||
			(channel != source && source.worldNumber == 0) {
			continue
		}
		ip := net.ParseIP(channel.IP).To4()
		if ip == nil {
			continue
		}
		channel.stages.Range(func(id string, stage *Stage) bool {
			// The native lobby decoder allows 32 bytes for the NUL-terminated ID.
			if !strings.HasPrefix(id, stagePrefix) || len(id) >= 32 || strings.IndexByte(id, 0) >= 0 {
				return true
			}
			stage.RLock()
			setup := stage.rawBinaryData[stageBinaryKey{1, 3}]
			players := len(stage.clients) + len(stage.reservedClientSlots)
			if channel.stages.IsCurrent(id, stage) && !stage.locked && stage.password == "" && stage.maxPlayers == mezFesMatchCapacity &&
				len(stage.clients) != 0 && players < mezFesMatchCapacity && len(setup) == mezFesRoomSetupSize {
				room := mezFesMatchRoom{
					serverIP:    ip,
					serverPort:  channel.Port,
					stageID:     id,
					playerCount: uint16(players),
					title:       mezFesRoomText(stage.rawBinaryData[stageBinaryKey{1, 0}]),
					comment:     mezFesRoomText(stage.rawBinaryData[stageBinaryKey{1, 1}]),
				}
				copy(room.settings[:], setup[4:])
				rooms = append(rooms, room)
			}
			stage.RUnlock()
			return len(rooms) < max
		})
	}
	return rooms
}

func mezFesRoomText(data []byte) []byte {
	// The ZZ lobby decoder (non-HD 1155fd10) allows up to 128 bytes
	// per text field and requires a NUL even when the field is empty.
	if end := bytes.IndexByte(data, 0); end >= 0 && end < 128 {
		return bytes.Clone(data[:end+1])
	}
	return []byte{0}
}
