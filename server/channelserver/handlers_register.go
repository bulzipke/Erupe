package channelserver

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func handleMsgMhfRegisterEvent(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfRegisterEvent)
	bf := byteframe.NewByteFrame()
	// Some kind of check if there's already a session
	if pkt.CheckOnly && s.server.getRaviSemaphore() == nil {
		doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	bf.WriteUint8(uint8(pkt.WorldID))
	bf.WriteUint8(uint8(pkt.LandID))
	s.server.raviente.Lock()
	ravienteID := s.server.raviente.id
	s.server.raviente.Unlock()
	bf.WriteUint16(ravienteID)
	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
}

// ACK error codes from the MHF client
const ackEFailed = uint8(0x41) // _ACK_EFAILED = 65

func handleMsgMhfReleaseEvent(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfReleaseEvent)

	// Do this ack manually because it uses a non-(0|1) error code
	/*
		_ACK_SUCCESS = 0
		_ACK_ERROR = 1

		_ACK_EINPROGRESS = 16
		_ACK_ENOENT = 17
		_ACK_ENOSPC = 18
		_ACK_ETIMEOUT = 19

		_ACK_EINVALID = 64
		_ACK_EFAILED = 65
		_ACK_ENOMEM = 66
		_ACK_ENOTEXIT = 67
		_ACK_ENOTREADY = 68
		_ACK_EALREADY = 69
		_ACK_DISABLE_WORK = 71
	*/
	s.QueueSendMHF(&mhfpacket.MsgSysAck{
		AckHandle:        pkt.AckHandle,
		IsBufferResponse: false,
		ErrorCode:        ackEFailed,
		AckData:          []byte{0x00, 0x00, 0x00, 0x00},
	})
}

// RaviUpdate represents a Raviente register update entry.
type RaviUpdate struct {
	Op   uint8
	Dest uint8
	Data uint32
}

func handleMsgSysOperateRegister(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysOperateRegister)

	if len(pkt.RawDataPayload) == 0 {
		doAckBufSucceed(s, pkt.AckHandle, nil)
		return
	}

	var raviUpdates []RaviUpdate
	var raviUpdate RaviUpdate
	// Strip null terminator
	bf := byteframe.NewByteFrameFromBytes(pkt.RawDataPayload[:len(pkt.RawDataPayload)-1])
	for i := len(pkt.RawDataPayload) / 6; i > 0; i-- {
		raviUpdate.Op = bf.ReadUint8()
		raviUpdate.Dest = bf.ReadUint8()
		raviUpdate.Data = bf.ReadUint32()
		raviUpdates = append(raviUpdates, raviUpdate)
	}
	bf = byteframe.NewByteFrame()

	var _old, _new uint32
	invalidIndex := -1
	raviPlayers, raviActive := s.server.raviPlayerCount()
	func() {
		s.server.raviente.Lock()
		defer s.server.raviente.Unlock()
		for _, update := range raviUpdates {
			if int(update.Dest) >= len(s.server.raviente.state) {
				invalidIndex = int(update.Dest)
				return
			}
		}
		for _, update := range raviUpdates {
			switch update.Op {
			case 2:
				_old, _new = s.server.updateRaviLocked(pkt.SemaphoreID, update.Dest, update.Data, true, raviPlayers, raviActive)
			case 13, 14:
				_old, _new = s.server.updateRaviLocked(pkt.SemaphoreID, update.Dest, update.Data, false, raviPlayers, raviActive)
			}
			bf.WriteUint8(1)
			bf.WriteUint8(update.Dest)
			bf.WriteUint32(_old)
			bf.WriteUint32(_new)
		}
	}()
	if invalidIndex >= 0 {
		s.logger.Warn("Rejected Raviente register index", zap.Int("index", invalidIndex))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())

	if s.server.erupeConfig.GameplayOptions.LowLatencyRaviente {
		s.notifyRavi()
	}
}

func handleMsgSysLoadRegister(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysLoadRegister)
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	bf.WriteUint8(pkt.Values)
	s.server.raviente.Lock()
	if int(pkt.Values) > len(s.server.raviente.state) {
		s.server.raviente.Unlock()
		s.logger.Warn("Rejected Raviente register read count", zap.Uint8("count", pkt.Values))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	for i := uint8(0); i < pkt.Values; i++ {
		switch pkt.RegisterID {
		case raviRegisterState:
			bf.WriteUint32(s.server.raviente.state[i])
		case raviRegisterSupport:
			bf.WriteUint32(s.server.raviente.support[i])
		case raviRegisterGeneral:
			bf.WriteUint32(s.server.raviente.register[i])
		}
	}
	s.server.raviente.Unlock()
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func (s *Session) notifyRavi() {
	sema := s.server.getRaviSemaphore()
	if sema == nil {
		return
	}
	var temp mhfpacket.MHFPacket
	raviNotif := byteframe.NewByteFrame()
	temp = &mhfpacket.MsgSysNotifyRegister{RegisterID: raviRegisterState}
	raviNotif.WriteUint16(uint16(temp.Opcode()))
	_ = temp.Build(raviNotif, s.clientContext)
	temp = &mhfpacket.MsgSysNotifyRegister{RegisterID: raviRegisterSupport}
	raviNotif.WriteUint16(uint16(temp.Opcode()))
	_ = temp.Build(raviNotif, s.clientContext)
	temp = &mhfpacket.MsgSysNotifyRegister{RegisterID: raviRegisterGeneral}
	raviNotif.WriteUint16(uint16(temp.Opcode()))
	_ = temp.Build(raviNotif, s.clientContext)
	raviNotif.WriteUint16(0x0010) // End it.
	if s.server.erupeConfig.GameplayOptions.LowLatencyRaviente {
		sema.RLock()
		clients := make([]*Session, 0, len(sema.clients))
		for session := range sema.clients {
			clients = append(clients, session)
		}
		sema.RUnlock()
		for _, session := range clients {
			session.QueueSendNonBlocking(raviNotif.Data())
		}
	} else {
		sema.RLock()
		clients := make([]*Session, 0, len(sema.clients))
		for session := range sema.clients {
			clients = append(clients, session)
		}
		sema.RUnlock()
		for _, session := range clients {
			if session.charID == s.charID {
				session.QueueSendNonBlocking(raviNotif.Data())
			}
		}
	}
}

func handleMsgSysNotifyRegister(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented
