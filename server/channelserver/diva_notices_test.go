package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaNoticeNativeCapacityAndClearing(t *testing.T) {
	rows := []divaNotice{{Text: "가희수위전 안내", Start: 0x11223344, End: 0x55667788}}
	got := divaNoticePayload(rows)
	if len(got) != 0x1021 || got[0] != 4 {
		t.Fatalf("native buffer size/count: %d/%d", len(got), got[0])
	}
	if text := stringsupport.SJISToUTF8Lossy(bytes.TrimRight(got[1:1025], "\x00")); text != rows[0].Text {
		t.Fatalf("Korean text did not round-trip: %q", text)
	}
	if binary.BigEndian.Uint32(got[1025:1029]) != rows[0].Start || binary.BigEndian.Uint32(got[1029:1033]) != rows[0].End {
		t.Fatal("timestamp offsets differ from 11534450")
	}
	if !bytes.Equal(got[1033:], make([]byte, 3*1032)) {
		t.Fatal("unused cached slots were not cleared")
	}
	if empty := divaNoticePayload(nil); empty[0] != 4 || !bytes.Equal(empty[1:], make([]byte, 4128)) {
		t.Fatal("disabled notices must clear every native slot")
	}
	if oversized := divaNoticePayload(make([]divaNotice, 5)); len(oversized) != 4129 {
		t.Fatal("oversized notice list exceeds the native buffer")
	}
}

