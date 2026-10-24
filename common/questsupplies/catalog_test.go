package questsupplies

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// All dates and window names here are synthetic fixtures, not approved
// operating dates or production season identities.
var fixtureStart = time.Date(2000, 1, 2, 3, 0, 0, 0, time.UTC)

func fixtureDocument() Document {
	d := ApprovedSeasonalDocument()
	d.Windows = []Window{
		{ID: "fixture-gw", SupplyKey: GWKey, StartsAt: fixtureStart, EndsAt: fixtureStart.Add(48 * time.Hour)},
		{ID: "fixture-new-year", SupplyKey: HunterDiceBagKey, StartsAt: fixtureStart, EndsAt: fixtureStart.Add(48 * time.Hour)},
	}
	return d
}

func mustCatalog(t *testing.T, d Document) *Catalog {
	t.Helper()
	c, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustPrepare(t *testing.T, c *Catalog, key, window string, identity Identity, nonce Nonce, now time.Time) Prepared {
	t.Helper()
	p, err := c.Prepare(key, window, identity, nonce, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApprovedDraftPreservesPolicyAndHasNoWindowOrActivation(t *testing.T) {
	c, err := NewApprovedSeasonalDraft()
	if err != nil {
		t.Fatal(err)
	}
	d := c.Document()
	if d.Version != "season-supplies-approved-20261024-v1" || len(d.Definitions) != 2 || len(d.Windows) != 0 {
		t.Fatalf("unexpected approved draft: %#v", d)
	}
	tests := []struct {
		key            string
		kind           SupplyKind
		item, quantity uint16
		price          uint32
		quests         []uint16
	}{
		{GWKey, FreeCharacterWindow, 6831, 4, 0, []uint16{50070, 50071, 50072, 50073}},
		{HunterDiceBagKey, PaidRepeatable, 3638, 1, 1, []uint16{51032, 51033, 51034, 51035, 55620, 55621, 55622, 55623, 55624}},
	}
	for _, test := range tests {
		got, exists := c.Definition(test.key)
		if !exists || got.Kind != test.kind || got.Delivery != (ItemStack{StandardItemType, test.item, test.quantity}) || got.PriceGoldG != test.price || got.QuestRules != PreserveOriginalQuestRules {
			t.Fatalf("wrong approved supply %s: %#v", test.key, got)
		}
		var ids []uint16
		for _, q := range got.Quests {
			ids = append(ids, q.QuestID)
			if q.EntryItem != (ItemStack{StandardItemType, test.item, 1}) {
				t.Fatalf("delivery quantity confused with per-quest requirement: %#v", q)
			}
		}
		if !reflect.DeepEqual(ids, test.quests) {
			t.Fatalf("quest mappings changed: %v", ids)
		}
		if _, err := c.Prepare(test.key, "", Identity{1, 2}, Nonce{1}, fixtureStart); !errors.Is(err, ErrUnknownWindow) {
			t.Fatalf("unbound approved draft prepared an intent: %v", err)
		}
	}
}

func TestInvalidDefinitionsRejected(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Document)
	}{
		{"empty version", func(d *Document) { d.Version = "" }},
		{"empty definitions", func(d *Document) { d.Definitions = nil }},
		{"invalid key", func(d *Document) { d.Definitions[0].Key = "../gift" }},
		{"duplicate key", func(d *Document) { d.Definitions = append(d.Definitions, d.Definitions[0]) }},
		{"quest rights are not standard items", func(d *Document) { d.Definitions[0].Delivery.ItemType = 9 }},
		{"zero item", func(d *Document) { d.Definitions[0].Delivery.ItemID = 0 }},
		{"zero quantity", func(d *Document) { d.Definitions[0].Delivery.Quantity = 0 }},
		{"free has price", func(d *Document) { d.Definitions[0].PriceGoldG = 1 }},
		{"paid is free", func(d *Document) { d.Definitions[1].PriceGoldG = 0 }},
		{"paid exceeds campaign quote price", func(d *Document) { d.Definitions[1].PriceGoldG = MaxGoldGPrice + 1 }},
		{"unknown kind", func(d *Document) { d.Definitions[0].Kind = "permanent_right" }},
		{"waived rules", func(d *Document) { d.Definitions[0].QuestRules = "waive_rules" }},
		{"missing quests", func(d *Document) { d.Definitions[0].Quests = nil }},
		{"zero quest", func(d *Document) { d.Definitions[0].Quests[0].QuestID = 0 }},
		{"duplicate quest", func(d *Document) { d.Definitions[0].Quests[1] = d.Definitions[0].Quests[0] }},
		{"wrong entry item", func(d *Document) { d.Definitions[0].Quests[0].EntryItem.ItemID = 3638 }},
		{"zero entry quantity", func(d *Document) { d.Definitions[0].Quests[0].EntryItem.Quantity = 0 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			d := ApprovedSeasonalDocument()
			test.change(&d)
			if _, err := New(d); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
}

func TestWindowValidationAndAdjacentHalfOpenWindows(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Document)
	}{
		{"empty identity", func(d *Document) { d.Windows[0].ID = "" }},
		{"duplicate identity across supplies", func(d *Document) { d.Windows[1].ID = d.Windows[0].ID }},
		{"unknown supply", func(d *Document) { d.Windows[0].SupplyKey = "other" }},
		{"missing start", func(d *Document) { d.Windows[0].StartsAt = time.Time{} }},
		{"missing end", func(d *Document) { d.Windows[0].EndsAt = time.Time{} }},
		{"empty interval", func(d *Document) { d.Windows[0].EndsAt = d.Windows[0].StartsAt }},
		{"reversed interval", func(d *Document) { d.Windows[0].EndsAt = d.Windows[0].StartsAt.Add(-time.Hour) }},
		{"overlapping same supply", func(d *Document) {
			d.Windows = append(d.Windows, Window{ID: "fixture-gw-two", SupplyKey: GWKey, StartsAt: fixtureStart.Add(time.Hour), EndsAt: fixtureStart.Add(72 * time.Hour)})
		}},
		{"UTC outside encodable year", func(d *Document) {
			d.Windows[0].EndsAt = time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("fixture-west", -12*3600))
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			d := fixtureDocument()
			test.change(&d)
			if _, err := New(d); err == nil {
				t.Fatal("invalid window accepted")
			}
		})
	}
	d := fixtureDocument()
	d.Windows = append(d.Windows, Window{ID: "fixture-gw-next", SupplyKey: GWKey, StartsAt: d.Windows[0].EndsAt, EndsAt: d.Windows[0].EndsAt.Add(24 * time.Hour)})
	c := mustCatalog(t, d) // Different supplies may overlap; adjacent same-supply windows are valid.
	identity := Identity{1, 2}
	if _, err := c.Prepare(GWKey, "fixture-gw", identity, Nonce{}, d.Windows[0].EndsAt); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("old interval end was inclusive: %v", err)
	}
	mustPrepare(t, c, GWKey, "fixture-gw-next", identity, Nonce{}, d.Windows[0].EndsAt)
}

