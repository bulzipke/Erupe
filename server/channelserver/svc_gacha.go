package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"erupe-ce/common/byteframe"

	"go.uber.org/zap"
)

// GachaService encapsulates business logic for the gacha lottery system.
type GachaService struct {
	gachaRepo GachaRepo
	userRepo  UserRepo
	charRepo  CharacterRepo
	logger    *zap.Logger
}

// NewGachaService creates a new GachaService.
func NewGachaService(gr GachaRepo, ur UserRepo, cr CharacterRepo, log *zap.Logger) *GachaService {
	return &GachaService{
		gachaRepo: gr,
		userRepo:  ur,
		charRepo:  cr,
		logger:    log,
	}
}

// GachaReward represents a single gacha reward item with rarity.
type GachaReward struct {
	ItemType uint8
	ItemID   uint16
	Quantity uint16
	Rarity   uint8
}

// GachaPlayResult holds the outcome of a normal or box gacha play.
type GachaPlayResult struct {
	Rewards []GachaReward
}

// StepupPlayResult holds the outcome of a stepup gacha play.
type StepupPlayResult struct {
	RandomRewards     []GachaReward
	GuaranteedRewards []GachaReward
}

// StepupStatus holds the current stepup state for a character on a gacha.
type StepupStatus struct {
	Step uint8
}

// BoxDrawCount is how many balls of one box gacha reward entry a character
// has drawn since the box was last reset.
type BoxDrawCount struct {
	EntryID uint32 `db:"entry_id"`
	Count   int    `db:"count"`
}

const (
	// gachaItemTypeZenny rewards are credited by the client itself when it
	// shows the roll result (normal and box result screens), and the client's
	// pending-box list leaves them out. Stored in the pending box they could
	// never be received when they ended up after the last visible item.
	gachaItemTypeZenny = 10

	// A box reward entry holds weight balls. The client keeps each entry's
	// drawn count in one byte, so an entry holds 1..255 balls; a weight of 0
	// (the demo seed) counts as one ball.
	gachaBoxMaxBalls = 255
)

var (
	errGachaUnsupportedCost = errors.New("gacha roll cost is not a server-held currency")
	errGachaItemsBlocked    = errors.New("pending gacha items are unreadable")
	errGachaBoxShort        = errors.New("box gacha has fewer balls left than the roll draws")
	errGachaBoxOneTime      = errors.New("one-time box gacha cannot be reset")
	errGachaStepOrder       = errors.New("stepup gacha roll is not the character's current step")
)

// transact deducts the cost of a gacha roll. A roll whose cost the balance
// does not cover fails without deducting anything.
//
// The ZZ client pays some cost types out of the character save itself before
// it sends the roll (FUN_1046d200: 7 items, 10 zenny, 12/13, 14, 16), and the
// save carries that to the server, so nothing is deducted here. The
// server-held currencies (17 net cafe points, 19/20 gacha coins, 21 frontier
// points) are only mirrored by the client and are deducted here. Any other
// cost type is paid by nobody, so the roll is refused.
func (svc *GachaService) transact(userID, charID, gachaID uint32, rollID uint8) (int, error) {
	itemType, itemNumber, rolls, err := svc.gachaRepo.GetEntryForTransaction(gachaID, rollID)
	if err != nil {
		return 0, err
	}
	switch itemType {
	case 7, gachaItemTypeZenny, 12, 13, 14, 16:
		// Paid by the client from the character save.
	case 17:
		if _, err := svc.charRepo.SpendInt(charID, "netcafe_points", int(itemNumber)); err != nil {
			return 0, fmt.Errorf("deduct net cafe points: %w", err)
		}
	case 19, 20:
		if err := svc.spendGachaCoin(userID, itemNumber); err != nil {
			return 0, err
		}
	case 21:
		if err := svc.userRepo.DeductFrontierPoints(userID, uint32(itemNumber)); err != nil {
			return 0, fmt.Errorf("deduct frontier points: %w", err)
		}
	default:
		return 0, fmt.Errorf("%w: type %d", errGachaUnsupportedCost, itemType)
	}
	return rolls, nil
}

