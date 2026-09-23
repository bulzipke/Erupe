package config

import "fmt"

const (
	DivaMapRedTreasureOff       = "off"
	DivaMapRedTreasureRandomOne = "random-one"
	DivaMapRedTreasureAll       = "all"
)

func ValidateDivaMapRedTreasureMode(mode string) error {
	switch mode {
	case DivaMapRedTreasureOff, DivaMapRedTreasureRandomOne, DivaMapRedTreasureAll:
		return nil
	default:
		return fmt.Errorf("GameplayOptions.DivaMapRedTreasureMode must be off, random-one, or all")
	}
}
