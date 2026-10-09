package channelserver

import (
	"bytes"
	"strings"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
)

func TestWriteCampaignStringEncodesHangul(t *testing.T) {
	bf := byteframe.NewByteFrame()
	writeCampaignString(bf, "레알 키트 생산권 세트", campaignTextMax)
	body := stringsupport.UTF8ToSJIS("레알 키트 생산권 세트")
	want := append([]byte{byte(len(body) + 1)}, body...)
	want = append(want, 0)
	if !bytes.Equal(bf.Data(), want) {
		t.Fatalf("writeCampaignString = % x, want % x", bf.Data(), want)
	}
	if len(body) == 0 {
		t.Fatal("Hangul title encoded to nothing")
	}
}

func TestWriteCampaignStringCutsAtWholeCharacter(t *testing.T) {
	bf := byteframe.NewByteFrame()
	writeCampaignString(bf, strings.Repeat("가", 200), campaignTextMax)
	data := bf.Data()
	if int(data[0]) != len(data)-1 || data[len(data)-1] != 0 {
		t.Fatalf("length prefix %d does not match body+terminator %d", data[0], len(data)-1)
	}
	if body := len(data) - 2; body > campaignTextMax || body%2 != 0 {
		t.Fatalf("body %d bytes: over the cap or split a character", body)
	}
}

func TestWriteCampaignCountEscapesFromFF(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want []byte
	}{
		{0, []byte{0x00}},
		{254, []byte{0xFE}},
		{255, []byte{0xFF, 0x00, 0xFF}},
		{300, []byte{0xFF, 0x01, 0x2C}},
	} {
		bf := byteframe.NewByteFrame()
		writeCampaignCount(bf, tt.n)
		if !bytes.Equal(bf.Data(), tt.want) {
			t.Errorf("writeCampaignCount(%d) = % x, want % x", tt.n, bf.Data(), tt.want)
		}
	}
}

func TestWriteCampaignPrefixIsNineBytes(t *testing.T) {
	bf := byteframe.NewByteFrame()
	writeCampaignPrefix(bf, 123182040, "RLKT")
	want := []byte{0x07, 0x57, 0x9B, 0xD8, 0x00, 'R', 'L', 'K', 'T'}
	if !bytes.Equal(bf.Data(), want) {
		t.Fatalf("writeCampaignPrefix = % x, want % x", bf.Data(), want)
	}
	bf = byteframe.NewByteFrame()
	writeCampaignPrefix(bf, 1, "AB")
	if got := bf.Data()[5:]; !bytes.Equal(got, []byte{'A', 'B', 0, 0}) {
		t.Fatalf("short prefix padded to % x, want 41 42 00 00", got)
	}
}

func TestHandleMsgMhfEnumerateCampaign(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfEnumerateCampaign{
		AckHandle: 12345,
	}

	handleMsgMhfEnumerateCampaign(session, pkt)

	// Verify response packet was queued (fail response expected)
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfStateCampaign(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfStateCampaign{
		AckHandle: 12345,
	}

	handleMsgMhfStateCampaign(session, pkt)

	// Verify response packet was queued (fail response expected)
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfApplyCampaign(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfApplyCampaign{
		AckHandle: 12345,
	}

	handleMsgMhfApplyCampaign(session, pkt)

	// Verify response packet was queued (fail response expected)
	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

// Tests consolidated from handlers_core_test.go

func TestHandleMsgMhfEnumerateItem(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfEnumerateItem{
		AckHandle: 12345,
	}

	handleMsgMhfEnumerateItem(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfAcquireItem(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfAcquireItem{
		AckHandle: 12345,
	}

	handleMsgMhfAcquireItem(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestCampaignCategoryTextIsTerminated(t *testing.T) {
	got := campaignCategoryText("프리미엄 키트·오리지널", campaignCategoryTitleMax)
	body := stringsupport.UTF8ToSJIS("프리미엄 키트·오리지널")
	if !bytes.Equal(got, append(append([]byte{}, body...), 0)) {
		t.Fatalf("campaignCategoryText = % x, want the text and a terminator", got)
	}
}

func TestCampaignCategoryTextFitsClientLimits(t *testing.T) {
	for _, tt := range []struct {
		max   int
		limit int
	}{{campaignCategoryTitleMax, 0x40}, {campaignCategoryDescMax, 0x100}} {
		got := campaignCategoryText(strings.Repeat("가", 300), tt.max)
		if len(got) >= tt.limit || got[len(got)-1] != 0 || (len(got)-1)%2 != 0 {
			t.Fatalf("max %#x: %d bytes (client copies under %#x), last %#x", tt.max, len(got), tt.limit, got[len(got)-1])
		}
	}
}
