package channelserver

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
)

func TestEventQuestOrderInPagesAndClientCache(t *testing.T) {
	s := deliveryTestSession(t, `[]`)
	type quest struct {
		id        int
		title     string
		objective byte
	}
	quests := []quest{
		{22104, "천룡곤을 체험", 1}, {22096, "대검을 체험", 1},
		{22147, "마그넷스파이크를 체험", 1}, {22106, "헤비보우건을 체험", 1},
		{22099, "수렵피리를 체험", 1}, {22103, "슬래시액스Ｆ를 체험", 1},
		{22102, "건랜스를 체험", 1}, {22094, "한손검을 체험", 1},
		{22107, "활을 체험", 1}, {22101, "랜스를 체험", 1},
		{22098, "해머를 체험", 1}, {22105, "라이트보우건을 체험", 1},
		{22097, "태도를 체험", 1}, {22095, "쌍검을 체험", 1},
		{22150, "태도를 체험", 2}, // Different objective and therefore a different series.
		{22130, "일반 행사", 1},
		{22065, "무기훈 획득 퀘스트[자참추]", 1},
		{22067, "무기훈입수퀘스트【경노】", 1},
		{22068, "무기훈 획득 퀘스트[한손검]", 1},
		// Live sample, with category labels that would scramble the name sort.
		{56162, "≪G★7 이벤트≫\n내 낭만이 모든 걸 부순다", 1},
		{56161, "≪G★4 이벤트≫\n빨강고양이의 종언", 1},
		{56159, "≪G★7 이벤트≫\n카트레아의 추천", 1},
		{56158, "≪초난관 퀘스트≫\n초무쌍 습격 고전[작영룡]", 1},
		{56157, "≪G★7 이벤트≫\n축제의 열기[혼석Ⅳ]", 1},
		{56153, "≪초난관 퀘스트≫\n초무쌍 습격 연전Ⅷ[작영룡]", 1},
		{56152, "≪초난관 퀘스트≫\n초무쌍 습격 연전Ⅶ[폭무룡]", 1},
		{50654, "[ＨＲ１７]도달 기념보수３", 1},
		{50655, "[ＨＲ２２]도달 기념보수４", 1},
		{50652, "[ＨＲ３]도달 기념보수１", 1},
		{50656, "[ＨＲ３１]도달 기념보수５", 1},
		{50653, "[ＨＲ５]도달 기념보수２", 1},
		{50657, "[ＨＲ５１]도달 기념보수６", 1},
		{64010, "[HR17]도달 기념보수10", 1},
		{63099, "≪프론티어 퀘스트≫\n3만 돌파 기념!", 1},
		{61447, "≪프론티어 퀘스트≫\n3위 서버 전용 퀘스트", 1},
		{61560, "≪프론티어 퀘스트≫\n2위 서버 전용 퀘스트", 2},
		{61445, "≪프론티어 퀘스트≫\n1위 서버 전용 퀘스트", 1},
		{61446, "≪프론티어 퀘스트≫\n2위 서버 전용 퀘스트", 1},
		{61561, "≪프론티어 퀘스트≫\n3위 서버 전용 퀘스트", 2},
		{62416, "≪프론티어 퀘스트≫\n2만 돌파 기념!", 1},
		{61559, "≪프론티어 퀘스트≫\n１위 서버 전용 퀘스트", 2},
		{62018, "≪프론티어 퀘스트≫\n1만 돌파 기념이다냥!", 1},
		{40206, "≪프론티어 퀘스트≫\n4만 돌파 기념!", 1},
		{64011, "２만 달성!", 1},
		{64012, "3만 돌파 기념품", 1},
	}
	var rows []EventQuest
	const stringTable = questBodyLenZZ
	const titleStart = stringTable + questStringTablePadding
	for _, q := range quests {
		questType, title := uint8(18), q.title
		if strings.HasPrefix(title, "≪") {
			questType = 28
		} else {
			title = "≪이벤트 퀘스트≫\n" + title
		}
		rows = append(rows, EventQuest{ID: uint32(q.id + 1), QuestID: q.id, MaxPlayers: 4, QuestType: questType, Flags: -1})
		body := make([]byte, titleStart)
		binary.LittleEndian.PutUint32(body[questStringPointerOff:], stringTable)
		binary.LittleEndian.PutUint32(body[stringTable:], titleStart)
		binary.LittleEndian.PutUint16(body[0x2e:], uint16(q.id))
		body[0x30] = q.objective
		body = append(body, stringsupport.UTF8ToSJIS(title)...)
		body = append(body, 0)
		s.server.questCache.Put(q.id, s.Lang(), body)
	}
	s.server.eventRepo.(*mockEventRepo).eventQuests = rows
	snapshot, err := s.questDeliverySnapshot(0, deliveryTestTime())
	if err != nil {
		t.Fatal(err)
	}
	want := []int{
		50652, 50653, 50654, 64010, 50655, 50656, 50657,
		64011, 64012, 56162, 62018, 62416, 63099, 40206, 22068, 22065, 22067, 56161,
		61559, 61445, 61560, 61446, 61561, 61447,
		22094, 22095, 22096, 22097, 22098, 22099, 22101, 22102, 22103, 22104, 22147, 22105, 22106, 22107,
		22130, 56158, 56152, 56153, 56157, 56159, 22150,
	}
	got := make([]int, 0, len(snapshot.entries))
	for _, frame := range snapshot.entries {
		got = append(got, int(binary.LittleEndian.Uint16(frame[68:70])))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("event list IDs = %v, want %v", got, want)
	}
	page := byteframe.NewByteFrame()
	total, returned := snapshot.appendPage(page, 11)
	if int(total) != len(want) || int(returned) != len(want)-11 || int(binary.LittleEndian.Uint16(page.Data()[68:70])) != want[11] {
		t.Fatal("page offset did not use the sorted list")
	}
	cache, err := newQuestClientCacheTransfer(snapshot, make([]byte, 4))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range want {
		if actual := int(binary.BigEndian.Uint16(cache.manifest[2+i*36:])); actual != id {
			t.Fatalf("cache position %d = %d, want %d", i, actual, id)
		}
	}
}
