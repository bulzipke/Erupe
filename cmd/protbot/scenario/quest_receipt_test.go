package scenario

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	"erupe-ce/cmd/protbot/protocol"
)

func TestQuestDetailedResultKeepsCompleteEvidence(t *testing.T) {
	m := &mockQuestRequester{request: func(ack uint32, packet []byte, _ time.Duration) (*protocol.AckResponse, error) {
		offset := binary.BigEndian.Uint16(packet[10:12])
		return &protocol.AckResponse{AckHandle: ack, IsBufferResponse: true, Data: questPageFixture(2, int(offset), 70-offset)}, nil
	}}
	result, err := enumerateQuestsDetailed(m, 2, 7)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.Pages != 2 || fmt.Sprint(result.QuestIDs) != "[70 69]" || len(result.Entries) != 760 || result.PayloadBytes <= len(result.Entries) {
		t.Fatalf("incorrect receipt evidence: %+v", result)
	}
	m.request = func(uint32, []byte, time.Duration) (*protocol.AckResponse, error) {
		return nil, errors.New("offline failure")
	}
	if result, err := enumerateQuestsDetailed(m, 2, 7); result != nil || err == nil {
		t.Fatalf("partial detailed result returned after failure: %+v %v", result, err)
	}
}
