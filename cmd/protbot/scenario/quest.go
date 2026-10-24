package scenario

import (
	"fmt"
	"time"

	"erupe-ce/cmd/protbot/protocol"
)

// EnumerateQuests receives and verifies every page of the ZZ quest list.
// It returns concatenated entry frames, without the per-page header and tables.
func EnumerateQuests(ch *protocol.ChannelConn, world uint8, counter uint16) ([]byte, error) {
	result, err := EnumerateQuestsDetailed(ch, world, counter)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

// QuestEnumerationResult describes a completely received, verified snapshot.
// QuestIDs and Entries retain wire order. Entries excludes per-page tables.
// A server-announced total alone does not prove that an operator's intended
// eligible roster was sent; the caller may compare QuestIDs to that roster.
type QuestEnumerationResult struct {
	Total        int
	Pages        int
	PayloadBytes int
	QuestIDs     []uint16
	Entries      []byte
}

// EnumerateQuestsDetailed retains the ID evidence needed to compare the complete
// snapshot with an explicit expected list for this world, counter and account.
// It returns no partial successful result when page validation fails.
func EnumerateQuestsDetailed(ch *protocol.ChannelConn, world uint8, counter uint16) (*QuestEnumerationResult, error) {
	fmt.Printf("[quest] Enumerating all quest pages (world=%d, counter=%d, capacity=%d)...\n",
		world, counter, questEnumerationCapacity)
	result, err := enumerateQuestsDetailed(ch, world, counter)
	if err != nil {
		return nil, err
	}
	fmt.Printf("[quest] Complete: total=%d, received=%d, unique IDs=%d, pages=%d, ACK payload bytes=%d, entry bytes=%d\n",
		result.Total, len(result.QuestIDs), len(result.QuestIDs), result.Pages, result.PayloadBytes, len(result.Entries))
	return result, nil
}

func enumerateQuestsDetailed(ch questPageRequester, world uint8, counter uint16) (*QuestEnumerationResult, error) {
	result, err := enumerateQuests(ch, world, counter)
	if err != nil {
		return nil, err
	}
	return &QuestEnumerationResult{
		Total: result.total, Pages: result.pages, PayloadBytes: result.payloadBytes,
		QuestIDs: result.questIDs, Entries: result.entries,
	}, nil
}

type questPageRequester interface {
	NextAckHandle() uint32
	RequestQuestPage(uint32, []byte, time.Duration) (*protocol.AckResponse, error)
}

func enumerateQuests(ch questPageRequester, world uint8, counter uint16) (*questEnumeration, error) {
	return enumerateQuestPages(func(offset uint16) ([]byte, error) {
		ack := ch.NextAckHandle()
		pkt := protocol.BuildEnumerateQuestPacket(ack, world, counter, offset)
		resp, err := ch.RequestQuestPage(ack, pkt, 15*time.Second)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.AckHandle != ack {
			return nil, fmt.Errorf("missing or mismatched quest ACK for handle %d", ack)
		}
		if resp.ErrorCode != 0 {
			return nil, fmt.Errorf("quest ACK error code %d", resp.ErrorCode)
		}
		if !resp.IsBufferResponse {
			return nil, fmt.Errorf("quest ACK is not a buffer response")
		}
		return resp.Data, nil
	})
}