// checkPendingItems refuses a roll before its cost is paid when the pending
// gacha items blob is corrupt, because saveGachaItems would then keep the old
// blob and drop the new rewards. A failed read keeps the old behaviour (the
// roll goes ahead and saveGachaItems starts a fresh blob).
func (svc *GachaService) checkPendingItems(charID uint32) error {
	data, err := svc.charRepo.LoadColumn(charID, "gacha_items")
	if err != nil || len(data) == 0 {
		return nil
	}
	if _, _, _, valid := inspectGachaItemBlob(data); !valid {
		return errGachaItemsBlocked
	}
	return nil
}

// spendGachaCoin deducts gacha coins, preferring trial coins over premium.
func (svc *GachaService) spendGachaCoin(userID uint32, quantity uint16) error {
	trial, _ := svc.userRepo.GetTrialCoins(userID)
	if quantity <= trial {
		err := svc.userRepo.DeductTrialCoins(userID, uint32(quantity))
		if !errors.Is(err, errInsufficientBalance) {
			return err
		}
		// Trial coins were spent elsewhere since the read: use premium coins.
	}
	if err := svc.userRepo.DeductPremiumCoins(userID, uint32(quantity)); err != nil {
		return fmt.Errorf("deduct gacha coins: %w", err)
	}
	return nil
}

// resolveRewards selects random entries and resolves them into rewards.
func (svc *GachaService) resolveRewards(entries []GachaEntry, rolls int, isBox bool) []GachaReward {
	rewardEntries, err := getRandomEntries(entries, rolls, isBox)
	if err != nil {
		svc.logger.Warn("Failed to select gacha entries", zap.Error(err))
		return nil
	}
	var rewards []GachaReward
	for i := range rewardEntries {
		entryItems, err := svc.gachaRepo.GetItemsForEntry(rewardEntries[i].ID)
		if err != nil {
			svc.logger.Warn("Gacha entry has no items",
				zap.Uint32("entryID", rewardEntries[i].ID), zap.Error(err))
			continue
		}
		for _, item := range entryItems {
			rewards = append(rewards, GachaReward{
				ItemType: item.ItemType,
				ItemID:   item.ItemID,
				Quantity: item.Quantity,
				Rarity:   rewardEntries[i].Rarity,
			})
		}
	}
	return rewards
}

// saveGachaItems appends reward items to the character's gacha item storage.
// Zenny rewards are left out: the client has already credited them.
func (svc *GachaService) saveGachaItems(charID uint32, items []GachaItem) {
	kept := items[:0:0]
	for _, item := range items {
		if item.ItemType != gachaItemTypeZenny {
			kept = append(kept, item)
		}
	}
	items = kept
	if len(items) == 0 {
		return
	}
	data, err := svc.charRepo.LoadColumn(charID, "gacha_items")
	if err != nil {
		svc.logger.Error("Failed to load pending gacha items before append",
			zap.Uint32("charID", charID), zap.Error(err))
		// Preserve the pre-existing behavior on a transient read failure:
		// still attempt to save the newly rolled rewards rather than silently
		// discarding them after their cost has already been deducted.
		data = nil
	}

	oldRecords := 0
	var oldPayload []byte
	if len(data) > 0 {
		declared, actual, trailing, valid := inspectGachaItemBlob(data)
		if !valid {
			svc.logger.Error("Invalid pending gacha items blob; refusing to overwrite it",
				zap.Uint32("charID", charID),
				zap.Int("declared_count", declared),
				zap.Int("actual_records", actual),
				zap.Int("trailing_bytes", trailing))
			return
		}
		oldRecords = actual
		oldPayload = data[1:]
	}

	newItem := byteframe.NewByteFrame()
	newItem.WriteUint8(uint8(len(items) + oldRecords))
	for i := range items {
		newItem.WriteUint8(items[i].ItemType)
		newItem.WriteUint16(items[i].ItemID)
		newItem.WriteUint16(items[i].Quantity)
	}
	newItem.WriteBytes(oldPayload)
	if err := svc.charRepo.SaveColumn(charID, "gacha_items", newItem.Data()); err != nil {
		svc.logger.Error("Failed to update gacha items", zap.Error(err))
	}
}

