package channelserver

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"math/bits"
	"slices"
	"strconv"
	"strings"

	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
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

const rankedServerQuestName = "서버 전용 퀘스트"
const milestoneQuestName = "돌파 기념"

// Numbered event families share one title position, then sort by their number.
// Match the whole naming pattern so other quests starting with digits stay separate.
func numberedQuestSeries(title string) (name string, number uint64) {
	prefix, ok := strings.CutSuffix(title, "위 "+rankedServerQuestName)
	name = rankedServerQuestName
	if !ok {
		var ending string
		prefix, ending, ok = strings.Cut(title, "만 "+milestoneQuestName)
		if !ok || (ending != "" && ending != "!" && ending != "이다냥!") {
			return title, 0
		}
		name = milestoneQuestName
	}
	number, err := strconv.ParseUint(strings.TrimSpace(prefix), 10, 32)
	if err != nil || number == 0 {
		return title, 0
	}
	return name, number
}

// A title can start with the category line shown above its actual name.
func splitQuestListingTitle(title string) (category, name string) {
	title = strings.TrimSpace(width.Fold.String(title))
	if rest, ok := strings.CutPrefix(title, "≪"); ok {
		if header, tail, found := strings.Cut(rest, "≫"); found && strings.TrimSpace(tail) != "" {
			return header, strings.TrimSpace(tail)
		}
	}
	return "", title
}

// Only match weapon fields, such as a final [weapon] or "weapon을 체험".
// A weapon mentioned in an ordinary quest title does not define a series.
func weaponListingTitle(header, name string) (seriesName string, weapon int, ok bool) {
	name = listingTitleVariants.Replace(strings.Join(strings.Fields(name), ""))
	if i := strings.LastIndexByte(name, '['); i > 0 && strings.HasSuffix(name, "]") {
		variant := name[i+1 : len(name)-1]
		for rank, names := range listingWeapons {
			if slices.Contains(names, variant) {
				return name[:i], rank, true
			}
		}
	}
	for _, suffix := range []string{"을체험", "를체험", "を体験"} {
		if variant, found := strings.CutSuffix(name, suffix); found {
			for rank, names := range listingWeapons {
				if slices.Contains(names, variant) {
					if header != "" {
						return header, rank, true
					}
					return strings.TrimLeft(suffix, "을를を"), rank, true
				}
			}
		}
	}
	return "", 0, false
}

type weaponListingSeries struct {
	category, name                  string
	questType                       byte
	rank, locale, objectives, entry string
}

func questListingName(frame []byte, mode cfg.Mode) (name string, series weaponListingSeries, weapon int) {
	headerLen := 18
	if mode >= cfg.G2 {
		headerLen = 22
	}
	if len(frame) < headerLen {
		return "", weaponListingSeries{}, 0
	}
	n := int(binary.BigEndian.Uint16(frame[headerLen-2 : headerLen]))
	if n < questBodyLenS6+questStringTablePadding || len(frame) != headerLen+n+2 {
		return "", weaponListingSeries{}, 0
	}
	body := frame[headerLen : headerLen+n]
	table := uint64(binary.LittleEndian.Uint32(body[questStringPointerOff:]))
	if table < questBodyLenS6 || table+questStringTablePadding > uint64(n) {
		return "", weaponListingSeries{}, 0
	}
	start := uint64(binary.LittleEndian.Uint32(body[table:]))
	if start < table+questStringTablePadding || start >= uint64(n) {
		return "", weaponListingSeries{}, 0
	}
	title, _, terminated := bytes.Cut(body[start:], []byte{0})
	if !terminated {
		return "", weaponListingSeries{}, 0
	}
	category, name := splitQuestListingTitle(stringsupport.SJISToUTF8Lossy(title))
	name = strings.Join(strings.Fields(name), " ")
	if mode != cfg.ZZ {
		return name, weaponListingSeries{}, 0
	}
	seriesName, rank, ok := weaponListingTitle(category, name)
	if !ok {
		// An untranslated listing can still carry the original Japanese bytes.
		jp, _ := stringsupport.SJISToUTF8(title)
		jpCategory, jpName := splitQuestListingTitle(jp)
		seriesName, rank, ok = weaponListingTitle(jpCategory, jpName)
		if !ok {
			return name, weaponListingSeries{}, 0
		}
		category, name = jpCategory, strings.Join(strings.Fields(jpName), " ")
	}
	return name, weaponListingSeries{
		category: listingTitleVariants.Replace(strings.Join(strings.Fields(category), "")), name: seriesName, questType: frame[11],
		rank: string(body[0x08:0x0c]), locale: string(body[0x24:0x28]),
		objectives: string(body[0x30:0x48]), entry: string(body[0x4c:0x54]),
	}, rank
}

// Sort ascending by the displayed name or the shared name of a weapon series. Paging
// and cache transfer both consume the resulting ordered frames.
func orderEventQuestListings(listings []eventQuestListing, mode cfg.Mode) [][]byte {
	type listingOrder struct {
		eventQuestListing
		series       weaponListingSeries
		nameKey      []byte
		anchorID     int
		weapon       int
		seriesNumber uint64
	}
	type seriesOrder struct {
		nameKey  []byte
		anchorID int
		weapons  uint16
	}
	collator := collate.New(language.Korean, collate.IgnoreCase, collate.Numeric)
	var keys collate.Buffer
	ordered := make([]listingOrder, len(listings))
	groups := make(map[weaponListingSeries]seriesOrder)
	for i, listing := range listings {
		name, series, weapon := questListingName(listing.data, mode)
		name, number := numberedQuestSeries(name)
		ordered[i] = listingOrder{eventQuestListing: listing, series: series, anchorID: listing.questID, weapon: weapon, seriesNumber: number}
		if name != "" {
			ordered[i].nameKey = collator.KeyFromString(&keys, name)
		}
		if series.name != "" {
			group := groups[series]
			if group.nameKey == nil {
				group.nameKey = collator.KeyFromString(&keys, series.name)
			}
			group.anchorID = max(group.anchorID, listing.questID)
			group.weapons |= 1 << weapon
			groups[series] = group
		}
	}
	for i := range ordered {
		if group := groups[ordered[i].series]; bits.OnesCount16(group.weapons) >= 2 {
			ordered[i].nameKey, ordered[i].anchorID = group.nameKey, group.anchorID
		}
	}
	slices.SortStableFunc(ordered, func(a, b listingOrder) int {
		if a.nameKey == nil && b.nameKey != nil {
			return 1
		}
		if a.nameKey != nil && b.nameKey == nil {
			return -1
		}
		if byName := bytes.Compare(a.nameKey, b.nameKey); byName != 0 {
			return byName
		}
		if byNumber := cmp.Compare(a.seriesNumber, b.seriesNumber); byNumber != 0 {
			return byNumber
		}
		if byID := cmp.Compare(b.anchorID, a.anchorID); byID != 0 {
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
