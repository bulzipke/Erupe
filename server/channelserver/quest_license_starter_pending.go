package channelserver

import (
	"fmt"
	"math"
	"slices"
	"sync"
)

// A zero-value questLicenseStarterPending is ready for one authenticated
// session. It records DB-prepared native claims only; it does not grant items,
// commit DB receipts, or prove which inventory bytes a save contains.
// Canonical unpacked clients enqueue snapshot -> ACQUIRE_ITEM -> SAVEDATA in
// HD 1151ced0 (calls 1151cf1e/1151cf40/1151cf82) and non-HD 114f6630
// (114f667e/114f66a0/114f66e2), before polling either ACK. A failed claim ACK
// therefore cannot by itself prove the already-enqueued save is safe to commit.
type questLicenseStarterPending struct {
	mu          sync.Mutex
	userID      uint32
	characterID uint32
	items       map[uint32]CampaignReward
	rejected    bool
}

func sameQuestLicenseStarterPendingItem(a, b CampaignReward) bool {
	return a.ID == b.ID && a.ItemType == b.ItemType && a.ItemID == b.ItemID && a.Quantity == b.Quantity && a.Deadline.Equal(b.Deadline)
}

func (p *questLicenseStarterPending) Stage(userID, characterID uint32, items []CampaignReward) error {
	if p == nil || userID == 0 || userID > math.MaxInt32 || characterID == 0 || characterID > math.MaxInt32 || len(items) > 512 {
		return fmt.Errorf("invalid starter pending identity or count")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rejected {
		return fmt.Errorf("starter claim requires session resynchronization")
	}
	if p.userID != 0 && (p.userID != userID || p.characterID != characterID) {
		return fmt.Errorf("starter pending session identity changed")
	}
	seen := make(map[uint32]bool, len(items))
	count := len(p.items)
	for _, item := range items {
		if item.ID < campaignCoinRewardBase || item.ID > math.MaxInt32 || item.ItemType != 7 || item.ItemID == 0 || item.Quantity == 0 || seen[item.ID] {
			return fmt.Errorf("invalid pending starter item")
		}
		seen[item.ID] = true
		if old, exists := p.items[item.ID]; exists {
			if !sameQuestLicenseStarterPendingItem(old, item) {
				return fmt.Errorf("pending starter item snapshot changed")
			}
		} else {
			count++
		}
	}
	if count > 512 {
		return fmt.Errorf("too many session starter receipts")
	}
	if len(items) == 0 {
		return nil
	}
	if p.items == nil {
		p.items = make(map[uint32]CampaignReward, len(items))
	}
	p.userID, p.characterID = userID, characterID
	for _, item := range items {
		p.items[item.ID] = item
	}
	return nil
}

func (p *questLicenseStarterPending) Snapshot(userID, characterID uint32) []uint32 {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.userID != userID || p.characterID != characterID {
		return nil
	}
	ids := make([]uint32, 0, len(p.items))
	for id := range p.items {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Reject latches a failed native claim until the session is destroyed/reset.
// The client enqueues SAVEDATA before it sees the claim ACK, so merely returning
// a failed claim ACK must never allow that already-enqueued blob to save without
// its receipt. Even a mismatched/invalid rejection identity fails closed.
func (p *questLicenseStarterPending) Reject(userID, characterID uint32) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rejected = true
	if p.userID == 0 && userID > 0 && userID <= math.MaxInt32 && characterID > 0 && characterID <= math.MaxInt32 {
		p.userID, p.characterID = userID, characterID
	}
}

// SnapshotForClientSave is the only snapshot suitable for the CLIENT savedata
// entry point. Snapshot remains a read-only inspection helper and cannot express
// a failed claim barrier. A rejected save needs a fresh authenticated session,
// not a success ACK, Complete(), or a server-generated save to clear the latch.
func (p *questLicenseStarterPending) SnapshotForClientSave(userID, characterID uint32) ([]uint32, error) {
	if p == nil || userID == 0 || userID > math.MaxInt32 || characterID == 0 || characterID > math.MaxInt32 {
		return nil, fmt.Errorf("invalid client starter save identity")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rejected {
		return nil, fmt.Errorf("starter claim requires session resynchronization")
	}
	if p.userID == 0 {
		return nil, nil
	}
	if p.userID != userID || p.characterID != characterID {
		return nil, fmt.Errorf("client starter save session identity changed")
	}
	ids := make([]uint32, 0, len(p.items))
	for id := range p.items {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// Complete clears only the exact successfully saved snapshot. A failed save
// must leave it staged for retry; later claims remain pending independently.
func (p *questLicenseStarterPending) Complete(userID, characterID uint32, ids []uint32) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.userID != userID || p.characterID != characterID {
		return
	}
	for _, id := range ids {
		delete(p.items, id)
	}
}

func (p *questLicenseStarterPending) HasPending() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rejected || len(p.items) != 0
}

// Reset is for session destruction, never a substitute for receipt completion.
// Durable uncommitted DB rows remain available after the next authenticated login.
func (p *questLicenseStarterPending) Reset() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userID, p.characterID, p.items = 0, 0, nil
	p.rejected = false
}
