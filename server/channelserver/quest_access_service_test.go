package channelserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

type questAccessStoreStub struct {
	load func(QuestAccessLoadRequest) (*QuestAccessSnapshot, error)
}

func (s questAccessStoreStub) Load(_ context.Context, request QuestAccessLoadRequest) (*QuestAccessSnapshot, error) {
	return s.load(request)
}

func TestQuestAccessServiceLegacyIndependentOfDisabledLicenses(t *testing.T) {
	licenseStore := &questLicenseStoreStub{}
	licenses, _ := NewQuestLicenseService(false, serviceLicenseCatalog(t, false), licenseStore)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, disabled := range []*QuestLicenseService{nil, {}, licenses} {
		service, err := NewQuestAccessService(questAccessStoreStub{load: func(request QuestAccessLoadRequest) (*QuestAccessSnapshot, error) {
			if request.AccountLicensesEnabled || request.Catalog != nil {
				t.Fatal("disabled account service reached new tables")
			}
			return newQuestAccessSnapshot(1, 2, now, map[int]questAccessGrant{40142: {legacyExpiry: now.Add(time.Hour)}}, nil), nil
		}}, disabled)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := service.Load(context.Background(), 1, 2, []int{40142})
		if err != nil || !snapshot.Allows(40142, now) || snapshot.AccountReadError() != nil {
			t.Fatal(snapshot, err)
		}
	}
	if licenseStore.calls != 0 || licenseStore.binds != 0 {
		t.Fatal("disabled licenses performed database work")
	}
}

func TestQuestAccessServiceDefinitionFailureRetainsOnlyLegacy(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	licenseStore := &questLicenseStoreStub{bind: func(_ []questlicense.Definition) error { return errors.New("definition changed") }}
	licenses, _ := NewQuestLicenseService(true, serviceLicenseCatalog(t, false), licenseStore)
	service, err := NewQuestAccessService(questAccessStoreStub{load: func(request QuestAccessLoadRequest) (*QuestAccessSnapshot, error) {
		if request.AccountLicensesEnabled || request.Catalog != nil {
			t.Fatal("definition failure permitted account rights")
		}
		return newQuestAccessSnapshot(1, 2, now, map[int]questAccessGrant{40142: {legacyExpiry: now.Add(time.Hour)}, 40143: {}}, nil), nil
	}}, licenses)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Load(context.Background(), 1, 2, []int{40142, 40143})
	if err != nil || !snapshot.Allows(40142, now) || snapshot.Allows(40143, now) || snapshot.AccountReadError() == nil {
		t.Fatal(snapshot, err)
	}
}

func TestQuestAccessServiceBoundAccountAndIdentityValidation(t *testing.T) {
	licenseStore := &questLicenseStoreStub{}
	licenses, _ := NewQuestLicenseService(true, serviceLicenseCatalog(t, false), licenseStore)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	storeCalls := 0
	service, _ := NewQuestAccessService(questAccessStoreStub{load: func(request QuestAccessLoadRequest) (*QuestAccessSnapshot, error) {
		storeCalls++
		if !request.AccountLicensesEnabled || request.Catalog != licenses.catalog || licenseStore.binds != 1 {
			t.Fatal("account read preceded immutable definition binding")
		}
		return newQuestAccessSnapshot(request.UserID, request.CharacterID, now, map[int]questAccessGrant{40142: {accountPermanent: true}}, nil), nil
	}}, licenses)
	snapshot, err := service.Load(context.Background(), 1, 2, []int{40142})
	if err != nil || !snapshot.Allows(40142, now.AddDate(5, 0, 0)) || snapshot.Allows(40142, now.Add(-time.Nanosecond)) {
		t.Fatal(snapshot, err)
	}
	if _, err = service.Load(context.Background(), 0, 2, []int{40142}); err == nil || storeCalls != 1 {
		t.Fatal("invalid request reached store")
	}
	wrong, _ := NewQuestAccessService(questAccessStoreStub{load: func(QuestAccessLoadRequest) (*QuestAccessSnapshot, error) {
		return newQuestAccessSnapshot(99, 2, now, nil, nil), nil
	}}, nil)
	if snapshot, err = wrong.Load(context.Background(), 1, 2, []int{40142}); snapshot != nil || err == nil {
		t.Fatal("store identity mismatch was published")
	}
	if _, err := NewQuestAccessService(nil, nil); err == nil {
		t.Fatal("missing store accepted")
	}
}
