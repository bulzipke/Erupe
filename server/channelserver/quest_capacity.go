package channelserver

import "fmt"

const questEnumerationContractVersion = 1

func (s *Session) eventQuestCapacity() (int, error) {
	if s.server == nil || s.server.erupeConfig == nil {
		return 0, fmt.Errorf("event quest capacity requires server configuration")
	}
	return s.server.erupeConfig.EffectiveEventQuestCapacity()
}
