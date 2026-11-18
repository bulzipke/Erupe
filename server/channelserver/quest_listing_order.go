package channelserver

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"math/bits"
	"slices"
	"strings"

	"erupe-ce/common/stringsupport"
	"golang.org/x/text/width"
)

type eventQuestListing struct {
	questID int
	data    []byte
}

// Entries in each row are spellings already used for that weapon in quests.
var listingWeapons = [...][]string{
	{"한손검", "片手剣"},
	{"쌍검", "双剣"},
	{"대검", "大剣"},
	{"태도", "太刀"},
	{"해머", "추", "ハンマー", "鎚"},
	{"수렵피리", "피리", "狩猟笛"},
	{"랜스", "창", "ランス", "槍"},
	{"건랜스", "총창", "ガンランス", "銃槍"},
	{"슬래시액스F", "슬래시액스", "슬랙시액스F", "검부", "スラッシュアックスF", "スラッシュアックス", "剣斧"},
	{"천룡곤", "穿龍棍"},
	{"마그넷스파이크", "자참추", "マグネットスパイク", "磁斬鎚"},
	{"라이트보우건", "경노", "경총", "ライトボウガン", "軽銃"},
	{"헤비보우건", "중노", "중총", "ヘビィボウガン", "重銃"},
	{"활", "弓"},
}

var listingTitleVariants = strings.NewReplacer("【", "[", "】", "]", "입수퀘스트", "획득퀘스트")

// Only match weapon fields, such as a final [weapon] or "weapon을 체험".
// A weapon mentioned in an ordinary quest title does not define a series.
func weaponListingTitle(title string) (series string, weapon int, ok bool) {
	title = listingTitleVariants.Replace(width.Fold.String(title))
	header, name := "", title
	if i := strings.LastIndexByte(title, '\n'); i >= 0 {
		header, name = title[:i], title[i+1:]
	}
	header = strings.Join(strings.Fields(header), "")
	name = strings.Join(strings.Fields(name), "")
	if i := strings.LastIndexByte(name, '['); i > 0 && strings.HasSuffix(name, "]") {
		variant := name[i+1 : len(name)-1]
		for rank, names := range listingWeapons {
			if slices.Contains(names, variant) {
				return header + "\n" + name[:i] + "[]", rank, true
			}
		}
	}
	for _, suffix := range []string{"을체험", "를체험", "を体験"} {
		if variant, found := strings.CutSuffix(name, suffix); found {
			for rank, names := range listingWeapons {
				if slices.Contains(names, variant) {
					return header + "\nexperience", rank, true
				}
			}
		}
	}
	return "", 0, false
}

type weaponListingSeries struct {
	title                           string
	questType                       byte
	rank, locale, objectives, entry string
}

func weaponListingFromFrame(frame []byte) (weaponListingSeries, int) {
	const headerLen = 22 // ZZ event listing header
	if len(frame) < headerLen+questBodyLenZZ+questStringTablePadding+2 {
		return weaponListingSeries{}, 0
	}
	n := int(binary.BigEndian.Uint16(frame[20:22]))
	if n < questBodyLenZZ+questStringTablePadding || len(frame) != headerLen+n+2 {
		return weaponListingSeries{}, 0
	}
	body := frame[headerLen : headerLen+n]
	table := uint64(binary.LittleEndian.Uint32(body[questStringPointerOff:]))
	if table < questBodyLenZZ || table+questStringTablePadding > uint64(n) {
		return weaponListingSeries{}, 0
	}
	start := uint64(binary.LittleEndian.Uint32(body[table:]))
	if start < table+questStringTablePadding || start >= uint64(n) {
		return weaponListingSeries{}, 0
	}
	title, _, terminated := bytes.Cut(body[start:], []byte{0})
	if !terminated {
		return weaponListingSeries{}, 0
	}
	series, rank, ok := weaponListingTitle(stringsupport.SJISToUTF8Lossy(title))
	if !ok {
		// An untranslated listing can still carry the original Japanese bytes.
		jp, _ := stringsupport.SJISToUTF8(title)
		series, rank, ok = weaponListingTitle(jp)
		if !ok {
			return weaponListingSeries{}, 0
		}
	}
	return weaponListingSeries{
		title: series, questType: frame[11],
		rank: string(body[0x08:0x0c]), locale: string(body[0x24:0x28]),
		objectives: string(body[0x30:0x48]), entry: string(body[0x4c:0x54]),
	}, rank
}

// Each series occupies the position of its highest visible quest ID. Sorting
// once before paging gives native enumeration and cache transfers the same order.
func orderEventQuestListings(listings []eventQuestListing, groupWeapons bool) [][]byte {
	type listingOrder struct {
		eventQuestListing
		series   weaponListingSeries
		position int
		weapon   int
	}
	type seriesOrder struct {
		highest int
		weapons uint16
	}
	ordered := make([]listingOrder, len(listings))
	groups := make(map[weaponListingSeries]seriesOrder)
	for i, listing := range listings {
		ordered[i] = listingOrder{eventQuestListing: listing, position: listing.questID}
		if !groupWeapons {
			continue
		}
		series, weapon := weaponListingFromFrame(listing.data)
		if series.title != "" {
			ordered[i].series, ordered[i].weapon = series, weapon
			group := groups[series]
			group.highest = max(group.highest, listing.questID)
			group.weapons |= 1 << weapon
			groups[series] = group
		}
	}
	for i := range ordered {
		if group := groups[ordered[i].series]; bits.OnesCount16(group.weapons) >= 2 {
			ordered[i].position = group.highest
		}
	}
	slices.SortStableFunc(ordered, func(a, b listingOrder) int {
		if byID := cmp.Compare(b.position, a.position); byID != 0 {
			return byID
		}
		if byWeapon := cmp.Compare(a.weapon, b.weapon); byWeapon != 0 {
			return byWeapon
		}
		return cmp.Compare(b.questID, a.questID)
	})
	ordered = orderTowerQuests(ordered, func(q listingOrder) int { return q.questID })
	frames := make([][]byte, len(ordered))
	for i, listing := range ordered {
		frames[i] = listing.data
	}
	return frames
}