func TestPrepareTimeBoundariesAndInactivePreview(t *testing.T) {
	c := mustCatalog(t, fixtureDocument())
	for _, key := range []string{GWKey, HunterDiceBagKey} {
		window := "fixture-gw"
		if key == HunterDiceBagKey {
			window = "fixture-new-year"
		}
		for _, test := range []struct {
			name string
			at   time.Time
			ok   bool
		}{
			{"before", fixtureStart.Add(-time.Nanosecond), false},
			{"start", fixtureStart, true},
			{"last instant", fixtureStart.Add(48*time.Hour - time.Nanosecond), true},
			{"end", fixtureStart.Add(48 * time.Hour), false},
			{"after", fixtureStart.Add(49 * time.Hour), false},
		} {
			t.Run(key+"/"+test.name, func(t *testing.T) {
				p, err := c.Prepare(key, window, Identity{1, 2}, Nonce{1}, test.at)
				if test.ok {
					if err != nil {
						t.Fatal(err)
					}
					preview := p.Preview()
					if preview.LiveEnabled || preview.CampaignID != 0 {
						t.Fatal("local preview activated a live operation")
					}
				} else if !errors.Is(err, ErrWindowClosed) {
					t.Fatalf("wrong boundary error: %v", err)
				}
			})
		}
	}
}

