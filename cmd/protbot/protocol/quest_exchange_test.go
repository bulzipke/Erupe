package protocol

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
)

func TestQuestPageExchangeRegistersBeforeImmediateAck(t *testing.T) {
	for _, size := range []int{16, 70000} {
		ch := &ChannelConn{}
		payload := bytes.Repeat([]byte{0xA5}, size)
		response, err := ch.requestQuestPage(42, []byte{9}, time.Second, func(packet []byte) error {
			if !bytes.Equal(packet, []byte{9}) {
				t.Fatal("send payload changed")
			}
			bf := byteframe.NewByteFrame()
			bf.WriteUint32(42)
			bf.WriteBool(true)
			bf.WriteUint8(0)
			if size >= 65535 {
				bf.WriteUint16(65535)
				bf.WriteUint32(uint32(size))
			} else {
				bf.WriteUint16(uint16(size))
			}
			bf.WriteBytes(payload)
			ch.handleAck(bf.Data()) // Synchronous reply while send is still running.
			return nil
		})
		if err != nil || response == nil || !bytes.Equal(response.Data, payload) {
			t.Fatalf("size=%d response=%v err=%v", size, response, err)
		}
		if _, exists := ch.waiters.Load(uint32(42)); exists {
			t.Fatal("waiter leaked after success")
		}
	}
}

func TestQuestPageExchangeConcurrentHandles(t *testing.T) {
	ch := &ChannelConn{}
	var wg sync.WaitGroup
	for handle := uint32(1); handle <= 32; handle++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := ch.requestQuestPage(handle, nil, time.Second, func([]byte) error {
				bf := byteframe.NewByteFrame()
				bf.WriteUint32(handle)
				bf.WriteBool(true)
				bf.WriteUint8(0)
				bf.WriteUint16(1)
				bf.WriteUint8(uint8(handle))
				ch.handleAck(bf.Data())
				return nil
			})
			if err != nil || response == nil || response.AckHandle != handle || !bytes.Equal(response.Data, []byte{uint8(handle)}) {
				t.Errorf("handle=%d response=%v err=%v", handle, response, err)
			}
		}()
	}
	wg.Wait()
	ch.waiters.Range(func(key, value any) bool {
		t.Errorf("waiter leaked for handle %v", key)
		return true
	})
}

func TestQuestPageExchangeFailureCleanup(t *testing.T) {
	for _, name := range []string{"send error", "timeout", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			ch := &ChannelConn{}
			old := make(chan *AckResponse, 1)
			if name == "duplicate" {
				ch.waiters.Store(uint32(7), old)
			}
			sends := 0
			response, err := ch.requestQuestPage(7, nil, time.Millisecond, func([]byte) error {
				sends++
				if name == "send error" {
					return errors.New("mock write failure")
				}
				return nil
			})
			if response != nil || err == nil {
				t.Fatalf("response=%v err=%v", response, err)
			}
			value, exists := ch.waiters.Load(uint32(7))
			if name == "duplicate" {
				if sends != 0 || !exists || value != old || !strings.Contains(err.Error(), "already has") {
					t.Fatal("duplicate request replaced the original waiter or sent")
				}
			} else if sends != 1 || exists {
				t.Fatal("failure leaked waiter or skipped send")
			}
		})
	}
}
