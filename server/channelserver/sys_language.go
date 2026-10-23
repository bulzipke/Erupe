package channelserver

// Bead holds the display strings for a single kiju prayer bead type.
type Bead struct {
	ID          int
	Name        string
	Description string
}

type guildMailTemplate struct {
	title string
	body  string // %s is the guild name.
}

type i18n struct {
	language string
	beads    []Bead
	cafe     struct {
		reset string
	}
	timer    string
	shutdown string
	chat     struct {
		ngWordRejected string
	}
	commands struct {
		descriptions map[string]string
		noOp         string
		disabled     string
		reload       string
		playtime     string
		kqf          struct {
			get string
			set struct {
				error   string
				success string
			}
			version string
		}
		rights struct {
			error   string
			success string
		}
		course struct {
			error    string
			disabled string
			enabled  string
			locked   string
		}
		teleport struct {
			error   string
			success string
		}
		psn struct {
			error   string
			success string
			exists  string
		}
		discord struct {
			success string
		}
		ban struct {
			success string
			noUser  string
			invalid string
			error   string
			length  string
		}
		timer struct {
			enabled  string
			disabled string
		}
		lang struct {
			usage   string
			invalid string
			success string
			current string
		}
		ravi struct {
			noCommand string
			start     struct {
				success string
				error   string
			}
			multiplier string
			res        struct {
				success string
				error   string
			}
			sed struct {
				success string
			}
			request   string
			error     string
			noPlayers string
			version   string
		}
	}
	raviente struct {
		berserk        string
		extreme        string
		extremeLimited string
		berserkSmall   string
	}
	guild struct {
		accepted        guildMailTemplate
		rejected        guildMailTemplate
		kicked          guildMailTemplate
		withdrawal      guildMailTemplate
		rookieGuildName string
		returnGuildName string
		invite          struct {
			title   string
			body    string
			success struct {
				title string
				body  string
			}
			accepted struct {
				title string
				body  string
			}
			rejected struct {
				title string
				body  string
			}
			declined struct {
				title string
				body  string
			}
		}
	}
}

// commandDescription keeps configured command descriptions as the fallback,
// while translating the built-in help entries for the session language.
func (i *i18n) commandDescription(fallback string) string {
	if description := i.commands.descriptions[fallback]; description != "" {
		return description
	}
	return fallback
}

// characterI18n resolves mail text for its recipient, including offline users.
// A missing preference or a failed lookup retains the server default.
func (s *Server) characterI18n(charID uint32) i18n {
	if s.Registry != nil {
		if recipient := s.Registry.FindSessionByCharID(charID); recipient != nil {
			return *recipient.I18n()
		}
	}
	if s.charRepo != nil && s.userRepo != nil {
		if userID, err := s.charRepo.GetUserID(charID); err == nil {
			if language, err := s.userRepo.GetLanguage(userID); err == nil && isSupportedLang(language) {
				return getLangStringsFor(language)
			}
		}
	}
	return getLangStrings(s)
}

// beadName returns the localised name for a bead type.
func (i *i18n) beadName(beadType int) string {
	for _, b := range i.beads {
		if b.ID == beadType {
			return b.Name
		}
	}
	return ""
}

// beadDescription returns the localised description for a bead type.
func (i *i18n) beadDescription(beadType int) string {
	for _, b := range i.beads {
		if b.ID == beadType {
			return b.Description
		}
	}
	return ""
}

// supportedLangs lists the language codes the server can serve. Kept in one
// place so the !lang command validator and future API handlers stay in sync
// with getLangStringsFor.
var supportedLangs = []string{"en", "jp", "fr", "es", "zh", "ko"}

// isSupportedLang reports whether the given code is one the server can serve.
func isSupportedLang(code string) bool {
	for _, l := range supportedLangs {
		if l == code {
			return true
		}
	}
	return false
}

// getLangStringsFor returns the i18n string table for the given language code,
// falling back to English for unknown or empty codes. This is the primitive
// callers should use when they have a concrete language (e.g. a per-session
// preference from the database); callers that only want the server default
// should use getLangStrings.
func getLangStringsFor(lang string) i18n {
	switch lang {
	case "jp":
		return langJapanese()
	case "fr":
		return langFrench()
	case "es":
		return langSpanish()
	case "zh":
		return langChinese()
	case "en":
		return langEnglish()
	case "ko":
		return langKorean()
	default:
		return langEnglish()
	}
}

// getLangStrings returns the i18n string table for the server's globally
// configured language. Per-session localization should resolve the language
// first and call getLangStringsFor directly.
func getLangStrings(s *Server) i18n {
	return getLangStringsFor(s.erupeConfig.Language)
}