func TestFreeClaimIdentitySurvivesDateAndCatalogVersionEdits(t *testing.T) {
	d := fixtureDocument()
	a := mustCatalog(t, d)
	p := mustPrepare(t, a, GWKey, "fixture-gw", Identity{1, 2}, Nonce{}, fixtureStart)
	one, ok := p.FreeClaimScope()
	if !ok {
		t.Fatal("free scope missing")
	}
	d.Version = "fixture-version-two"
	d.Windows[0].StartsAt = fixtureStart.Add(-time.Hour)
	d.Windows[0].EndsAt = fixtureStart.Add(72 * time.Hour)
	b := mustCatalog(t, d)
	q := mustPrepare(t, b, GWKey, "fixture-gw", Identity{999, 2}, Nonce{9}, fixtureStart.Add(time.Hour))
	two, ok := q.FreeClaimScope()
	if !ok || one != two {
		t.Fatalf("date/version/account/nonce edit granted a new claim identity: %#v %#v", one, two)
	}
	if a.Digest() == b.Digest() {
		t.Fatal("catalog edit did not change catalog digest")
	}
	ad, _ := a.DefinitionDigest(GWKey)
	bd, _ := b.DefinitionDigest(GWKey)
	if ad != bd {
		t.Fatal("semantic policy digest changed for date/version-only edit")
	}
	otherChar := mustPrepare(t, b, GWKey, "fixture-gw", Identity{1, 3}, Nonce{}, fixtureStart)
	otherScope, _ := otherChar.FreeClaimScope()
	if otherScope == one {
		t.Fatal("different character shared a free claim")
	}
	d.Windows[0].ID = "fixture-next-approved-gw"
	next := mustPrepare(t, mustCatalog(t, d), GWKey, "fixture-next-approved-gw", Identity{1, 2}, Nonce{}, fixtureStart)
	nextScope, _ := next.FreeClaimScope()
	if nextScope == one {
		t.Fatal("distinct approved seasonal identity shared its free claim")
	}
	if _, ok := p.PaidReplayContract(); ok {
		t.Fatal("free gift became a paid purchase")
	}
}

func TestPaidReplayBindingAndRepeatedPurchase(t *testing.T) {
	c := mustCatalog(t, fixtureDocument())
	identity := Identity{11, 12}
	first := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", identity, Nonce{1}, fixtureStart)
	retry := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", identity, Nonce{1}, fixtureStart.Add(time.Hour))
	a, ok := first.PaidReplayContract()
	if !ok {
		t.Fatal("missing paid replay contract")
	}
	b, _ := retry.PaidReplayContract()
	if ComparePaidReplay(a, b) != ReplayExactRequest {
		t.Fatal("exact retry changed due to observation time")
	}
	changedChar := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", Identity{11, 13}, Nonce{1}, fixtureStart)
	b, _ = changedChar.PaidReplayContract()
	if ComparePaidReplay(a, b) != ReplayConflict {
		t.Fatal("same nonce accepted for another character")
	}
	nextPurchase := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", identity, Nonce{2}, fixtureStart)
	b, _ = nextPurchase.PaidReplayContract()
	if ComparePaidReplay(a, b) != ReplayDifferentScope {
		t.Fatal("new nonce did not describe a new purchase")
	}
	otherAccount := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", Identity{22, 23}, Nonce{1}, fixtureStart)
	b, _ = otherAccount.PaidReplayContract()
	if ComparePaidReplay(a, b) != ReplayDifferentScope {
		t.Fatal("different accounts shared the nonce scope")
	}
	for _, mutate := range []func(*Document){
		func(d *Document) { d.Definitions[1].PriceGoldG = 2 },
		func(d *Document) { d.Definitions[1].Delivery.Quantity = 2 },
		func(d *Document) { d.Windows[1].ID = "fixture-other-new-year" },
	} {
		d := fixtureDocument()
		mutate(&d)
		p := mustPrepare(t, mustCatalog(t, d), HunterDiceBagKey, d.Windows[1].ID, identity, Nonce{1}, fixtureStart)
		b, _ = p.PaidReplayContract()
		if ComparePaidReplay(a, b) != ReplayConflict {
			t.Fatal("conflicting paid request retained a reusable nonce contract")
		}
	}
	if _, ok := first.FreeClaimScope(); ok {
		t.Fatal("paid purchase became a free seasonal gift")
	}
}

