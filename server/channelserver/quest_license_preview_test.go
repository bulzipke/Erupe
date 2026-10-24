package channelserver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

func questLicensePreviewFixture() QuestLicensePreview {
	return QuestLicensePreview{
		Quote: QuestLicenseQuote{CampaignID: 123, Price: 50, QuestIDs: []int{40081, 65000},
			PaymentCode: "^0032AQIDBAUGBwg", ExpiresAt: time.Date(2026, 10, 23, 12, 2, 0, 0, time.UTC)},
		Title: "\uac00\ub098",
		Quests: []QuestLicensePreviewQuest{
			{ID: 40081, Name: "\ub2e4\ub77c", Conditions: "HR5", Publication: "2026-10-23"},
			{ID: 65000, Name: "Quest 65000"},
		},
	}
}

func readQuestLicensePreviewText(t *testing.T, reader *bytes.Reader) string {
	t.Helper()
	var size uint16
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, size)
	if _, err := io.ReadFull(reader, encoded); err != nil {
		t.Fatal(err)
	}
	value, _, err := transform.String(korean.EUCKR.NewDecoder(), string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestQuestLicensePreviewWireAndCP949Roundtrip(t *testing.T) {
	for _, ownership := range []QuestLicenseOwnership{QuestLicenseOwnershipNone, QuestLicenseOwnershipOwned, QuestLicenseOwnershipPartial} {
		preview := questLicensePreviewFixture()
		preview.Ownership = ownership
		data, err := encodeQuestLicensePreview(preview)
		if err != nil {
			t.Fatal(err)
		}
		if string(data[:4]) != "LPV1" || binary.BigEndian.Uint16(data[4:6]) != 1 ||
			binary.BigEndian.Uint16(data[6:8]) != 3 || data[8] != uint8(ownership) || data[9] != 0 ||
			binary.BigEndian.Uint32(data[10:14]) != 123 || binary.BigEndian.Uint16(data[14:16]) != 50 ||
			binary.BigEndian.Uint16(data[16:18]) != 2 || binary.BigEndian.Uint32(data[18:22]) != uint32(preview.Quote.ExpiresAt.Unix()) ||
			string(data[22:38]) != preview.Quote.PaymentCode {
			t.Fatal("typed preview header differs")
		}
		if !bytes.Equal(data[40:44], []byte{0xb0, 0xa1, 0xb3, 0xaa}) {
			t.Fatal("title was not encoded as CP949")
		}
		reader := bytes.NewReader(data[38:])
		if readQuestLicensePreviewText(t, reader) != preview.Title {
			t.Fatal("title did not roundtrip")
		}
		for _, quest := range preview.Quests {
			var id uint16
			if err := binary.Read(reader, binary.BigEndian, &id); err != nil {
				t.Fatal(err)
			}
			if int(id) != quest.ID || readQuestLicensePreviewText(t, reader) != quest.Name ||
				readQuestLicensePreviewText(t, reader) != quest.Conditions || readQuestLicensePreviewText(t, reader) != quest.Publication {
				t.Fatal("quest text/ID did not roundtrip")
			}
		}
		if reader.Len() != 0 {
			t.Fatal("trailing bytes in typed preview")
		}
		// 65000 is intentionally above the native inventory item-name limit.
		if preview.Quote.QuestIDs[1] != 65000 || preview.Quests[1].ID != 65000 {
			t.Fatal("serializer changed its input IDs")
		}
	}
}

func TestQuestLicensePreviewRejectsInvalidContractsWithoutPartialBytes(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*QuestLicensePreview)
	}{
		{"zero campaign", func(p *QuestLicensePreview) { p.Quote.CampaignID = 0 }},
		{"signed campaign overflow", func(p *QuestLicensePreview) { p.Quote.CampaignID = 1 << 31 }},
		{"zero price", func(p *QuestLicensePreview) { p.Quote.Price = 0 }},
		{"price overflow", func(p *QuestLicensePreview) { p.Quote.Price = 65536 }},
		{"quote price mismatch", func(p *QuestLicensePreview) { p.Quote.PaymentCode = "^0033AQIDBAUGBwg" }},
		{"kit code namespace", func(p *QuestLicensePreview) { p.Quote.PaymentCode = "!0032AQIDBAUGBwg" }},
		{"noncanonical token", func(p *QuestLicensePreview) { p.Quote.PaymentCode = "^0032AQIDBAUGBwh" }},
		{"empty token", func(p *QuestLicensePreview) { p.Quote.PaymentCode = "" }},
		{"zero expiry", func(p *QuestLicensePreview) { p.Quote.ExpiresAt = time.Time{} }},
		{"epoch expiry", func(p *QuestLicensePreview) { p.Quote.ExpiresAt = time.Unix(0, 999) }},
		{"negative expiry", func(p *QuestLicensePreview) { p.Quote.ExpiresAt = time.Unix(-1, 0) }},
		{"expiry overflow", func(p *QuestLicensePreview) { p.Quote.ExpiresAt = time.Unix(math.MaxUint32+1, 0) }},
		{"invalid ownership", func(p *QuestLicensePreview) { p.Ownership = 3 }},
		{"no quests", func(p *QuestLicensePreview) { p.Quests = nil; p.Quote.QuestIDs = nil }},
		{"different counts", func(p *QuestLicensePreview) { p.Quote.QuestIDs = p.Quote.QuestIDs[:1] }},
		{"different IDs", func(p *QuestLicensePreview) { p.Quote.QuestIDs[1] = 65001 }},
		{"zero ID", func(p *QuestLicensePreview) { p.Quests[0].ID = 0; p.Quote.QuestIDs[0] = 0 }},
		{"ID overflow", func(p *QuestLicensePreview) { p.Quests[1].ID = 65536; p.Quote.QuestIDs[1] = 65536 }},
		{"duplicate IDs", func(p *QuestLicensePreview) {
			p.Quests[1].ID = p.Quests[0].ID
			p.Quote.QuestIDs[1] = p.Quote.QuestIDs[0]
		}},
		{"unsorted IDs", func(p *QuestLicensePreview) {
			p.Quests[0], p.Quests[1] = p.Quests[1], p.Quests[0]
			p.Quote.QuestIDs[0], p.Quote.QuestIDs[1] = p.Quote.QuestIDs[1], p.Quote.QuestIDs[0]
		}},
		{"empty title", func(p *QuestLicensePreview) { p.Title = "" }},
		{"blank name", func(p *QuestLicensePreview) { p.Quests[0].Name = "   " }},
		{"title byte limit", func(p *QuestLicensePreview) { p.Title = strings.Repeat("T", 97) }},
		{"title CP949 byte limit", func(p *QuestLicensePreview) { p.Title = strings.Repeat("\uac00", 49) }},
		{"name byte limit", func(p *QuestLicensePreview) { p.Quests[0].Name = strings.Repeat("N", 121) }},
		{"conditions byte limit", func(p *QuestLicensePreview) { p.Quests[0].Conditions = strings.Repeat("C", 513) }},
		{"publication byte limit", func(p *QuestLicensePreview) { p.Quests[0].Publication = strings.Repeat("P", 513) }},
		{"title NUL", func(p *QuestLicensePreview) { p.Title = "A\x00B" }},
		{"name TAB", func(p *QuestLicensePreview) { p.Quests[0].Name = "A\tB" }},
		{"conditions newline", func(p *QuestLicensePreview) { p.Quests[0].Conditions = "A\nB" }},
		{"publication control", func(p *QuestLicensePreview) { p.Quests[0].Publication = "A\x7fB" }},
		{"format control", func(p *QuestLicensePreview) { p.Quests[0].Name = "A\u00adB" }},
		{"unrepresentable title", func(p *QuestLicensePreview) { p.Title = "\U0001f600" }},
		{"unrepresentable final row", func(p *QuestLicensePreview) { p.Quests[1].Publication = "\U0001f600" }},
		{"invalid UTF8", func(p *QuestLicensePreview) { p.Title = string([]byte{0xff}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			preview := questLicensePreviewFixture()
			test.change(&preview)
			if data, err := encodeQuestLicensePreview(preview); err == nil || data != nil {
				t.Fatal("invalid contract exposed a partial preview", len(data), err)
			}
		})
	}
	preview := questLicensePreviewFixture()
	preview.Quests = make([]QuestLicensePreviewQuest, questLicensePreviewMaxRows+1)
	preview.Quote.QuestIDs = make([]int, len(preview.Quests))
	if data, err := encodeQuestLicensePreview(preview); err == nil || data != nil {
		t.Fatal("unbounded row list accepted")
	}
}

