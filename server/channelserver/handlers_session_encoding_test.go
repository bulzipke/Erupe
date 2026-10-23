package channelserver

import (
	"bytes"
	"strings"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestTransitMessageSearchesKoreanName(t *testing.T) {
	server := setupTransitServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	target := setupTransitSession(42, server, "192.168.1.50")
	target.Name = "한글이름"
	target.stage = NewStage("abcLa000")
	searcher := setupTransitSession(1, server, "192.168.1.50")
	term := stringsupport.UTF8ToSJIS("한글")
	handleMsgMhfTransitMessage(searcher, &mhfpacket.MsgMhfTransitMessage{
		AckHandle: 100, SearchType: 2,
		MessageData: buildTransitSearchByName(string(term), 10),
	})
	select {
	case packet := <-searcher.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(packet.data[ackBufDataOffset:])
		if count := bf.ReadUint16(); count != 1 {
			t.Fatalf("Korean name search result count = %d, want 1", count)
		}
		bf.ReadBytes(10) // IP, port, character ID.
		stageLength := bf.ReadUint8()
		nameLength := bf.ReadUint8()
		bf.ReadUint16() // User binary length.
		bf.ReadBytes(48) // ZZ padding.
		bf.ReadBytes(uint(stageLength))
		want := append(stringsupport.UTF8ToSJIS(target.Name), 0)
		if got := bf.ReadBytes(uint(nameLength)); !bytes.Equal(got, want) {
			t.Fatalf("search name bytes = %x, want %x", got, want)
		}
	default:
		t.Fatal("no search response queued")
	}
}

func TestTransitMessageStageLengthFitsPrefix(t *testing.T) {
	server := setupTransitServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	target := setupTransitSession(42, server, "192.168.1.50")
	target.Name = "한글"
	target.stage = NewStage(strings.Repeat("A", maxStageIDLength))
	searcher := setupTransitSession(1, server, "192.168.1.50")
	handleMsgMhfTransitMessage(searcher, &mhfpacket.MsgMhfTransitMessage{
		AckHandle: 100, SearchType: 1, MessageData: buildTransitSearchByCharID(42),
	})
	select {
	case packet := <-searcher.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(packet.data[ackBufDataOffset:])
		bf.ReadUint16()
		bf.ReadBytes(10)
		stageLength := bf.ReadUint8()
		nameLength := bf.ReadUint8()
		bf.ReadUint16()
		bf.ReadBytes(48)
		stage := bf.ReadBytes(uint(stageLength))
		if len(stage) != 255 || stage[254] != 0 {
			t.Fatalf("stage length = %d, want 255 with NUL", stageLength)
		}
		want := append(stringsupport.UTF8ToSJIS(target.Name), 0)
		if got := bf.ReadBytes(uint(nameLength)); !bytes.Equal(got, want) {
			t.Fatalf("name after long stage = %x, want %x", got, want)
		}
	default:
		t.Fatal("no search response queued")
	}
}