func TestClosedWindowDoesNotInvalidateExistingReplayContract(t *testing.T) {
	c := mustCatalog(t, fixtureDocument())
	p := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", Identity{1, 2}, Nonce{1}, fixtureStart)
	contract, _ := p.PaidReplayContract()
	if _, err := c.Prepare(HunterDiceBagKey, "fixture-new-year", Identity{1, 2}, Nonce{1}, fixtureStart.Add(48*time.Hour)); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("fresh preparation outside window succeeded: %v", err)
	}
	if ComparePaidReplay(contract, contract) != ReplayExactRequest {
		t.Fatal("existing receipt contract depends on current time")
	}
	// No item or money state exists here; a durable adapter must use this
	// comparison before fresh preparation and must not grant/charge twice.
}

func TestPaidPriceBoundaryAndVersionStableRequest(t *testing.T) {
	d := fixtureDocument()
	d.Definitions[1].PriceGoldG = MaxGoldGPrice
	c := mustCatalog(t, d)
	p := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", Identity{1, 2}, Nonce{1}, fixtureStart)
	if p.Preview().Definition.PriceGoldG != 65535 {
		t.Fatal("maximum campaign-quote price did not survive preparation")
	}
	first, _ := p.PaidReplayContract()
	d.Version = "fixture-edited-version"
	d.Windows[1].StartsAt = fixtureStart.Add(-time.Hour)
	d.Windows[1].EndsAt = fixtureStart.Add(72 * time.Hour)
	q := mustPrepare(t, mustCatalog(t, d), HunterDiceBagKey, "fixture-new-year", Identity{1, 2}, Nonce{1}, fixtureStart.Add(time.Hour))
	retry, _ := q.PaidReplayContract()
	if ComparePaidReplay(first, retry) != ReplayExactRequest {
		t.Fatal("same request changed because only version or window dates changed")
	}
}

func TestPaidReplayRejectsZeroAndPartialContracts(t *testing.T) {
	c := mustCatalog(t, fixtureDocument())
	p := mustPrepare(t, c, HunterDiceBagKey, "fixture-new-year", Identity{1, 2}, Nonce{1}, fixtureStart)
	valid, _ := p.PaidReplayContract()
	cases := []struct {
		name     string
		contract PaidReplayContract
	}{
		{"zero", PaidReplayContract{}},
		{"missing account", PaidReplayContract{Scope: PaidScope{Nonce: Nonce{1}}, ContentDigest: valid.ContentDigest}},
		{"missing nonce", PaidReplayContract{Scope: PaidScope{AccountID: 1}, ContentDigest: valid.ContentDigest}},
		{"missing content", PaidReplayContract{Scope: valid.Scope}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, pair := range [][2]PaidReplayContract{{test.contract, test.contract}, {valid, test.contract}, {test.contract, valid}} {
				if ComparePaidReplay(pair[0], pair[1]) != ReplayInvalid {
					t.Fatal("invalid public receipt contract treated as replayable")
				}
			}
		})
	}
}