// rewardsToItems converts GachaReward slices to GachaItem slices for storage.
func rewardsToItems(rewards []GachaReward) []GachaItem {
	items := make([]GachaItem, len(rewards))
	for i, r := range rewards {
		items[i] = GachaItem{ItemType: r.ItemType, ItemID: r.ItemID, Quantity: r.Quantity}
	}
	return items
}

// PlayNormalGacha processes a normal gacha roll: validates the reward pool,
// deducts cost, selects random rewards, saves items, and returns the result.
func (svc *GachaService) PlayNormalGacha(userID, charID, gachaID uint32, rollType uint8) (*GachaPlayResult, error) {
	entries, err := svc.gachaRepo.GetRewardPool(gachaID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("gacha has no valid reward entries")
	}
	if err := svc.checkPendingItems(charID); err != nil {
		return nil, err
	}
	rolls, err := svc.transact(userID, charID, gachaID, rollType)
	if err != nil {
		return nil, err
	}
	rewards := svc.resolveRewards(entries, rolls, false)
	svc.saveGachaItems(charID, rewardsToItems(rewards))
	return &GachaPlayResult{Rewards: rewards}, nil
}

// PlayStepupGacha processes a stepup gacha roll: validates the reward pool,
// deducts cost, advances step, awards frontier points, selects random +
// guaranteed rewards, and saves items.
//
// The roll must be the character's current step (the same noon reset and
// missing-next-step rules as GetStepupStatus), otherwise a client could pay
// for and receive any step, including the last step's guaranteed rewards and
// frontier points, out of order. A step with no random draws (rolls 0, only
// guaranteed rewards) does not need a reward pool.
func (svc *GachaService) PlayStepupGacha(userID, charID, gachaID uint32, rollType uint8) (*StepupPlayResult, error) {
	status, err := svc.GetStepupStatus(gachaID, charID, TimeAdjusted())
	if err != nil {
		return nil, err
	}
	if status.Step != rollType {
		return nil, fmt.Errorf("%w: rolled %d, current %d", errGachaStepOrder, rollType, status.Step)
	}
	_, _, stepRolls, err := svc.gachaRepo.GetEntryForTransaction(gachaID, rollType)
	if err != nil {
		return nil, err
	}
	entries, err := svc.gachaRepo.GetRewardPool(gachaID)
	if err != nil {
		return nil, err
	}
	if stepRolls > 0 && len(entries) == 0 {
		return nil, errors.New("gacha has no valid reward entries")
	}
	if err := svc.checkPendingItems(charID); err != nil {
		return nil, err
	}
	rolls, err := svc.transact(userID, charID, gachaID, rollType)
	if err != nil {
		return nil, err
	}
	if err := svc.userRepo.AddFrontierPointsFromGacha(userID, gachaID, rollType); err != nil {
		svc.logger.Error("Failed to award stepup gacha frontier points", zap.Error(err))
	}
	if err := svc.gachaRepo.DeleteStepup(gachaID, charID); err != nil {
		svc.logger.Error("Failed to delete gacha stepup state", zap.Error(err))
	}
	if err := svc.gachaRepo.InsertStepup(gachaID, rollType+1, charID); err != nil {
		svc.logger.Error("Failed to insert gacha stepup state", zap.Error(err))
	}

	guaranteedItems, _ := svc.gachaRepo.GetGuaranteedItems(rollType, gachaID)
	var randomRewards []GachaReward
	if rolls > 0 {
		randomRewards = svc.resolveRewards(entries, rolls, false)
	}

	var guaranteedRewards []GachaReward
	for _, item := range guaranteedItems {
		guaranteedRewards = append(guaranteedRewards, GachaReward{
			ItemType: item.ItemType,
			ItemID:   item.ItemID,
			Quantity: item.Quantity,
			Rarity:   0,
		})
	}

	svc.saveGachaItems(charID, rewardsToItems(randomRewards))
	svc.saveGachaItems(charID, rewardsToItems(guaranteedRewards))
	return &StepupPlayResult{
		RandomRewards:     randomRewards,
		GuaranteedRewards: guaranteedRewards,
	}, nil
}

