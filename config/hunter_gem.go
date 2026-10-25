package config

import (
	"fmt"
	"math"
)

// ValidateHunterGemPointMultiplier bounds the percent signal consumed by the
// paired ZZ client. Older clients retain their native behavior at the default.
func ValidateHunterGemPointMultiplier(multiplier float32, mode Mode) error {
	if math.IsNaN(float64(multiplier)) || math.IsInf(float64(multiplier), 0) || multiplier < 0 || multiplier > 100 {
		return fmt.Errorf("GameplayOptions.HunterGemPointMultiplier must be finite and between 0 and 100")
	}
	if mode != ZZ && multiplier != 1 {
		return fmt.Errorf("GameplayOptions.HunterGemPointMultiplier requires the ZZ client with matching Vorbis when different from 1")
	}
	return nil
}
