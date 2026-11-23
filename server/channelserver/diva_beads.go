package channelserver

// The 18 selectable retail prayer beads. Internal effects 2,5,6,7,12,13
// and the independent shop effect 16 are not selectable prayer beads.
type divaBeadDefinition struct {
	ID, MaxLevel                                int
	JP, KO, EN                                  string
	DescriptionJP, DescriptionKO, DescriptionEN string
}

// Korean descriptions are the introductions shown under the bead titles.
// The introductions follow archived Japanese gameplay and period transcriptions.
// Provenance is documented in the Korean localization project.
var divaBeadCatalog = [...]divaBeadDefinition{
	{1, 3, "暴風の祈珠", "폭풍의 기주", "Storm Prayer Bead", "あらゆるアクションによる\n斬れ味の減少を無効化します。\nまた、斬れ味レベル+1の効果が発動します。", "폭풍은 거세게 이는 마음.\n듣는 이에게 용기를 준다.", "Prevents sharpness loss and grants Sharpness +1."},
	{3, 1, "断力の祈珠", "단력의 기주", "Severing Prayer Bead", "斬属性以外の攻撃でも\nモンスターの尻尾を切断できるようになります。", "단력은 끊어 내고자 하는 의지.\n듣는 이에게 새로운 힘을 준다.", "Allows attacks other than cutting damage to sever tails."},
	{4, 2, "風韻の祈珠", "풍운의 기주", "Melody Prayer Bead", "体力ゲージの赤い部分以外も自動的に回復します。\n火事場力発動中は一定値以上の体力が必要です。\nまた、スタミナの回復速度が上昇します。", "풍운은 가희가 지닌 매력.\n때마다 다른 운치를 자아낸다.", "Automatically recovers health and increases stamina recovery speed."},
	{8, 3, "斬刃の祈珠", "참인의 기주", "Cutting Prayer Bead", "斬属性の武器で攻撃した時、\nダメージがより通りやすくなります。\nただし、属性ダメージは変化しません。", "참인은 모든 것을 베어 가르는 힘.\n모을수록 목소리가 맑아진다.", "Increases cutting damage, excluding elemental damage."},
	{9, 3, "打明の祈珠", "타명의 기주", "Blunt Prayer Bead", "打属性の武器で攻撃した時、\nダメージがより通りやすくなります。\nただし、属性ダメージは変化しません。", "타명은 뜨거운 힘.\n듣는 이에게 활력을 준다.", "Increases blunt damage, excluding elemental damage."},
	{10, 3, "弾起の祈珠", "탄기의 기주", "Shot Prayer Bead", "弾属性の武器で攻撃した時、\nダメージがより通りやすくなります。\nただし、属性ダメージは変化しません。", "탄기는 아득한 기억.\n듣는 이에게 더 큰 힘을 준다.", "Increases shot damage, excluding elemental damage."},
	{11, 3, "変続の祈珠", "변속의 기주", "Continuance Prayer Bead", "モンスターが状態異常にかかった際の\n持続時間が長くなります。", "변속은 영원히 남는 말.\n듣는 이에게 이어 갈 힘을 준다.", "Extends monster status ailment durations."},
	{14, 3, "万雷の祈珠", "만뢰의 기주", "Thunder Prayer Bead", "モンスターへの属性ダメージが増加します。", "만뢰는 가희에게 모인 이들의 뜻.\n가희의 목소리를 널리 전한다.", "Increases elemental damage dealt to monsters."},
	{15, 1, "不動の祈珠", "부동의 기주", "Immovable Prayer Bead", "モンスターが戦闘状態の間、そのモンスターは\n別のエリアに移動しなくなります。\n瀕死状態や特定のモンスターには無効です。", "부동은 짓누르는 힘.\n듣는 이에게 압도할 힘을 준다.", "Prevents area changes during combat; some monsters and weakened monsters are excluded."},
	{17, 3, "結集の祈珠", "결집의 기주", "Unity Prayer Bead", "クエストに参加しているプレイヤーキャラクター\nの人数に応じて、攻撃力が上昇します。", "결집은 굳건한 신뢰.\n모을수록 헌터들의 마음이 강해진다.", "Increases attack based on the number of player hunters, excluding NPCs."},
	{18, 3, "歌護の祈珠", "가호의 기주", "Protection Prayer Bead", "様々な保護系の効果が発動します。", "가호는 가희의 보살핌.\n모을수록 헌터들의 버팀목이 된다.", "Grants protective effects according to the bead level."},
	{19, 3, "強撃の祈珠", "강격의 기주", "Critical Prayer Bead", "武器の会心率が上がります。", "강격은 힘찬 음색.\n듣는 이의 힘을 벼린다.", "Increases weapon affinity."},
	{20, 3, "封火の祈珠", "봉화의 기주", "Fire Ward Prayer Bead", "火属性への耐性が上がります。", "봉화는 불의 봉인.\n듣는 이에게 내성을 준다.", "Increases fire resistance."},
	{21, 3, "封水の祈珠", "봉수의 기주", "Water Ward Prayer Bead", "水属性への耐性が上がります。", "봉수는 물의 봉인.\n듣는 이에게 내성을 준다.", "Increases water resistance."},
	{22, 3, "封氷の祈珠", "봉빙의 기주", "Ice Ward Prayer Bead", "氷属性への耐性が上がります。", "봉빙은 얼음의 봉인.\n듣는 이에게 내성을 준다.", "Increases ice resistance."},
	{23, 3, "封雷の祈珠", "봉뢰의 기주", "Thunder Ward Prayer Bead", "雷属性への耐性が上がります。", "봉뢰는 번개의 봉인.\n듣는 이에게 내성을 준다.", "Increases thunder resistance."},
	{24, 3, "封龍の祈珠", "봉룡의 기주", "Dragon Ward Prayer Bead", "龍属性への耐性が上がります。", "봉룡은 용의 봉인.\n듣는 이에게 내성을 준다.", "Increases dragon resistance."},
	{25, 2, "封属の祈珠", "봉속의 기주", "Elemental Ward Prayer Bead", "すべての属性への耐性が上がります。", "봉속은 다섯 속성의 봉인.\n듣는 이에게 내성을 준다.", "Increases all elemental resistances."},
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
			// The client draws the text before the first newline as a reading.
			name, desc = b.KO, "\n"+b.DescriptionKO
		}
		result = append(result, Bead{b.ID, name, desc})
	}
	return result
}
