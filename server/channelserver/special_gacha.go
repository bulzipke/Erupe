package channelserver

import (
	"errors"
	"fmt"
	"math"
)

const (
	specialGachaID = 65
	// ZZ reserves 128 six-byte records for the normal draw response.
	normalGachaResultLimit = 128
)

type specialGachaStore interface {
	commitSpecialGachaRoll(userID, charID uint32, costType uint8, cost uint16, items []GachaItem) error
}

func (svc *GachaService) playSpecialGacha(store specialGachaStore, userID, charID uint32, rollType uint8, entries []GachaEntry) (*GachaPlayResult, error) {
	if rollType > 2 {
		return nil, errors.New("unknown special gacha draw option")
	}
	costType, cost, rolls, err := svc.gachaRepo.GetEntryForTransaction(specialGachaID, rollType)
	if err != nil {
		return nil, err
	}
	if rolls != 1 && rolls != 11 {
		return nil, fmt.Errorf("invalid special gacha draw count %d", rolls)
	}
	drawn, err := getRandomEntries(entries, rolls, false)
	if err != nil {
		return nil, err
	}
	var rewards []GachaReward
	var bundleWon bool
	for _, entry := range drawn {
		items, err := svc.gachaRepo.GetItemsForEntry(entry.ID)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("special gacha prize %d has no items", entry.ID)
		}
		bundleWon = bundleWon || len(items) > gachaDetailItemsPerEntry
		rewards = appendGachaRewards(rewards, items, entry.Rarity)
	}
	if bundleWon {
		// Repeated bundle wins add to the same item stacks. This keeps even
		// eleven bundle wins inside the result and the pending-box page.
		rewards = stackGachaMaterials(rewards)
	}
	if len(rewards) > normalGachaResultLimit {
		return nil, fmt.Errorf("special gacha result exceeds %d items", normalGachaResultLimit)
	}
	items := rewardsToItems(rewards)
	pending := items[:0]
	for _, item := range items {
		if item.ItemType != gachaItemTypeZenny {
			pending = append(pending, item)
		}
	}
	if err := store.commitSpecialGachaRoll(userID, charID, costType, cost, pending); err != nil {
		return nil, err
	}
	svc.recordSuccessfulPlay(specialGachaID, rolls)
	return &GachaPlayResult{Rewards: rewards}, nil
}

func stackGachaMaterials(rewards []GachaReward) []GachaReward {
	type material struct {
		id     uint16
		rarity uint8
	}
	stackAt := make(map[material]int)
	stacked := make([]GachaReward, 0, len(rewards))
	for _, reward := range rewards {
		if reward.ItemType == 7 {
			key := material{reward.ItemID, reward.Rarity}
			if idx, exists := stackAt[key]; exists && reward.Quantity <= math.MaxUint16-stacked[idx].Quantity {
				stacked[idx].Quantity += reward.Quantity
				continue
			}
			stackAt[key] = len(stacked)
		}
		stacked = append(stacked, reward)
	}
	return stacked
}

// The item currency is paid by ZZ from the character save. For the gold
// options the server commits the debit and all pending items together.
func (r *GachaRepository) commitSpecialGachaRoll(userID, charID uint32, costType uint8, cost uint16, items []GachaItem) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if costType == 19 {
		err := debitResult(tx.Exec(`UPDATE users SET
			gacha_trial = CASE WHEN COALESCE(gacha_trial,0) >= $2 THEN COALESCE(gacha_trial,0)-$2 ELSE COALESCE(gacha_trial,0) END,
			gacha_premium = CASE WHEN COALESCE(gacha_trial,0) >= $2 THEN COALESCE(gacha_premium,0) ELSE COALESCE(gacha_premium,0)-$2 END
			WHERE id=$1 AND (COALESCE(gacha_trial,0)>=$2 OR COALESCE(gacha_premium,0)>=$2)`, userID, cost))
		if err != nil {
			return fmt.Errorf("deduct special gacha coins: %w", err)
		}
	} else if costType != 7 {
		return fmt.Errorf("%w: type %d", errGachaUnsupportedCost, costType)
	}

	var data []byte
	if err := tx.Get(&data, `SELECT gacha_items FROM characters WHERE id=$1 AND user_id=$2 AND NOT deleted FOR UPDATE`, charID, userID); err != nil {
		return err
	}
	if len(data) > 0 {
		if _, _, _, valid := inspectGachaItemBlob(data); !valid {
			return errGachaItemsBlocked
		}
	}
	if len(items) > 0 {
		if _, err := tx.Exec(`UPDATE characters SET gacha_items=$1 WHERE id=$2`, prependGachaItems(data, items), charID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
