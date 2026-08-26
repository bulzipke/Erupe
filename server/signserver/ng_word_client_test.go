package signserver

import (
	"testing"

	"erupe-ce/common/stringsupport"
)

func TestNormalizeClientNGWordPartsKeepsPatchedCP949Pairs(t *testing.T) {
	groups := []smcGroup{
		{charGroup: [][]rune{{'す'}, {'ス'}, {'ｽ'}}},
		{charGroup: [][]rune{{'て'}, {'テ'}, {'ﾃ'}}},
		{charGroup: [][]rune{{'け'}, {'ケ'}, {'ｹ'}}},
	}

	// CP949 "시발" is BD C3 B9 DF. Vorbis patches the name validator's
	// lead range, so the client sees two CP949 tokens instead of four
	// half-width-kana-like Shift-JIS bytes.
	got := normalizeClientNGWordParts(stringsupport.ToNGWordCP949("시발"), groups)
	if len(got) != 2 {
		t.Fatalf("normalized part count = %d, want 2", len(got))
	}
	wantValues := []uint16{0xC3BD, 0xDFB9}
	for i, want := range wantValues {
		if got[i].value != want || got[i].smcIndex != -1 {
			t.Errorf("part %d = (%04X,%d), want (%04X,-1)",
				i, got[i].value, got[i].smcIndex, want)
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
