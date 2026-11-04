package channelserver

// The 18 selectable retail prayer beads. Internal effects 2,5,6,7,12,13
// and the independent shop effect 16 are not selectable prayer beads.
type divaBeadDefinition struct {
	ID, MaxLevel                                int
	JP, KO, EN                                  string
	DescriptionJP, DescriptionKO, DescriptionEN string
}

var divaBeadCatalog = [...]divaBeadDefinition{
	{1, 3, "暴風の祈珠", "폭풍의 기주", "Storm Prayer Bead", "あらゆるアクションによる\n斬れ味の減少を無効化します。\nまた、斬れ味レベル+1の効果が発動します。", "예리도 감소를 무효화하고\n예리도 레벨+1이 발동합니다.", "Prevents sharpness loss and grants Sharpness +1."},
	{3, 1, "断力の祈珠", "단력의 기주", "Severing Prayer Bead", "斬属性以外の攻撃でも\nモンスターの尻尾を切断できるようになります。", "절단 계열 이외의 공격으로도\n꼬리를 절단할 수 있습니다.", "Allows attacks other than cutting damage to sever tails."},
	{4, 2, "風韻の祈珠", "풍운의 기주", "Melody Prayer Bead", "体力ゲージの赤い部分以外も自動的に回復します。\n火事場力発動中は一定値以上の体力が必要です。\nまた、スタミナの回復速度が上昇します。", "체력 자동회복과 스태미나\n회복속도 증가 효과가 발동합니다.\n재난대처능력 시 체력 조건 있음.", "Automatically recovers health and increases stamina recovery speed."},
	{8, 3, "斬刃の祈珠", "참인의 기주", "Cutting Prayer Bead", "斬属性の武器で攻撃した時、\nダメージがより通りやすくなります。\nただし、属性ダメージは変化しません。", "절단 계열의 물리 대미지가\n증가합니다. 속성은 제외합니다.", "Increases cutting damage, excluding elemental damage."},
	{9, 3, "打明の祈珠", "타명의 기주", "Blunt Prayer Bead", "打属性の武器で攻撃した時、\nダメージがより通りやすくなります。\nただし、属性ダメージは変化しません。", "타격 계열의 물리 대미지가\n증가합니다. 속성은 제외합니다.", "Increases blunt damage, excluding elemental damage."},
	{10, 3, "弾起の祈珠", "탄기의 기주", "Shot Prayer Bead", "弾属性の武器で攻撃した時、\nダメージがより通りやすくなります。\nただし、属性ダメージは変化しません。", "탄 계열의 물리 대미지가\n증가합니다. 속성은 제외합니다.", "Increases shot damage, excluding elemental damage."},
	{11, 3, "変続の祈珠", "변속의 기주", "Continuance Prayer Bead", "モンスターが状態異常にかかった際の\n持続時間が長くなります。", "몬스터의 상태이상\n지속시간이 길어집니다.", "Extends monster status ailment durations."},
	{14, 3, "万雷の祈珠", "만뢰의 기주", "Thunder Prayer Bead", "モンスターへの属性ダメージが増加します。", "몬스터에게 주는\n속성 대미지가 증가합니다.", "Increases elemental damage dealt to monsters."},
	{15, 1, "不動の祈珠", "부동의 기주", "Immovable Prayer Bead", "モンスターが戦闘状態の間、そのモンスターは\n別のエリアに移動しなくなります。\n瀕死状態や特定のモンスターには無効です。", "전투 중인 몬스터의\n구역 이동을 막습니다.\n빈사·특정 몬스터에게는 무효.", "Prevents area changes during combat; some monsters and weakened monsters are excluded."},
	{17, 3, "結集の祈珠", "결집의 기주", "Unity Prayer Bead", "クエストに参加しているプレイヤーキャラクター\nの人数に応じて、攻撃力が上昇します。", "퀘스트의 플레이어 수에 따라\n공격력이 오릅니다. NPC는 제외.", "Increases attack based on the number of player hunters, excluding NPCs."},
	{18, 3, "歌護の祈珠", "가호의 기주", "Protection Prayer Bead", "様々な保護系の効果が発動します。", "여신의 보호 효과가 발동합니다.\n최대 레벨에서 전투불능을\n1회 막습니다.", "Grants protective effects according to the bead level."},
	{19, 3, "強撃の祈珠", "강격의 기주", "Critical Prayer Bead", "武器の会心率が上がります。", "무기의 회심률이 오릅니다.", "Increases weapon affinity."},
	{20, 3, "封火の祈珠", "봉화의 기주", "Fire Ward Prayer Bead", "火属性への耐性が上がります。", "화속성 내성이 오릅니다.", "Increases fire resistance."},
	{21, 3, "封水の祈珠", "봉수의 기주", "Water Ward Prayer Bead", "水属性への耐性が上がります。", "수속성 내성이 오릅니다.", "Increases water resistance."},
	{22, 3, "封氷の祈珠", "봉빙의 기주", "Ice Ward Prayer Bead", "氷属性への耐性が上がります。", "빙속성 내성이 오릅니다.", "Increases ice resistance."},
	{23, 3, "封雷の祈珠", "봉뢰의 기주", "Thunder Ward Prayer Bead", "雷属性への耐性が上がります。", "뇌속성 내성이 오릅니다.", "Increases thunder resistance."},
	{24, 3, "封龍の祈珠", "봉룡의 기주", "Dragon Ward Prayer Bead", "龍属性への耐性が上がります。", "용속성 내성이 오릅니다.", "Increases dragon resistance."},
	{25, 2, "封属の祈珠", "봉속의 기주", "Elemental Ward Prayer Bead", "すべての属性への耐性が上がります。", "모든 속성 내성이 오릅니다.", "Increases all elemental resistances."},
}

func divaBeadDefinitionFor(effect int) (divaBeadDefinition, bool) {
	for _, b := range divaBeadCatalog {
		if b.ID == effect {
			return b, true
		}
	}
	return divaBeadDefinition{}, false
}

// DivaBeadNameKO shares the game's verified catalog with dashboard displays.
func DivaBeadNameKO(effect int) string {
	b, ok := divaBeadDefinitionFor(effect)
	if !ok {
		return ""
	}
	return b.KO
}

// DivaBeadLevels matches native 1039D8F0 -> 103A0BC0: count the eight
// published winning colors and cap each slot at its effect's maximum level.
// No victories means level zero; missing evidence is not a fabricated level 1.
func DivaBeadLevels(effects []int, colors []byte) map[int]int {
	if len(effects) == 0 || len(effects) > 4 || len(colors) != 8 {
		return nil
	}
	levels := make(map[int]int, len(effects))
	for slot, effect := range effects {
		bead, valid := divaBeadDefinitionFor(effect)
		if _, duplicate := levels[effect]; !valid || duplicate {
			return nil
		}
		wins := 0
		for _, color := range colors {
			if int(color) == slot+1 {
				wins++
			}
		}
		levels[effect] = min(wins, bead.MaxLevel)
	}
	return levels
}

// English is the fallback for locales without a verified prayer-bead catalog.
// The old locale tables contained invented names and unrelated effect descriptions.
func divaLocalizedBeads(language string) []Bead {
	result := make([]Bead, 0, len(divaBeadCatalog))
	for _, b := range divaBeadCatalog {
		name, desc := b.EN, b.DescriptionEN
		if language == "jp" {
			name, desc = b.JP, b.DescriptionJP
		}
		if language == "ko" {
			name, desc = b.KO, b.DescriptionKO
		}
		result = append(result, Bead{b.ID, name, desc})
	}
	return result
}
