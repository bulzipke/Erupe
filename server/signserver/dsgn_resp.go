package signserver

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/common/gametime"
	ps "erupe-ce/common/pascalstring"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (s *Session) makeSignResponse(uid uint32) []byte {
	// Get the characters from the DB.
	chars, err := s.server.getCharactersForUser(uid)
	if len(chars) == 0 && uid != 0 {
		err = s.server.newUserChara(uid)
		if err == nil {
			chars, err = s.server.getCharactersForUser(uid)
		}
	}
	if err != nil {
		s.logger.Warn("Error getting characters from DB", zap.Error(err))
	}

	bf := byteframe.NewByteFrame()
	var tokenID uint32
	var sessToken string
	if uid == 0 && s.psn != "" {
		tokenID, sessToken, err = s.server.registerPsnToken(s.psn)
	} else {
		tokenID, sessToken, err = s.server.registerUidToken(uid)
	}
	if err != nil {
		bf.WriteUint8(uint8(SIGN_EABORT))
		return bf.Data()
	}

	if s.client == PS3 && (s.server.erupeConfig.PatchServerFile == "" || s.server.erupeConfig.PatchServerManifest == "") {
		bf.WriteUint8(uint8(SIGN_EABORT))
		return bf.Data()
	}

	bf.WriteUint8(uint8(SIGN_SUCCESS))
	bf.WriteUint8(2) // patch server count
	bf.WriteUint8(1) // entrance server count
	bf.WriteUint8(uint8(len(chars)))
	bf.WriteUint32(tokenID)
	bf.WriteBytes([]byte(sessToken))
	bf.WriteUint32(uint32(gametime.Adjusted().Unix()))
	if s.client == PS3 {
		ps.Uint8(bf, fmt.Sprintf("%s/ps3", s.server.erupeConfig.PatchServerManifest), false)
		ps.Uint8(bf, fmt.Sprintf("%s/ps3", s.server.erupeConfig.PatchServerFile), false)
	} else {
		ps.Uint8(bf, s.server.erupeConfig.PatchServerManifest, false)
		ps.Uint8(bf, s.server.erupeConfig.PatchServerFile, false)
	}
	if strings.Split(s.rawConn.RemoteAddr().String(), ":")[0] == "127.0.0.1" {
		ps.Uint8(bf, fmt.Sprintf("127.0.0.1:%d", s.server.erupeConfig.Entrance.Port), false)
	} else {
		ps.Uint8(bf, fmt.Sprintf("%s:%d", s.server.erupeConfig.Host, s.server.erupeConfig.Entrance.Port), false)
	}

	lastPlayed := uint32(0)
	for _, char := range chars {
		if lastPlayed == 0 {
			lastPlayed = char.ID
		}
		bf.WriteUint32(char.ID)
		if s.server.erupeConfig.DebugOptions.MaxLauncherHR {
			bf.WriteUint16(999)
		} else {
			bf.WriteUint16(char.HR)
		}
		bf.WriteUint16(char.WeaponType)                                          // Weapon, 0-13.
		bf.WriteUint32(char.LastLogin)                                           // Last login date, unix timestamp in seconds.
		bf.WriteBool(char.IsFemale)                                              // Sex, 0=male, 1=female.
		bf.WriteBool(char.IsNewCharacter)                                        // Is new character, 1 replaces character name with ?????.
		bf.WriteUint8(0)                                                         // Old GR
		bf.WriteBool(true)                                                       // Use uint16 GR, no reason not to
		bf.WriteBytes(stringsupport.PaddedString(char.Name, 16, true))           // Character name
		bf.WriteBytes(stringsupport.PaddedString(char.UnkDescString, 32, false)) // unk str
		if s.server.erupeConfig.RealClientMode >= cfg.G7 {
			bf.WriteUint16(char.GR)
			bf.WriteUint8(0) // Unk
			bf.WriteUint8(0) // Unk
		}
	}

	friends := s.server.getFriendsForCharacters(chars)
	if len(friends) == 0 {
		bf.WriteUint8(0)
	} else {
		if len(friends) > 255 {
			bf.WriteUint8(255)
			bf.WriteUint16(uint16(len(friends)))
		} else {
			bf.WriteUint8(uint8(len(friends)))
		}
		for _, friend := range friends {
			bf.WriteUint32(friend.CID)
			bf.WriteUint32(friend.ID)
			ps.Uint8(bf, friend.Name, true)
		}
	}

	guildmates := s.server.getGuildmatesForCharacters(chars)
	if len(guildmates) == 0 {
		bf.WriteUint8(0)
	} else {
		if len(guildmates) > 255 {
			bf.WriteUint8(255)
			bf.WriteUint16(uint16(len(guildmates)))
		} else {
			bf.WriteUint8(uint8(len(guildmates)))
		}
		for _, guildmate := range guildmates {
			bf.WriteUint32(guildmate.CID)
			bf.WriteUint32(guildmate.ID)
			ps.Uint8(bf, guildmate.Name, true)
		}
	}

	if s.server.erupeConfig.HideLoginNotice {
		bf.WriteBool(false)
	} else {
		bf.WriteBool(true)
		bf.WriteUint8(0)
		bf.WriteUint8(0)
		ps.Uint16(bf, strings.Join(s.server.erupeConfig.LoginNotices[:], "<PAGE>"), true)
	}

	bf.WriteUint32(s.server.getLastCID(uid))
	bf.WriteUint32(s.server.getUserRights(uid))

	filters, selectedWords := BuildClientNGWordFilter(s.server.ngWords)
	if selectedWords < len(s.server.ngWords) {
		s.server.ngWordWarnOnce.Do(func() {
			s.logger.Warn("Some NG-word CSV entries exceeded the client buffer or per-entry limit and were omitted",
				zap.Int("loaded", len(s.server.ngWords)), zap.Int("selected", selectedWords))
		})
	}

	if len(filters) > maxNGWordFilterPayloadBytes {
		// selectNGWordParts accounts for every variable and fixed table byte;
		// retain this guard so a future protocol-layout edit cannot wrap uint16.
		s.logger.Error("NG-word filter payload exceeded protocol limit",
			zap.Int("bytes", len(filters)), zap.Int("max", maxNGWordFilterPayloadBytes))
		bf.WriteUint16(0)
	} else {
		bf.WriteUint16(uint16(len(filters)))
		bf.WriteBytes(filters)
	}

	if s.client == VITA || s.client == PS3 || s.client == PS4 {
		psnUser, err := s.server.userRepo.GetPSNIDForUser(uid)
		if err != nil {
			s.logger.Warn("Failed to get PSN ID for user", zap.Uint32("uid", uid), zap.Error(err))
		}
		bf.WriteBytes(stringsupport.PaddedString(psnUser, 20, true))
	}

	// CapLink.Values requires at least 5 elements to avoid index out of range panics
	// Provide safe defaults if array is too small
	capLinkValues := s.server.erupeConfig.DebugOptions.CapLink.Values
	if len(capLinkValues) < 5 {
		capLinkValues = []uint16{0, 0, 0, 0, 0}
	}

	bf.WriteUint16(capLinkValues[0])
	if capLinkValues[0] == 51728 {
		bf.WriteUint16(capLinkValues[1])
		if capLinkValues[1] == 20000 || capLinkValues[1] == 20002 {
			ps.Uint16(bf, s.server.erupeConfig.DebugOptions.CapLink.Key, false)
		}
	}

	caStruct := []struct {
		Unk0 uint8
		Unk1 uint32
		Unk2 string
	}{}
	bf.WriteUint8(uint8(len(caStruct)))
	for i := range caStruct {
		bf.WriteUint8(caStruct[i].Unk0)
		bf.WriteUint32(caStruct[i].Unk1)
		ps.Uint8(bf, caStruct[i].Unk2, false)
	}
	bf.WriteUint16(capLinkValues[2])
	bf.WriteUint16(capLinkValues[3])
	bf.WriteUint16(capLinkValues[4])
	if capLinkValues[2] == 51729 && capLinkValues[3] == 1 && capLinkValues[4] == 20000 {
		ps.Uint16(bf, fmt.Sprintf(`%s:%d`, s.server.erupeConfig.DebugOptions.CapLink.Host, s.server.erupeConfig.DebugOptions.CapLink.Port), false)
	}

	// The zero time means the account has no returning-player status. Its Unix()
	// is negative, so convert it explicitly instead of letting the uint32 cast
	// wrap it into a far-future timestamp that would unlock the Return world.
	var returnExpiryTS uint32
	if returnExpiry := s.server.getReturnExpiry(uid); !returnExpiry.IsZero() {
		if unix := returnExpiry.Unix(); unix > 0 {
			returnExpiryTS = uint32(unix)
		}
	}
	bf.WriteUint32(returnExpiryTS)
	bf.WriteUint32(0)

	tickets := []uint32{
		s.server.erupeConfig.GameplayOptions.MezFesSoloTickets,
		s.server.erupeConfig.GameplayOptions.MezFesGroupTickets,
	}
	stalls := []uint8{
		10, 3, 6, 9, 4, 8, 5, 7,
	}
	if s.server.erupeConfig.GameplayOptions.MezFesSwitchMinigame {
		stalls[4] = 2
	}

	// We can just use the start timestamp as the event ID
	bf.WriteUint32(uint32(gametime.WeekStart().Unix()))
	// Start time
	bf.WriteUint32(uint32(gametime.WeekNext().Add(-time.Duration(s.server.erupeConfig.GameplayOptions.MezFesDuration) * time.Second).Unix()))
	// End time
	bf.WriteUint32(uint32(gametime.WeekNext().Unix()))
	bf.WriteUint8(uint8(len(tickets)))
	for i := range tickets {
		bf.WriteUint32(tickets[i])
	}
	bf.WriteUint8(uint8(len(stalls)))
	for i := range stalls {
		bf.WriteUint8(stalls[i])
	}
	return bf.Data()
}

func firstNGWord(parts []uint16) uint16 {
	if len(parts) == 0 {
		return 0
	}
	return parts[0]
}
