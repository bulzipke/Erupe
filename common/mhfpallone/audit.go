package mhfpallone

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// QuestFiles lists present variants only. Their presence does not establish
// that their contents are valid or that a client's exact variant can load.
type QuestFiles struct {
	QuestID uint16   `json:"quest_id"`
	Files   []string `json:"files"`
}

type AssetAudit struct {
	RouteCount      int          `json:"route_count"`
	QuestCount      int          `json:"unique_quest_count"`
	MissingQuestIDs []uint16     `json:"missing_quest_ids"`
	Routes          []Route      `json:"routes"`
	Quests          []QuestFiles `json:"quests"`
}

// AuditAssets inventories the exact route IDs against Erupe's
// existing .bin/.json delivery path without modifying client or server data.
func AuditAssets(routes []Route, binPath string) (AssetAudit, error) {
	entries, err := os.ReadDir(filepath.Join(binPath, "quests"))
	if err != nil {
		return AssetAudit{}, fmt.Errorf("pallone: read quest directory: %w", err)
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			present[entry.Name()] = true
		}
	}
	ids := make(map[uint16]bool)
	for _, route := range routes {
		for _, id := range route.StageQuestIDs {
			ids[id] = true
		}
	}
	ordered := make([]int, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, int(id))
	}
	sort.Ints(ordered)
	audit := AssetAudit{
		RouteCount: len(routes), QuestCount: len(ordered),
		Routes: routes, MissingQuestIDs: []uint16{}, Quests: make([]QuestFiles, 0, len(ordered)),
	}
	for _, id := range ordered {
		quest := QuestFiles{QuestID: uint16(id), Files: []string{}}
		for _, suffix := range []string{"d0", "n0", "d1", "n1", "d2", "n2"} {
			for _, ext := range []string{".bin", ".json"} {
				filename := fmt.Sprintf("%05d%s%s", id, suffix, ext)
				if present[filename] {
					quest.Files = append(quest.Files, filename)
				}
			}
		}
		if len(quest.Files) == 0 {
			audit.MissingQuestIDs = append(audit.MissingQuestIDs, quest.QuestID)
		}
		audit.Quests = append(audit.Quests, quest)
	}
	return audit, nil
}
