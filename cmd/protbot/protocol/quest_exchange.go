package protocol

import (
	"fmt"
	"time"
)

// RequestQuestPage registers its ACK waiter before sending the request, so a
// fast page reply cannot be discarded between SendPacket and WaitForAck.
func (ch *ChannelConn) RequestQuestPage(handle uint32, packet []byte, timeout time.Duration) (*AckResponse, error) {
	return ch.requestQuestPage(handle, packet, timeout, ch.SendPacket)
}

func (ch *ChannelConn) requestQuestPage(handle uint32, packet []byte, timeout time.Duration, send func([]byte) error) (*AckResponse, error) {
	waitCh := make(chan *AckResponse, 1)
	if _, loaded := ch.waiters.LoadOrStore(handle, waitCh); loaded {
		return nil, fmt.Errorf("quest ACK handle %d already has a waiter", handle)
	}
	defer ch.waiters.Delete(handle)
	if err := send(packet); err != nil {
		return nil, fmt.Errorf("quest page send: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-waitCh:
		return resp, nil
	case <-timer.C:
		return nil, fmt.Errorf("quest ACK timeout for handle %d", handle)
	}
}
