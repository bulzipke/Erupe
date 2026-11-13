package channelserver

import "time"

const (
	festaGoalDivisor    = 3
	festaFPPercent      = 200
	festaItemMultiplier = 2
	festaMonopolyCount  = 2 // trials per team during the published windows
)

var festaJST = time.FixedZone("JST", 9*60*60)

// Keep the Japanese goal values in storage and round the reduced cut up.
func festaSoulGoal(original uint32) uint32 {
	return uint32((uint64(original) + festaGoalDivisor - 1) / festaGoalDivisor)
}

// The high bit distinguishes the bonus from the old reserved value (1).
const festaFPBonusPresent uint32 = 1 << 31

func festaPersonalFPBonus(souls, maxFP uint32) uint32 {
	total := uint64(max(0, int64(min(souls, 499))-99))*300 +
		uint64(max(0, int64(min(souls, 1000))-499))*750 +
		uint64(max(0, int64(souls)-1000))*1000
	return uint32(min(total, uint64(maxFP), uint64(festaFPBonusPresent-1)))
}

type festaSecretQuest struct {
	hr, gr uint32
	bonus  uint16
}

// The eight pairs and bonus amounts are from the Japanese 146th festival.
// Bonus excludes the base hunt souls, which the client adds independently.
// https://wikiwiki.jp/mhfsecret/第146回狩人祭
var festaSecrets = [...]festaSecretQuest{
	{26229, 23369, 94}, // Hypnocatrice (breeding season)
	{26460, 23311, 91}, // Kamu Orugaron
	{21709, 23490, 88}, // Diorex
	{26253, 23480, 90}, // Espinas
	{26022, 23238, 91}, // Lavasioth
	{21929, 23380, 88}, // Gasurabazura
	{21914, 55929, 88}, // Harudomerugu
	{26675, 0, 94},     // Sunday morning: HR Gogomoa
}

// Start times are fixed in JST. A slot's trailing team is fixed by the
// submission totals two hours earlier, even if the live lead changes.
func festaSecretWindow(now time.Time) (cutoff time.Time, slot int, both bool) {
	local := now.In(festaJST)
	hour := local.Hour()
	if (hour < 6 || hour >= 8) && (hour < 14 || hour >= 16) && (hour < 22) {
		return time.Time{}, 0, false
	}
	startHour := (hour-6)/8*8 + 6
	start := time.Date(local.Year(), local.Month(), local.Day(), startHour, 0, 0, 0, festaJST)
	day := (int(local.Weekday()) + 6) % 7
	slot = day*3 + (startHour-6)/8
	both = (local.Weekday() == time.Saturday && startHour >= 14) ||
		local.Weekday() == time.Sunday || (local.Weekday() == time.Monday && startHour == 6)
	return start.Add(-2 * time.Hour), slot, both
}

func festaSecretTrials(round uint32, slot int, both, tied bool) []FestaTrial {
	index := (slot + int(round%7)) % 7
	if slot == 18 { // Sunday at 06:00.
		index = 7
	}
	quest := festaSecrets[index]
	teamRule := uint16(0) // Native objective 5: trailing team only.
	if both || tied {
		teamRule = 2
	}
	trials := make([]FestaTrial, 0, 4)
	for _, id := range [...]uint32{quest.hr, quest.gr} {
		if id == 0 {
			continue
		}
		trials = append(trials, FestaTrial{ID: 0x10000 + id, Objective: 5, GoalID: id,
			Reward: quest.bonus, Monopoly: FestivalColorNone, Unk: teamRule})
		if both && !tied {
			// Native rule 0 matches only the trailing team, so this stacks
			// with rule 2 and does not change the leading team's reward.
			trials = append(trials, FestaTrial{ID: 0x20000 + id, Objective: 5, GoalID: id,
				Reward: 20, Monopoly: FestivalColorNone})
		}
	}
	return trials
}

func festaMonopolyWindow(now time.Time) bool {
	local := now.In(festaJST)
	hour := local.Hour()
	switch local.Weekday() {
	case time.Friday, time.Saturday:
		return hour >= 12 && hour < 18
	case time.Sunday, time.Monday:
		return hour >= 18
	}
	return false
}

type festaMonopolyVote struct {
	TrialID uint32        `db:"trial_id"`
	Team    FestivalColor `db:"team"`
}

func festaMonopolies(votes []festaMonopolyVote, first FestivalColor) map[uint32]FestivalColor {
	picks := make(map[uint32]FestivalColor)
	teams := [...]FestivalColor{FestivalColorBlue, FestivalColorRed}
	if first == FestivalColorRed {
		teams[0], teams[1] = teams[1], teams[0]
	}
	for range festaMonopolyCount {
		for _, team := range teams {
			for _, vote := range votes {
				if vote.Team == team && picks[vote.TrialID] == "" {
					picks[vote.TrialID] = team
					break
				}
			}
		}
	}
	return picks
}

// Reward categories follow the native reward confirmation menu: winner
// ranked, winner regular, loser ranked, loser regular, draw.
func festaMainRewards() []FestaReward {
	rewards := make([]FestaReward, 0, 20)
	for i, quantities := range [...][3]uint16{
		{35, 100, 1000}, {25, 70, 500}, {20, 50, 300}, {5, 20, 100}, {25, 70, 500},
	} {
		result := uint8(i + 1)
		rewards = append(rewards,
			FestaReward{Unk0: result, ItemType: 7, Quantity: quantities[0] * festaItemMultiplier, ItemID: 1520},
			FestaReward{Unk0: result, ItemType: 7, Quantity: quantities[1] * festaItemMultiplier, ItemID: 7011, MinGR: 1},
			FestaReward{Unk0: result, ItemType: 12, Quantity: quantities[2]},
			FestaReward{Unk0: result, ItemType: 13},
		)
	}
	return rewards
}

func festaRewardResult(team FestivalColor, rank, blue, red uint32) uint32 {
	if team == FestivalColorNone || rank == 0 {
		return 0
	}
	if blue == red {
		return 5
	}
	winner := (team == FestivalColorBlue && blue > red) || (team == FestivalColorRed && red > blue)
	if winner {
		if rank <= 300 {
			return 1
		}
		return 2
	}
	if rank <= 300 {
		return 3
	}
	return 4
}
