package channelserver

import "fmt"

// This immutable value represents the inventory actually sent to this client.
// Reading a new DB blob for SAVEDATA must never advance this baseline.
type questLicenseClientInventory struct {
	characterID uint32
	baseline    QuestLicenseStarterEpochBaseline
}

func (s *Session) usesQuestLicenseStarterEpoch() bool {
	return s != nil && s.server != nil && s.server.erupeConfig != nil && s.server.erupeConfig.QuestLicenses.Enabled
}

func (s *Session) clientStarterEpochBaseline() (QuestLicenseStarterEpochBaseline, error) {
	loaded := s.questLicenseClientInventory.Load()
	if loaded == nil || loaded.characterID != s.charID || loaded.baseline.UserID != s.userID || s.userID == 0 || loaded.baseline.Epoch < 0 {
		return QuestLicenseStarterEpochBaseline{}, fmt.Errorf("client inventory baseline is not initialized")
	}
	return loaded.baseline, nil
}