func TestPrepareRejectsInvalidIdentityWindowAndNonce(t *testing.T) {
	c := mustCatalog(t, fixtureDocument())
	for _, test := range []struct {
		name, key, window string
		identity          Identity
		nonce             Nonce
		now               time.Time
		want              error
	}{
		{"supply", "unknown", "fixture-gw", Identity{1, 2}, Nonce{}, fixtureStart, ErrUnknownSupply},
		{"window", GWKey, "unknown", Identity{1, 2}, Nonce{}, fixtureStart, ErrUnknownWindow},
		{"wrong supply window", GWKey, "fixture-new-year", Identity{1, 2}, Nonce{}, fixtureStart, ErrWrongWindow},
		{"account", GWKey, "fixture-gw", Identity{0, 2}, Nonce{}, fixtureStart, ErrIdentity},
		{"character", GWKey, "fixture-gw", Identity{1, 0}, Nonce{}, fixtureStart, ErrIdentity},
		{"nonce", HunterDiceBagKey, "fixture-new-year", Identity{1, 2}, Nonce{}, fixtureStart, ErrNonce},
		{"zero observation time", GWKey, "fixture-gw", Identity{1, 2}, Nonce{}, time.Time{}, ErrWindowClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := c.Prepare(test.key, test.window, test.identity, test.nonce, test.now); !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
}

func TestCanonicalDigestAndPolicyChangeDetection(t *testing.T) {
	d := fixtureDocument()
	a := mustCatalog(t, d)
	d.Definitions[0], d.Definitions[1] = d.Definitions[1], d.Definitions[0]
	for i := range d.Definitions {
		q := d.Definitions[i].Quests
		for l, r := 0, len(q)-1; l < r; l, r = l+1, r-1 {
			q[l], q[r] = q[r], q[l]
		}
	}
	d.Windows[0], d.Windows[1] = d.Windows[1], d.Windows[0]
	for i := range d.Windows {
		d.Windows[i].StartsAt = d.Windows[i].StartsAt.In(time.FixedZone("fixture-kst", 9*3600))
		d.Windows[i].EndsAt = d.Windows[i].EndsAt.In(time.FixedZone("fixture-kst", 9*3600))
	}
	b := mustCatalog(t, d)
	if a.Digest() != b.Digest() {
		t.Fatal("equivalent order/timezone changed digest")
	}
	for _, key := range []string{GWKey, HunterDiceBagKey} {
		x, _ := a.DefinitionDigest(key)
		y, _ := b.DefinitionDigest(key)
		if x != y {
			t.Fatalf("definition %s depends on order", key)
		}
	}
	d = fixtureDocument()
	d.Definitions[0].Quests[0].EntryItem.Quantity = 2
	changed := mustCatalog(t, d)
	x, _ := a.DefinitionDigest(GWKey)
	y, _ := changed.DefinitionDigest(GWKey)
	if x == y {
		t.Fatal("changed entry-quantity metadata retained a policy digest")
	}
	if _, ok := a.DefinitionDigest("unknown"); ok {
		t.Fatal("unknown definition has a digest")
	}
}

func TestCatalogAndPreparationDefensiveCopies(t *testing.T) {
	d := fixtureDocument()
	c := mustCatalog(t, d)
	original := c.Digest()
	d.Definitions[0].Delivery.Quantity = 900
	d.Definitions[0].Quests[0].EntryItem.Quantity = 900
	d.Windows[0].ID = "mutated-input"
	copyDoc := c.Document()
	copyDoc.Definitions[0].Quests[0].QuestID = 1
	copyDoc.Windows[0].ID = "mutated-accessor"
	def, _ := c.Definition(GWKey)
	def.Quests[0].EntryItem.Quantity = 77
	p := mustPrepare(t, c, GWKey, "fixture-gw", Identity{1, 2}, Nonce{}, fixtureStart)
	preview := p.Preview()
	preview.Definition.Quests[0].QuestID = 2
	preview.Definition.Delivery.Quantity = 99
	preview.LiveEnabled = true
	preview.CampaignID = 123
	unchanged := p.Preview()
	if unchanged.LiveEnabled || unchanged.CampaignID != 0 || unchanged.Definition.Delivery.Quantity != 4 || unchanged.Definition.Quests[0].QuestID != 50070 {
		t.Fatal("preview mutation escaped its copy")
	}
	current, _ := c.Definition(GWKey)
	if current.Delivery.Quantity != 4 || current.Quests[0].EntryItem.Quantity != 1 || c.Digest() != original {
		t.Fatal("catalog mutated through an input/accessor")
	}
	newDraft := ApprovedSeasonalDocument()
	newDraft.Definitions[0].Quests[0].QuestID = 1
	if ApprovedSeasonalDocument().Definitions[0].Quests[0].QuestID != 50070 {
		t.Fatal("approved factory shares mutable global data")
	}
}

func TestConcurrentImmutableReadsAndPreparations(t *testing.T) {
	c := mustCatalog(t, fixtureDocument())
	original := c.Digest()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p, err := c.Prepare(HunterDiceBagKey, "fixture-new-year", Identity{1, uint32(i + 1)}, Nonce{byte(j + 1)}, fixtureStart)
				if err != nil {
					t.Error(err)
					return
				}
				preview := p.Preview()
				preview.Definition.Quests[0].QuestID = 1
				d := c.Document()
				d.Definitions[0].Quests[0].EntryItem.Quantity = 99
				if c.Digest() != original {
					t.Error("concurrent read changed immutable catalog")
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
