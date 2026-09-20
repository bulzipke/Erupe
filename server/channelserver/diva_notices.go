package channelserver

import (
	"fmt"
	"strings"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// ZZ 114fd150 reserves 0x1021 bytes. 11534450 copies count rows of
// {text[1024], start u32, end u32}; 103b80a0 scans all FOUR slots and selects
// the first active one. Clear unused rows: the parser retains omitted slots.
const divaNoticeSlots = 4

type divaNotice struct {
	Text       string
	Start, End uint32 // Both endpoints are inclusive in the native client.
}

func divaNoticePayload(rows []divaNotice) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(divaNoticeSlots)
	for i := 0; i < divaNoticeSlots; i++ {
		var row divaNotice
		if i < len(rows) {
			row = rows[i]
		}
		bf.WriteBytes(stringsupport.PaddedString(row.Text, 1024, true))
		bf.WriteUint32(row.Start)
		bf.WriteUint32(row.End)
	}
	return bf.Data()
}

func divaNoticeTexts(language string, random bool, manualCount int) [4]string {
	if language == "ko" {
		bonus := "현재 보너스 타깃은 없습니다."
		if random {
			bonus = "타깃은 3시간마다 바뀝니다.\n선택 기주의 타깃은 2배입니다."
		} else if manualCount > 0 {
			bonus = "선택한 기주의 보너스 타깃과\n적용 시간은 목록에서 확인 가능합니다."
		}
		return [4]string{
			fmt.Sprintf("가희수위전 · 기도의 장\n■ 기도의 장 안내\n기주를 선택하고 사냥하면\n노래 구슬이 쌓입니다.\n개인 달성 보수와 매일 보수는\n노래 구슬을 모아 받을 수 있습니다.\n%s", bonus),
			"가희수위전 · 전가의 장\n■ 전가의 장 안내\n요격 퀘스트에서 점수를 모으면\n개인 달성 보수를 받을 수 있습니다.\n기도의 장에서 획득한 보수도\n보수 메뉴에서 확인 가능합니다.\n미수령 요격 보수는 다음 요격전\n시작 전까지 수령 가능합니다.",
			"가희수위전 · 가영의 장\n■ 보수 수령 안내\n미수령 요격 개인 보수는\n다음 요격전이 시작되기 전까지\n수령할 수 있습니다.\n수령 가능한 보수는\n보수 메뉴에서 확인 가능합니다.",
			"가희수위전 · 다음 개최 준비\n■ 보수 수령 안내\n미수령 요격 개인 보수는\n다음 요격전이 시작되기 전까지\n수령할 수 있습니다.\n새 회차 보수에는 해당 회차에서\n획득한 점수만 반영됩니다.",
		}
	}
	bonus := "No bonus targets configured."
	if random {
		bonus = "Targets: every 3 hours, x2.\nCheck your bead's target."
	} else if manualCount > 0 {
		bonus = "Check your bead's targets\nand bonus times."
	}
	return [4]string{
		fmt.Sprintf("Diva Defense - Prayer\n[Prayer phase]\nChoose a bead and go hunting\nto collect song gems.\nCollect gems to earn milestone\nand daily rewards.\n%s", bonus),
		"Diva Defense - Interception\n[Interception phase]\nEarn points on battle quests\nto receive milestone rewards.\nPrayer rewards can also be\nchecked at the reward counter.\nClaim pending battle rewards\nbefore the next battle begins.",
		"Diva Defense - Reception\n[Reward collection]\nUnclaimed battle rewards can\nbe collected before the next\ninterception event begins.\nCheck the reward counter\nfor available rewards.",
		"Diva Defense - Between rounds\n[Reward collection]\nClaim pending battle rewards\nbefore the next battle begins.\nNew-round rewards use points\nearned during that round.",
	}
}

// Match the original notice's title / schedule / details layout. Use the same
// timestamps as GET_UD_SCHEDULE, not maintenance dates from historical notices.
func divaNoticeWithSchedule(text string, event DivaEvent, language string) string {
	title, body, _ := strings.Cut(text, "\n")
	ts := generateDivaTimestamps(nil, event.StartTime, false)
	date := func(timestamp uint32) string {
		return time.Unix(int64(timestamp), 0).In(divaLocation).Format("2006/01/02 15:04")
	}
	var schedule string
	if language == "ko" {
		schedule = fmt.Sprintf("■ 개최 일정 (한국 시간)\n기도: %s~\n전가: %s~\n가영: %s~", date(ts[0]), date(ts[2]), date(ts[4]))
	} else {
		schedule = fmt.Sprintf("[Schedule: UTC+9]\nPrayer: %s~\nBattle: %s~\nReception: %s~", date(ts[0]), date(ts[2]), date(ts[4]))
	}
	return title + "\n\n" + schedule + "\n\n" + body
}

func divaEventNotices(event DivaEvent, options *cfg.Config) []divaNotice {
	mode := options.DebugOptions.DivaOverride
	if event.ID == 0 || event.StartTime == 0 || mode == 0 || mode < -1 || mode > 3 ||
		uint64(event.StartTime)+divaTotalLifespan > 0xffffffff {
		return nil
	}
	texts := divaNoticeTexts(options.Language, options.GameplayOptions.DivaBonusRandom,
		len(options.GameplayOptions.DivaBonusTargets))
	start := event.StartTime
	// Cache all phase notices; the client changes the active text by its clock.
	// Include interludes in the preceding notice, with no overlapping endpoints.
	boundaries := [5]uint32{start, start + divaPhaseDuration + divaInterlude,
		start + divaPhaseDuration + divaWeekDuration + divaInterlude,
		start + divaPhaseDuration + 2*divaWeekDuration, start + divaTotalLifespan}
	var rows []divaNotice
	for i := 0; i < divaNoticeSlots; i++ {
		if mode > 0 && i != mode-1 {
			continue
		}
		end := boundaries[i+1]
		if mode == 1 {
			end = start + divaPhaseDuration
		} else if mode == 2 {
			end = start + divaPhaseDuration + divaWeekDuration
		}
		rows = append(rows, divaNotice{Text: divaNoticeWithSchedule(texts[i], event, options.Language), Start: boundaries[i], End: end - 1})
	}
	return rows
}

func handleDivaNotices(s *Session, pkt *mhfpacket.MsgMhfGetUdInfo) {
	options := s.server.erupeConfig
	if options.RealClientMode != cfg.ZZ {
		doAckBufSucceed(s, pkt.AckHandle, []byte{0})
		return // Other versions' native capacities have not been verified.
	}
	if options.DebugOptions.DivaOverride == 0 {
		doAckBufSucceed(s, pkt.AckHandle, divaNoticePayload(nil))
		return
	}
	event, err := s.divaEvent()
	if err != nil {
		s.logger.Warn("Failed to resolve Diva notices", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, divaNoticePayload(divaEventNotices(event, options)))
}
