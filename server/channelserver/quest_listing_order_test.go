package channelserver

import (
	"encoding/binary"
	"slices"
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
	}
	var rows []EventQuest
	const stringTable = questBodyLenZZ
	const titleStart = stringTable + questStringTablePadding
	for _, q := range quests {
		rows = append(rows, EventQuest{ID: uint32(q.id + 1), QuestID: q.id, MaxPlayers: 4, QuestType: 18, Flags: -1})
		body := make([]byte, titleStart)
		binary.LittleEndian.PutUint32(body[questStringPointerOff:], stringTable)
		binary.LittleEndian.PutUint32(body[stringTable:], titleStart)
		binary.LittleEndian.PutUint16(body[0x2e:], uint16(q.id))
		body[0x30] = q.objective
		body = append(body, stringsupport.UTF8ToSJIS("≪이벤트 퀘스트≫\n"+q.title)...)
		body = append(body, 0)
		s.server.questCache.Put(q.id, s.Lang(), body)
	}
	s.server.eventRepo.(*mockEventRepo).eventQuests = rows
	snapshot, err := s.questDeliverySnapshot(0, deliveryTestTime())
	if err != nil {
		t.Fatal(err)
	}
	want := []int{22150, 22094, 22095, 22096, 22097, 22098, 22099, 22101, 22102, 22103, 22104, 22147, 22105, 22106, 22107, 22130, 22068, 22065, 22067}
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
