package channelserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cfg "erupe-ce/config"
)

// Validate against the same monster catalog actually sent to the client.
// File presence is a prerequisite, not proof of quest availability: event
// quests still need their normal delivery rules and are never auto-published.
func validateDivaBonusData(binPath string, rules []cfg.DivaBonusTarget) error {
	checkedQuests := make(map[int64]bool)
	var questFiles []os.DirEntry
	for i, rule := range rules {
		switch rule.TargetType {
		case "monster":
			found := false
			for _, monster := range divaSongMonsterPoints {
				if int64(monster.MID) == rule.TargetID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("Diva bonus entry %d: monster %d has no song-point catalog entry", i, rule.TargetID)
			}
		case "quest":
			if checkedQuests[rule.TargetID] {
				continue
			}
			if questFiles == nil {
				var err error
				questFiles, err = os.ReadDir(filepath.Join(binPath, "quests"))
				if err != nil {
					return fmt.Errorf("Diva bonus quest directory: %w", err)
				}
			}
			pattern := fmt.Sprintf("%05d[dn][0-9]", rule.TargetID)
			// Event enumeration loads the d0 variant specifically.
			if rule.TargetID >= 40000 {
				pattern = fmt.Sprintf("%05dd0", rule.TargetID)
			}
			found := false
			binaryShadowsJSON := make(map[string]bool)
			for _, ext := range []string{".bin", ".json"} {
				for _, file := range questFiles {
					matches, _ := filepath.Match(pattern+ext, file.Name())
					if !matches {
						continue
					}
					base := strings.TrimSuffix(file.Name(), ext)
					if ext == ".json" && binaryShadowsJSON[base] {
						continue
					}
					path := filepath.Join(binPath, "quests", file.Name())
					info, err := os.Stat(path) // Follow deployed quest-file symlinks.
					if err != nil || !info.Mode().IsRegular() {
						continue
					}
					f, err := os.Open(path)
					if err != nil {
						continue // The loader may fall back to JSON on an I/O failure.
					}
					_ = f.Close()
					if ext == ".bin" {
						// A readable empty BIN still wins over JSON in the loader.
						binaryShadowsJSON[base] = true
					}
					if info.Size() > 0 {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				return fmt.Errorf("Diva bonus entry %d: quest %d has no nonempty quest file (%s.bin/.json)", i, rule.TargetID, pattern)
			}
			checkedQuests[rule.TargetID] = true
		}
	}
	return nil
}
