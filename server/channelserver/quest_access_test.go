package channelserver

import (
	"math"
	"slices"
	"testing"
	"time"
)

type questAccessMapCatalog map[int][]string

func (c questAccessMapCatalog) KeysForQuest(id int) []string { return c[id] }

type questAccessPanicCatalog struct{}

func (questAccessPanicCatalog) KeysForQuest(int) []string {
	panic("disabled account access must not consult the catalog")
}

type questAccessMutableError struct{ message string }

func (e *questAccessMutableError) Error() string { return e.message }

func TestQuestAccessSnapshotAdmissionAndExpiryBoundaries(t *testing.T) {
	observed := time.Date(2026, 10, 23, 12, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	end := observed.Add(72 * time.Hour)
	snapshot := newQuestAccessSnapshot(7, 11, observed, map[int]questAccessGrant{
		40079: {legacyExpiry: end},
		40142: {accountPermanent: true, legacyExpiry: end},
		40143: {},
	}, nil)
	for _, test := range []struct {
		name string
		id   int
		at   time.Time
		want bool
	}{
		{"before observation cannot borrow legacy", 40079, observed.Add(-time.Nanosecond), false},
		{"before observation cannot borrow purchase", 40142, observed.Add(-time.Nanosecond), false},
		{"observed legacy", 40079, observed, true},
		{"last valid legacy instant", 40079, end.Add(-time.Nanosecond), true},
		{"exclusive legacy end", 40079, end, false},
		{"expired legacy", 40079, end.Add(time.Hour), false},
		{"permanent survives legacy end", 40142, end, true},
		{"permanent is not sale window", 40142, observed.AddDate(20, 0, 0), true},
		{"known but no right", 40143, observed, false},
		{"unknown", 40218, observed, false},
		{"zero time", 40142, time.Time{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := snapshot.Allows(test.id, test.at); got != test.want {
				t.Fatalf("Allows(%d,%s)=%t want %t", test.id, test.at, got, test.want)
			}
		})
	}
	if !snapshot.ObservedAt().Equal(observed) || snapshot.ObservedAt().Location() != time.UTC {
		t.Fatalf("observation was not copied in UTC: %s", snapshot.ObservedAt())
	}
	for _, empty := range []*QuestAccessSnapshot{nil, {}} {
		if empty.Allows(40142, observed) || !empty.ObservedAt().IsZero() {
			t.Fatal("nil/zero snapshot granted authority")
		}
	}
}

func TestQuestAccessSnapshotDoesNotRetainMutableInputs(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	grants := map[int]questAccessGrant{40079: {accountPermanent: true}, 40142: {}}
	snapshot := newQuestAccessSnapshot(7, 11, now, grants, nil)
	delete(grants, 40079)
	grants[40142] = questAccessGrant{accountPermanent: true}
	grants[40218] = questAccessGrant{accountPermanent: true}
	if !snapshot.Allows(40079, now) || snapshot.Allows(40142, now) || snapshot.Allows(40218, now) {
		t.Fatal("caller map changed issued authority")
	}
	copyTime := snapshot.ObservedAt().Add(-time.Hour)
	if !snapshot.ObservedAt().Equal(now) || snapshot.Allows(40079, copyTime) {
		t.Fatal("copied observation changed issued authority")
	}
}

func TestQuestAccessSnapshotRetainsOnlyImmutableAccountWarning(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	warning := &questAccessMutableError{message: "account source unavailable"}
	snapshot := newQuestAccessSnapshot(7, 11, now, map[int]questAccessGrant{
		40079: {legacyExpiry: now.Add(time.Hour)}, 40142: {},
	}, warning)
	warning.message = "changed driver error"
	if got := snapshot.AccountReadError(); got == nil || got.Error() != "account source unavailable" {
		t.Fatalf("diagnostic retained mutable driver error: %v", got)
	}
	if !snapshot.Allows(40079, now) || snapshot.Allows(40142, now) {
		t.Fatal("degraded account source changed legacy or granted an account right")
	}
	var empty *QuestAccessSnapshot
	if empty.AccountReadError() != nil {
		t.Fatal("nil snapshot has a source diagnostic")
	}
}

func TestQuestAccessRequestFreezesOnlyExactTargets(t *testing.T) {
	input := []int{40142, 40079}
	catalog := questAccessMapCatalog{
		40079: {"shared", "gothic"},
		40142: {"veil", "shared"},
		40218: {"invalid unrelated key"},
	}
	ids, mappings, keys, err := prepareQuestAccessRequest(QuestAccessLoadRequest{
		UserID: 7, CharacterID: 11, QuestIDs: input, AccountLicensesEnabled: true, Catalog: catalog,
	})
	if err != nil || !slices.Equal(ids, []int{40079, 40142}) ||
		!slices.Equal(keys, []string{"gothic", "shared", "veil"}) || len(mappings) != 2 {
		t.Fatalf("selected mappings: %v %v %v %v", ids, mappings, keys, err)
	}
	if !slices.Equal(input, []int{40142, 40079}) {
		t.Fatal("target normalization mutated the caller")
	}
	input[0] = 40218
	catalog[40079][0] = "changed"
	if !slices.Equal(ids, []int{40079, 40142}) || !slices.Equal(mappings[40079], []string{"gothic", "shared"}) {
		t.Fatal("mutable target/catalog storage escaped into the request")
	}
}

func TestQuestAccessRequestDisabledAccountBranchKeepsLegacyTargets(t *testing.T) {
	for _, catalog := range []QuestAccessCatalog{nil, questAccessPanicCatalog{}} {
		ids, mappings, keys, err := prepareQuestAccessRequest(QuestAccessLoadRequest{
			UserID: 7, CharacterID: 11, QuestIDs: []int{40142, 40079}, Catalog: catalog,
		})
		if err != nil || !slices.Equal(ids, []int{40079, 40142}) || len(mappings) != 0 || len(keys) != 0 {
			t.Fatalf("legacy-only request: %v %v %v %v", ids, mappings, keys, err)
		}
	}
}

func TestQuestAccessRequestRejectsInvalidScopeBeforeLoading(t *testing.T) {
	valid := QuestAccessLoadRequest{UserID: 7, CharacterID: 11, QuestIDs: []int{40079}}
	for _, test := range []struct {
		name   string
		change func(*QuestAccessLoadRequest)
	}{
		{"zero user", func(r *QuestAccessLoadRequest) { r.UserID = 0 }},
		{"large user", func(r *QuestAccessLoadRequest) { r.UserID = math.MaxInt32 + 1 }},
		{"zero character", func(r *QuestAccessLoadRequest) { r.CharacterID = 0 }},
		{"large character", func(r *QuestAccessLoadRequest) { r.CharacterID = math.MaxInt32 + 1 }},
		{"zero quest", func(r *QuestAccessLoadRequest) { r.QuestIDs = []int{0} }},
		{"negative quest", func(r *QuestAccessLoadRequest) { r.QuestIDs = []int{-1} }},
		{"large quest", func(r *QuestAccessLoadRequest) { r.QuestIDs = []int{math.MaxUint16 + 1} }},
		{"duplicate quest", func(r *QuestAccessLoadRequest) { r.QuestIDs = []int{40079, 40079} }},
		{"too many quests", func(r *QuestAccessLoadRequest) { r.QuestIDs = make([]int, maxQuestAccessTargets+1) }},
		{"missing enabled catalog", func(r *QuestAccessLoadRequest) { r.AccountLicensesEnabled = true }},
		{"invalid catalog key", func(r *QuestAccessLoadRequest) {
			r.AccountLicensesEnabled, r.Catalog = true, questAccessMapCatalog{40079: {"untrusted key"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.change(&request)
			if _, _, _, err := prepareQuestAccessRequest(request); err == nil {
				t.Fatal("accepted invalid authorization scope")
			}
		})
	}
}
