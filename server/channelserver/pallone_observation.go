package channelserver

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

// Observations help correlate the first native maintenance run. They never
// authorize completion, create a receipt or credit points. Existing QuestTools
// controls them, so normal operation gains no extra logs or raw payload dumps.
func (s *Session) palloneObservationEnabled() bool {
	return s.server != nil && s.server.erupeConfig != nil && s.logger != nil &&
		s.server.erupeConfig.RealClientMode == cfg.ZZ && s.server.erupeConfig.DebugOptions.QuestTools
}

func (s *Session) observePallone(phase string, fields ...zap.Field) {
	if !s.palloneObservationEnabled() {
		return
	}
	base := []zap.Field{zap.String("phase", phase), zap.Uint32("charID", s.charID)}
	s.logger.Debug("PalloneObservation", append(base, fields...)...)
}

func pallone001Stage(id uint16) bool {
	return id == 27438 || id == 27479 || id == 27348 || id == 27365
}

func palloneQuestFilename(name string) (uint16, bool) {
	if len(name) != 7 || (name[5] != 'd' && name[5] != 'n') || name[6] < '0' || name[6] > '2' {
		return 0, false
	}
	id := uint32(0)
	for i := 0; i < 5; i++ {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
		id = id*10 + uint32(name[i]-'0')
	}
	if id > 65535 || !pallone001Stage(uint16(id)) {
		return 0, false
	}
	return uint16(id), true
}

func (s *Session) observePalloneFileReply(filename string, ack uint32, data []byte) {
	id, ok := palloneQuestFilename(filename)
	if !ok || !s.palloneObservationEnabled() {
		return
	}
	s.observePallone("file_reply_queued", zap.Uint32("ackHandle", ack), zap.Uint16("questID", id),
		zap.String("filename", filename), zap.Int("bytes", len(data)), zap.String("sha256", fmt.Sprintf("%x", sha256.Sum256(data))))
}

func (s *Session) observePalloneStage(phase string, questID uint16, stageID string) {
	if pallone001Stage(questID) {
		s.observePallone(phase, zap.Uint16("questID", questID), zap.String("stageID", stageID))
	}
}

func (s *Session) observePalloneRecord(data []byte) {
	if !s.palloneObservationEnabled() || len(data) < questElapsedFramesOffset+4 {
		return
	}
	id := binary.LittleEndian.Uint16(data[questIDOffset:])
	if !pallone001Stage(id) {
		return
	}
	s.observePallone("quest_result_reported", zap.Uint16("questID", id),
		zap.Uint8("resultCode", data[questResultCodeOffset]),
		zap.Uint32("elapsedFrames", binary.LittleEndian.Uint32(data[questElapsedFramesOffset:])),
		zap.Int("payloadBytes", len(data)))
}
