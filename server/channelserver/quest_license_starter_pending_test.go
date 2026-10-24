package channelserver

import (
	"slices"
	"sync"
	"testing"
	"time"
)

func starterPendingTestItem(id uint32) CampaignReward {
	return CampaignReward{ID: campaignCoinRewardBase + id, ItemType: 7, ItemID: 1859, Quantity: 1, Deadline: time.Unix(0, 0).UTC()}
}

func TestQuestLicenseStarterPendingScopeAndSnapshots(t *testing.T) {
	var pending questLicenseStarterPending
	a, b := starterPendingTestItem(1), starterPendingTestItem(2)
	if pending.HasPending() || len(pending.Snapshot(1, 2)) != 0 {
		t.Fatal("zero value retained receipts")
	}
	if err := pending.Stage(1, 2, []CampaignReward{a}); err != nil {
		t.Fatal(err)
	}
	if err := pending.Stage(1, 2, []CampaignReward{a}); err != nil {
		t.Fatal("identical claim retry failed", err)
	}
	snapshot := pending.Snapshot(1, 2)
	if !slices.Equal(snapshot, []uint32{a.ID}) || !pending.HasPending() {
		t.Fatal(snapshot)
	}
	snapshot[0] = b.ID
	if !slices.Equal(pending.Snapshot(1, 2), []uint32{a.ID}) {
		t.Fatal("caller mutated session receipts")
	}
	if err := pending.Stage(2, 2, []CampaignReward{b}); err == nil {
		t.Fatal("another account reused staged claims")
	}
	if err := pending.Stage(1, 3, []CampaignReward{b}); err == nil {
		t.Fatal("another character reused staged claims")
	}
	pending.Complete(1, 3, []uint32{a.ID})
	if len(pending.Snapshot(1, 3)) != 0 || !pending.HasPending() {
		t.Fatal("wrong character accessed or cleared receipts")
	}
	if err := pending.Stage(1, 2, []CampaignReward{b}); err != nil {
		t.Fatal(err)
	}
	pending.Complete(1, 2, []uint32{a.ID})
	if !slices.Equal(pending.Snapshot(1, 2), []uint32{b.ID}) {
		t.Fatal("save completion cleared a later receipt")
	}
	pending.Reset()
	if pending.HasPending() || len(pending.Snapshot(1, 2)) != 0 {
		t.Fatal("session destruction retained in-memory claims")
	}
	if err := pending.Stage(2, 3, []CampaignReward{a}); err != nil {
		t.Fatal("reset did not release session identity", err)
	}
}

func TestQuestLicenseStarterPendingRejectsAtomically(t *testing.T) {
	var pending questLicenseStarterPending
	a, b := starterPendingTestItem(1), starterPendingTestItem(2)
	if err := pending.Stage(1, 2, []CampaignReward{a}); err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.Quantity = 2
	invalid := b
	invalid.ItemType = 9
	for _, items := range [][]CampaignReward{{b, changed}, {b, invalid}, {b, b}, {{ID: 1, ItemType: 7, ItemID: 1859, Quantity: 1}},
		{{ID: b.ID, ItemType: 7, ItemID: 0, Quantity: 1}}, {{ID: b.ID, ItemType: 7, ItemID: 1859, Quantity: 0}}} {
		if err := pending.Stage(1, 2, items); err == nil {
			t.Fatal("invalid staged item accepted")
		}
		if !slices.Equal(pending.Snapshot(1, 2), []uint32{a.ID}) {
			t.Fatal("failed batch partially staged a receipt")
		}
	}
	tooMany := make([]CampaignReward, 513)
	for i := range tooMany {
		tooMany[i] = starterPendingTestItem(uint32(i + 2))
	}
	if err := pending.Stage(1, 2, tooMany); err == nil {
		t.Fatal("unbounded staged batch accepted")
	}
	var unavailable *questLicenseStarterPending
	if unavailable.Stage(1, 2, nil) == nil || unavailable.HasPending() || len(unavailable.Snapshot(1, 2)) != 0 {
		t.Fatal("nil staging tracker accepted claims")
	}
	unavailable.Complete(1, 2, nil)
	unavailable.Reset()
}

func TestQuestLicenseStarterPendingConcurrentRetry(t *testing.T) {
	var pending questLicenseStarterPending
	item := starterPendingTestItem(1)
	var workers sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errors <- pending.Stage(1, 2, []CampaignReward{item})
			_ = pending.Snapshot(1, 2)
			_ = pending.HasPending()
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(pending.Snapshot(1, 2), []uint32{item.ID}) {
		t.Fatal("concurrent retry created multiple staged receipts")
	}
}

func TestQuestLicenseStarterPendingRejectedClaimBlocksEnqueuedSave(t *testing.T) {
	var pending questLicenseStarterPending
	if ids, err := pending.SnapshotForClientSave(1, 2); err != nil || len(ids) != 0 {
		t.Fatal("ordinary client save blocked", ids, err)
	}
	a := starterPendingTestItem(1)
	if err := pending.Stage(1, 2, []CampaignReward{a}); err != nil {
		t.Fatal(err)
	}
	if ids, err := pending.SnapshotForClientSave(1, 2); err != nil || !slices.Equal(ids, []uint32{a.ID}) {
		t.Fatal(ids, err)
	}
	pending.Reject(1, 2)
	if _, err := pending.SnapshotForClientSave(1, 2); err == nil {
		t.Fatal("failed claim allowed already-enqueued inventory save")
	}
	if err := pending.Stage(1, 2, []CampaignReward{a}); err == nil {
		t.Fatal("claim retry cleared the rejected-save barrier")
	}
	pending.Complete(1, 2, []uint32{a.ID})
	if !pending.HasPending() {
		t.Fatal("completion cleared a rejected-save barrier")
	}
	if _, err := pending.SnapshotForClientSave(1, 2); err == nil {
		t.Fatal("empty item snapshot bypassed rejected-save barrier")
	}
	pending.Reset()
	if _, err := pending.SnapshotForClientSave(2, 3); err != nil || pending.HasPending() {
		t.Fatal("new session remained rejected", err)
	}
	// Preparation can fail before anything is staged. The immediately queued
	// save must still be blocked, even if the rejection has invalid identity.
	pending.Reject(0, 0)
	if _, err := pending.SnapshotForClientSave(2, 3); err == nil {
		t.Fatal("unstaged rejected claim bypassed client save barrier")
	}
	var unavailable *questLicenseStarterPending
	if _, err := unavailable.SnapshotForClientSave(1, 2); err == nil {
		t.Fatal("nil checked tracker accepted a client save")
	}
}