func TestDivaNoticePhaseWindows(t *testing.T) {
	options := &cfg.Config{}
	options.DebugOptions.DivaOverride = -1
	event := DivaEvent{ID: 5, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	rows := divaEventNotices(event, options)
	if len(rows) != 4 || rows[0].Start != event.StartTime || rows[3].End != event.StartTime+divaTotalLifespan-1 {
		t.Fatalf("wrong full event windows: %+v", rows)
	}
	for i, row := range rows {
		if row.Start >= row.End || row.Text == "" {
			t.Fatalf("invalid row: %+v", row)
		}
		if i > 0 && rows[i-1].End+1 != row.Start {
			t.Fatal("phase notices overlap or leave a gap")
		}
	}
	for mode := 1; mode <= 3; mode++ {
		options.DebugOptions.DivaOverride = mode
		forced := divaEventNotices(event, options)
		if len(forced) != 1 || forced[0].Start != rows[mode-1].Start {
			t.Fatalf("forced phase %d leaks another phase: %+v", mode, forced)
		}
	}
	options.DebugOptions.DivaOverride = 0
	if len(divaEventNotices(event, options)) != 0 {
		t.Fatal("disabled event advertised rewards")
	}
	options.DebugOptions.DivaOverride = -1
	for _, invalid := range []DivaEvent{{}, {ID: 1, StartTime: 0xfffffff0}} {
		if len(divaEventNotices(invalid, options)) != 0 {
			t.Fatal("invalid event/overflow generated notices")
		}
	}
}

func TestDivaNoticeTextsDescribeOnlyEnabledFeatures(t *testing.T) {
	for _, lang := range []string{"ko", "en"} {
		for _, random := range []bool{false, true} {
			for _, manual := range []int{0, 1} {
				texts := divaNoticeTexts(lang, random, manual)
				for _, text := range texts {
					wire := stringsupport.UTF8ToSJIS(text)
					if len(wire) >= 1024 || stringsupport.SJISToUTF8Lossy(wire) != text {
						t.Fatalf("truncated/unencodable %s notice: %q", lang, text)
					}
				}
			}
		}
	}
	if text := divaNoticeTexts("en", false, 0)[0]; !strings.Contains(text, "No bonus targets") || strings.Contains(text, "x2") {
		t.Fatal("disabled bonus advertised as active")
	}
	if text := divaNoticeTexts("en", true, 0)[0]; !strings.Contains(text, "x2") {
		t.Fatal("configured random bonus omitted")
	}
}

func TestDivaNoticeTextsFitNativePanel(t *testing.T) {
	// 103b3730 uses a 440px panel and 18px glyphs, inset 40px on the left.
	// Keep a 40px right margin. ASCII has native 9px/12px variants, so use
	// the wider variant. Fourteen 18px rows use 252px of its 348px height,
	// leaving 96px for top/bottom margins, including the two blank separators.
	event := DivaEvent{ID: 5, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	for _, lang := range []string{"ko", "en"} {
		for _, random := range []bool{false, true} {
			for _, manual := range []int{0, 1} {
				for _, start := range []uint32{event.StartTime, 0xffffffff - divaTotalLifespan} {
					for _, body := range divaNoticeTexts(lang, random, manual) {
						text := divaNoticeWithSchedule(body, DivaEvent{ID: 5, StartTime: start}, lang)
						wire := stringsupport.UTF8ToSJIS(text)
						if len(wire) >= 1024 || stringsupport.SJISToUTF8Lossy(wire) != text {
							t.Fatalf("full notice was truncated or misencoded: %q", text)
						}
						if lines := strings.Count(text, "\n") + 1; lines > 14 {
							t.Errorf("%s notice exceeds fourteen lines: %d", lang, lines)
						}
						for _, line := range strings.Split(text, "\n") {
							width := 0
							for _, r := range line {
								width += 12
								if r > 127 {
									width += 6
								}
							}
							if width > 360 {
								t.Errorf("%s notice line exceeds panel: %d pixels: %q", lang, width, line)
							}
						}
					}
				}
			}
		}
	}
}

func TestDivaKoreanNoticesUsePlayerGuidance(t *testing.T) {
	texts := divaNoticeTexts("ko", true, 0)
	for _, phrase := range []string{"3시간마다 바뀝니다", "2배입니다", "기주를 선택하고 사냥하면", "개인 달성 보수와 매일 보수"} {
		if !strings.Contains(texts[0], phrase) {
			t.Fatalf("missing player-facing explanation %q", phrase)
		}
	}
	for _, text := range texts {
		for _, internal := range []string{"고정", "오류", "대체", "복원", "서버 자체"} {
			if strings.Contains(text, internal) {
				t.Fatalf("original-style notice contains development status %q", internal)
			}
		}
	}
	if !strings.Contains(divaNoticeTexts("ko", false, 0)[0], "현재 보너스 타깃은 없습니다") {
		t.Fatal("notice advertises an unconfigured target")
	}
}

func TestDivaNoticeOriginalStyleAndLiveSchedule(t *testing.T) {
	// Deliberately cross midnight, month and year boundaries. The host's local
	// zone must not change the date communicated to a UTC+9 game client.
	event := DivaEvent{ID: 5, StartTime: uint32(time.Date(2026, 12, 31, 18, 0, 0, 0, time.UTC).Unix())}
	options := &cfg.Config{Language: "ko"}
	options.DebugOptions.DivaOverride = -1
	ts := generateDivaTimestamps(nil, event.StartTime, false)
	normal := divaEventNotices(event, options)
	for _, row := range normal {
		sections := strings.Split(row.Text, "\n\n")
		if len(sections) != 3 || !strings.HasPrefix(sections[1], "■ 개최 일정") || !strings.HasPrefix(sections[2], "■ ") {
			t.Fatalf("expected title / schedule / details sections: %q", row.Text)
		}
		for _, index := range []int{0, 2, 4} {
			want := time.Unix(int64(ts[index]), 0).In(divaLocation).Format("2006/01/02 15:04")
			if !strings.Contains(sections[1], want+"~") {
				t.Fatalf("notice does not match native schedule %s: %q", want, row.Text)
			}
		}
		if !strings.Contains(sections[1], "2027/01/01 03:00~") {
			t.Fatal("notice is not using the game time zone")
		}
	}
	for mode := 1; mode <= 3; mode++ {
		options.DebugOptions.DivaOverride = mode
		rows := divaEventNotices(event, options)
		if len(rows) != 1 {
			t.Fatalf("fixed phase has %d notices", len(rows))
		}
		if rows[0].Text != normal[mode-1].Text {
			t.Fatalf("fixed mode changed the original-style dated notice: %q", rows[0].Text)
		}
	}
}

func TestDivaKoreanNoticesUseFormalEndings(t *testing.T) {
	for _, random := range []bool{false, true} {
		for _, manual := range []int{0, 1} {
			for _, text := range divaNoticeTexts("ko", random, manual) {
				for _, line := range strings.Split(text, "\n") {
					if strings.HasSuffix(line, ".") && !strings.HasSuffix(line, "니다.") {
						t.Errorf("notice sentence must use a formal ending: %q", line)
					}
					if strings.ContainsAny(line, "?!") {
						t.Errorf("notice should use declarative wording: %q", line)
					}
				}
			}
		}
	}
}

func TestDivaNoticeHandler(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	srv.erupeConfig.Language = "ko"
	repo := &mockDivaRepo{events: []DivaEvent{{ID: 5, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}}}
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	request := &mhfpacket.MsgMhfGetUdInfo{AckHandle: 123}
	handleMsgMhfGetUdInfo(s, request)
	if ack := readAck(t, s); ack.ErrorCode != 0 || len(ack.Payload) != 4129 || ack.Payload[1] == 0 {
		t.Fatalf("missing scheduled notice: %+v", ack)
	}
	repo.eventsErr = errors.New("database unavailable")
	handleMsgMhfGetUdInfo(s, request)
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("notice DB error was silently treated as active data")
	}
	srv.erupeConfig.DebugOptions.DivaOverride = 0
	handleMsgMhfGetUdInfo(s, request)
	if ack := readAck(t, s); ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, divaNoticePayload(nil)) {
		t.Fatal("disable did not clear cached notices")
	}
	srv.erupeConfig.RealClientMode = cfg.Z2
	handleMsgMhfGetUdInfo(s, request)
	if ack := readAck(t, s); !bytes.Equal(ack.Payload, []byte{0}) {
		t.Fatal("unverified version received the ZZ notice layout")
	}
}