// PlayBoxGacha processes a box gacha roll. Each reward entry holds
// gachaBoxBallCount(weight) balls; a roll draws its balls without replacement
// from the balls this character has not drawn yet (as the client shows them).
// A roll that needs more balls than are left is refused before its cost is
// paid. Each drawn ball is recorded, and the rewards are saved and returned.
func (svc *GachaService) PlayBoxGacha(userID, charID, gachaID uint32, rollType uint8) (*GachaPlayResult, error) {
	entries, err := svc.gachaRepo.GetRewardPool(gachaID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("gacha has no valid reward entries")
	}
	drawn, err := svc.gachaRepo.GetBoxDrawCounts(gachaID, charID)
	if err != nil {
		return nil, err
	}
	_, _, rollsWanted, err := svc.gachaRepo.GetEntryForTransaction(gachaID, rollType)
	if err != nil {
		return nil, err
	}
	balls := gachaBoxRemainingBalls(entries, drawn)
	if rollsWanted > len(balls) {
		return nil, fmt.Errorf("%w: %d left, roll draws %d", errGachaBoxShort, len(balls), rollsWanted)
	}
	if err := svc.checkPendingItems(charID); err != nil {
		return nil, err
	}
	rolls, err := svc.transact(userID, charID, gachaID, rollType)
	if err != nil {
		return nil, err
	}
	if rolls > len(balls) {
		rolls = len(balls)
	}
	rewardEntries := gachaBoxDraw(balls, rolls)
	var rewards []GachaReward
	for i := range rewardEntries {
		entryItems, err := svc.gachaRepo.GetItemsForEntry(rewardEntries[i].ID)
		if err != nil {
			svc.logger.Warn("Box gacha entry has no items",
				zap.Uint32("entryID", rewardEntries[i].ID), zap.Error(err))
			continue
		}
		if err := svc.gachaRepo.InsertBoxEntry(gachaID, rewardEntries[i].ID, charID); err != nil {
			svc.logger.Error("Failed to insert gacha box entry", zap.Error(err))
		}
		for _, item := range entryItems {
			rewards = append(rewards, GachaReward{
				ItemType: item.ItemType,
				ItemID:   item.ItemID,
				Quantity: item.Quantity,
				Rarity:   0,
			})
		}
	}
	svc.saveGachaItems(charID, rewardsToItems(rewards))
	return &GachaPlayResult{Rewards: rewards}, nil
}

// GetStepupStatus returns the current stepup step for a character, resetting
// stale progress based on the noon boundary. The now parameter enables
// deterministic testing.
func (svc *GachaService) GetStepupStatus(gachaID, charID uint32, now time.Time) (*StepupStatus, error) {
	// Compute the most recent noon boundary
	y, m, d := now.Date()
	midday := time.Date(y, m, d, 12, 0, 0, 0, now.Location())
	if now.Before(midday) {
		midday = midday.Add(-24 * time.Hour)
	}

	step, createdAt, err := svc.gachaRepo.GetStepupWithTime(gachaID, charID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		svc.logger.Error("Failed to get gacha stepup state", zap.Error(err))
	}

	if err == nil && createdAt.Before(midday) {
		if err := svc.gachaRepo.DeleteStepup(gachaID, charID); err != nil {
			svc.logger.Error("Failed to reset stale gacha stepup", zap.Error(err))
		}
		step = 0
	} else if err == nil {
		hasEntry, _ := svc.gachaRepo.HasEntryType(gachaID, step)
		if !hasEntry {
			if err := svc.gachaRepo.DeleteStepup(gachaID, charID); err != nil {
				svc.logger.Error("Failed to reset gacha stepup state", zap.Error(err))
			}
			step = 0
		}
	}

	return &StepupStatus{Step: step}, nil
}

