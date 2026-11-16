package entranceserver

import (
	"bytes"
	"errors"
	"testing"
)

const (
	iniKoreanPhrase  = "\xc7\xcf\xb4\xc3\xc0\xcc \xb8\xbc\xbe\xc6\xbf\xe4" // 하늘이 맑아요
	iniKoreanName    = "\xc4\xa3\xb1\xb8 \xc0\xcc\xb8\xa7"                 // 친구 이름
	iniDamagedPhrase = "\u03f4       \u01bf"
	iniDamagedName   = "\u0123    \u0338"
)

func TestIniUploadNormalization(t *testing.T) {
	ini := func(phrase, name string) []byte {
		return []byte("[VIDEO]\r\nFPS_60=1\r\n[fixed_sentence]\r\nCwords_00=" + phrase +
			"\r\n[chatphi_123456]\r\n phinf_name_0 = " + name + " \r\n")
	}
	korean := ini(iniKoreanPhrase, iniKoreanName)
	koreanUTF8 := ini("하늘이 맑아요", "친구 이름")
	damaged := ini(iniDamagedPhrase, iniDamagedName)
	japanese := ini("\x82\xb1\x82\xf1\x82\xc9\x82\xbf\x82\xcd", "\x82\xa0\x82\xa2")
	japaneseUTF8 := ini("こんにちは", "あい")
	halfwidthKana := ini("\xb6", "")
	ambiguousNative := ini("\xc3\xa9", "")
	cleared := ini("", "")
	withBOM := func(data []byte) []byte { return append(bytes.Clone(iniUTF8BOM), data...) }
	for _, tc := range []struct {
		name                     string
		previous, incoming, want []byte // nil want means reject
	}{
		{"invalid UTF-8 replaced with spaces", korean, damaged, nil},
		{"damaged with BOM", korean, withBOM(damaged), nil},
		{"invalid UTF-8 replaced with U+FFFD", korean,
			ini("\ufffd\u03f4\ufffd\ufffd\ufffd \ufffd\ufffd\ufffd\u01bf\ufffd", "\u0123\ufffd\ufffd \ufffd\u0338\ufffd"), nil},
		{"CP949 saved as UTF-8", korean, koreanUTF8, korean},
		{"CP932 saved as UTF-8", japanese, japaneseUTF8, japanese},
		{"CP949 on an otherwise ambiguous first upload with BOM", nil, withBOM(ini("한글", "")), ini("\xc7\xd1\xb1\xdb", "")},
		{"CP949 on unambiguous first upload without BOM", nil, koreanUTF8, korean},
		{"CP932 fallback from CP949", korean, withBOM(ini("ｶ", "")), halfwidthKana},
		{"CP949 fallback from CP932", japanese, koreanUTF8, korean},
		{"unsupported even with no previous copy", nil, withBOM(ini("😀", "")), nil},
		{"unsupported without BOM", nil, ini("😀", ""), nil},
		{"malformed UTF-8 with BOM", nil, withBOM(korean), nil},
		{"mixed UTF-8 and native values", korean, append(bytes.Clone(koreanUTF8), []byte("[OTHER]\r\nVALUE="+iniKoreanPhrase+"\r\n")...), nil},
		{"plain ASCII and BOM", nil, withBOM(cleared), cleared},
		{"unchanged native Korean", korean, korean, korean},
		{"unchanged native Japanese", japanese, japanese, japanese},
		{"preserve ambiguous BOM-less bytes", nil, ambiguousNative, ambiguousNative},
		{"cleared values", korean, cleared, cleared},
		{"repair", damaged, korean, korean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeIniUpload(tc.incoming, tc.previous, possibleIniReencoding(tc.incoming))
			if tc.want == nil {
				if !errors.Is(err, errIniEncoding) {
					t.Fatalf("expected an encoding rejection, got %v", err)
				}
				return
			}
			if err != nil || !bytes.Equal(got, tc.want) {
				t.Fatalf("normalized INI did not match expected bytes: %v", err)
			}
		})
	}
}

func TestIniDamageRequiresDistinctMatches(t *testing.T) {
	previous := []byte("[FIXED_SENTENCE]\nCWORDS_00=" + iniKoreanPhrase + "\nCWORDS_01=" + iniKoreanPhrase +
		"\n[CHATPHI_123456]\nPHINF_NAME_0=" + iniKoreanName)
	for _, incoming := range []string{
		"[FIXED_SENTENCE]\nCWORDS_00=" + iniDamagedPhrase,
		"[FIXED_SENTENCE]\nCWORDS_00=" + iniDamagedPhrase + "\nCWORDS_01=" + iniDamagedPhrase,
		"[FIXED_SENTENCE]\nCWORDS_00=" + iniDamagedPhrase + "\n[CHATPHI_123456]\nPHINF_NAME_0=" + iniKoreanName + "\nPHINF_NAME_0=" + iniDamagedName,
		"[FIXED_SENTENCE]\nCWORDS_00=" + iniDamagedPhrase + "\n[SCREEN]\nPHINF_NAME_0=" + iniDamagedName,
	} {
		if iniTextMatches(iniTextValues(previous), possibleIniReencoding([]byte(incoming)), matchesDamagedUTF8) {
			t.Fatal("encoding damage requires two distinct native values in their active fields")
		}
	}
}