func TestQuestLicensePreviewReplyCapacityBoundary(t *testing.T) {
	preview := questLicensePreviewFixture()
	preview.Title = strings.Repeat("T", 96)
	preview.Quests = make([]QuestLicensePreviewQuest, 15)
	preview.Quote.QuestIDs = make([]int, len(preview.Quests))
	for i := range preview.Quests {
		preview.Quote.QuestIDs[i] = 20000 + i
		preview.Quests[i] = QuestLicensePreviewQuest{ID: 20000 + i,
			Name: strings.Repeat("N", 120), Conditions: strings.Repeat("C", 512), Publication: strings.Repeat("P", 512)}
	}
	preview.Quests[14].Name = "N"
	preview.Quests[14].Conditions = strings.Repeat("C", 111)
	preview.Quests[14].Publication = ""
	data, err := encodeQuestLicensePreview(preview)
	if err != nil || len(data) != 0x4000 {
		t.Fatal("exact native reply capacity rejected", len(data), err)
	}
	preview.Quests[14].Conditions += "C"
	if data, err = encodeQuestLicensePreview(preview); err == nil || data != nil {
		t.Fatal("reply overflow exposed bytes", len(data), err)
	}
}

func TestQuestLicensePreviewExpiryWireBounds(t *testing.T) {
	preview := questLicensePreviewFixture()
	for _, seconds := range []int64{1, math.MaxUint32} {
		// The same instant near a year boundary must not depend on local offset.
		preview.Quote.ExpiresAt = time.Unix(seconds, 999999999).In(time.FixedZone("offset", -9*60*60))
		data, err := encodeQuestLicensePreview(preview)
		if err != nil || binary.BigEndian.Uint32(data[18:22]) != uint32(seconds) {
			t.Fatal("expiry seconds were not bounded/normalized", seconds, err)
		}
	}
}

func TestQuestLicensePreviewConcurrentDetachedBuffers(t *testing.T) {
	preview := questLicensePreviewFixture()
	want, err := encodeQuestLicensePreview(preview)
	if err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 16)
	var workers sync.WaitGroup
	for i := 0; i < cap(errors); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 50 {
				data, err := encodeQuestLicensePreview(preview)
				if err != nil || !bytes.Equal(data, want) {
					errors <- fmt.Errorf("concurrent typed preview changed: %v", err)
					return
				}
				data[0] = 'X'
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