// GetBoxInfo returns how many balls of each reward entry the character has
// drawn from a box gacha.
func (svc *GachaService) GetBoxInfo(gachaID, charID uint32) ([]BoxDrawCount, error) {
	return svc.gachaRepo.GetBoxDrawCounts(gachaID, charID)
}

// ResetBox clears all drawn entries for a box gacha. A one-time box is never
// reset (errGachaBoxOneTime).
func (svc *GachaService) ResetBox(gachaID, charID uint32) error {
	oneTime, err := svc.gachaRepo.IsOneTimeBox(gachaID)
	if err != nil {
		return err
	}
	if oneTime {
		return errGachaBoxOneTime
	}
	return svc.gachaRepo.DeleteBoxEntries(gachaID, charID)
}

// gachaBoxBallCount is how many balls a box reward entry of this weight holds.
func gachaBoxBallCount(weight float64) int {
	n := int(weight)
	if n < 1 {
		return 1
	}
	if n > gachaBoxMaxBalls {
		return gachaBoxMaxBalls
	}
	return n
}

// gachaBoxRemainingBalls lists one element per ball still in the box.
func gachaBoxRemainingBalls(entries []GachaEntry, drawn []BoxDrawCount) []GachaEntry {
	taken := make(map[uint32]int, len(drawn))
	for _, d := range drawn {
		taken[d.EntryID] += d.Count
	}
	var balls []GachaEntry
	for _, e := range entries {
		for left := gachaBoxBallCount(e.Weight) - taken[e.ID]; left > 0; left-- {
			balls = append(balls, e)
		}
	}
	return balls
}

// gachaBoxDraw takes n balls at random without replacement.
func gachaBoxDraw(balls []GachaEntry, n int) []GachaEntry {
	pool := append([]GachaEntry(nil), balls...)
	chosen := make([]GachaEntry, 0, n)
	for i := 0; i < n && len(pool) > 0; i++ {
		j := rand.Intn(len(pool))
		chosen = append(chosen, pool[j])
		pool[j] = pool[len(pool)-1]
		pool = pool[:len(pool)-1]
	}
	return chosen
}

// getRandomEntries selects random gacha entries. In non-box mode, entries are
// chosen with weighted probability (with replacement). In box mode, entries are
// chosen uniformly without replacement.
func getRandomEntries(entries []GachaEntry, rolls int, isBox bool) ([]GachaEntry, error) {
	if len(entries) == 0 {
		return nil, errors.New("no gacha entries available")
	}
	// Box mode draws without replacement, so clamp rolls to available entries.
	if isBox && rolls > len(entries) {
		rolls = len(entries)
	}
	var chosen []GachaEntry
	var totalWeight float64
	for i := range entries {
		totalWeight += entries[i].Weight
	}
	if !isBox && totalWeight <= 0 {
		return nil, errors.New("gacha entries have zero total weight")
	}
	for rolls != len(chosen) {
		if !isBox {
			result := rand.Float64() * totalWeight
			for _, entry := range entries {
				result -= entry.Weight
				if result < 0 {
					chosen = append(chosen, entry)
					break
				}
			}
		} else {
			result := rand.Intn(len(entries))
			chosen = append(chosen, entries[result])
			entries[result] = entries[len(entries)-1]
			entries = entries[:len(entries)-1]
		}
	}
	return chosen, nil
}
