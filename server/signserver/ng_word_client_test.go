package signserver

import (
	"testing"

	"erupe-ce/common/stringsupport"
)

func TestNormalizeClientNGWordPartsAccountsForCP949SMCCollisions(t *testing.T) {
	groups := []smcGroup{
		{charGroup: [][]rune{{'す'}, {'ス'}, {'ｽ'}}},
		{charGroup: [][]rune{{'て'}, {'テ'}, {'ﾃ'}}},
		{charGroup: [][]rune{{'け'}, {'ケ'}, {'ｹ'}}},
	}

	// CP949 "시발" is BD C3 B9 DF. Under the client's Shift-JIS parser,
	// BD/C3/B9 collide with the half-width kana ｽ/ﾃ/ｹ in the SMC table.
	got := normalizeClientNGWordParts(stringsupport.ToNGWordCP949("시발"), groups)
	if len(got) != 4 {
		t.Fatalf("normalized part count = %d, want 4", len(got))
	}
	wantIndexes := []int16{0, 4, 8, -1}
	for i, want := range wantIndexes {
		if got[i].smcIndex != want {
			t.Errorf("part %d SMC index = %d, want %d", i, got[i].smcIndex, want)
		}
	}
}

func TestNormalizeClientNGWordPartsCollapsesTwoTokenSMCVariant(t *testing.T) {
	groups := []smcGroup{
		{charGroup: [][]rune{{'ず'}, {'ｽ', 'ﾞ'}}},
	}
	raw := stringsupport.ToNGWord("ｽﾞ")
	got := normalizeClientNGWordParts(raw, groups)
	if len(got) != 1 {
		t.Fatalf("normalized part count = %d, want 1", len(got))
	}
	if got[0].smcIndex != 0 {
		t.Fatalf("SMC index = %d, want 0", got[0].smcIndex)
	}
}
