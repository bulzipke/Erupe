package channelserver

import (
	"errors"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
)

type dailyCafeAtomicSpy struct {
	*mockCharacterRepo
	claimed                           bool
	claimErr, addErr                  error
	claimCalls, addCalls, legacyCalls int
	until                             time.Time
	bonus, daily                      uint32
	maximumNP, delta                  int
}

func (r *dailyCafeAtomicSpy) ClaimDailyCafe(_ uint32, until time.Time, bonus, daily uint32, maximumNP int) (bool, error) {
	r.claimCalls++
	r.until, r.bonus, r.daily, r.maximumNP = until, bonus, daily, maximumNP
	return r.claimed, r.claimErr
}

func (r *dailyCafeAtomicSpy) AddCappedNetcafePoints(_ uint32, delta, maximumNP int) error {
	r.addCalls++
	r.delta, r.maximumNP = delta, maximumNP
	return r.addErr
}

func (r *dailyCafeAtomicSpy) ReadInt(id uint32, column string) (int, error) {
	r.legacyCalls++
	return r.mockCharacterRepo.ReadInt(id, column)
}

func (r *dailyCafeAtomicSpy) SaveInt(id uint32, column string, value int) error {
	r.legacyCalls++
	return r.mockCharacterRepo.SaveInt(id, column, value)
}

func (r *dailyCafeAtomicSpy) ReadTime(id uint32, column string, fallback time.Time) (time.Time, error) {
	r.legacyCalls++
	return r.mockCharacterRepo.ReadTime(id, column, fallback)
}

func (r *dailyCafeAtomicSpy) UpdateDailyCafe(id uint32, until time.Time, bonus, daily uint32) error {
	r.legacyCalls++
	return r.mockCharacterRepo.UpdateDailyCafe(id, until, bonus, daily)
}

func TestDailyCafeAtomicClaimReply(t *testing.T) {
	for _, tc := range []struct {
		name    string
		claimed bool
		err     error
	}{
		{"granted", true, nil},
		{"already_claimed", false, nil},
		{"transaction_failure", false, errors.New("injected grant failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := createMockServer()
			r := &dailyCafeAtomicSpy{mockCharacterRepo: newMockCharacterRepo(), claimed: tc.claimed, claimErr: tc.err}
			srv.charRepo = r
			srv.erupeConfig.GameplayOptions.MaximumNP = 100
			srv.erupeConfig.GameplayOptions.BonusQuestAllowance = 10
			srv.erupeConfig.GameplayOptions.DailyQuestAllowance = 5
			s := createMockSession(100, srv)
			before := nextDailyCafeReset(TimeAdjusted())
			handleMsgMhfCheckDailyCafepoint(s, &mhfpacket.MsgMhfCheckDailyCafepoint{AckHandle: 811})
			after := nextDailyCafeReset(TimeAdjusted())
			var payload []byte
			if tc.err == nil {
				bf := byteframe.NewByteFrame()
				bf.WriteBool(tc.claimed)
				if tc.claimed {
					bf.WriteUint32(5)
					bf.WriteUint32(10)
					bf.WriteUint32(5)
				} else {
					bf.WriteUint32(0)
					bf.WriteUint32(0)
					bf.WriteUint32(0)
				}
				payload = bf.Data()
			}
			assertDailyPointIntegrityAck(t, s, 811, true, tc.err != nil, payload)
			if r.claimCalls != 1 || r.addCalls != 0 || r.legacyCalls != 0 || r.maximumNP != 100 || r.bonus != 10 || r.daily != 5 {
				t.Fatal("grant did not use the single atomic repository operation")
			}
			if !r.until.Equal(before) && !r.until.Equal(after) {
				t.Fatalf("wrong daily boundary: %s", r.until)
			}
		})
	}
}

func TestDailyCafeNoonBoundary(t *testing.T) {
	zone := time.FixedZone("UTC+9", 9*60*60)
	noon := time.Date(2026, 12, 31, 12, 0, 0, 0, zone)
	for _, tc := range []struct {
		name      string
		now, want time.Time
	}{
		{"before", noon.Add(-time.Nanosecond), noon},
		{"equal", noon, noon},
		{"after", noon.Add(time.Nanosecond), time.Date(2027, 1, 1, 12, 0, 0, 0, zone)},
		{"midnight", time.Date(2027, 1, 1, 0, 0, 0, 0, zone), time.Date(2027, 1, 1, 12, 0, 0, 0, zone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextDailyCafeReset(tc.now); !got.Equal(tc.want) {
				t.Fatalf("daily boundary=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestDailyCafeCappedAddWiring(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			srv := createMockServer()
			r := &dailyCafeAtomicSpy{mockCharacterRepo: newMockCharacterRepo()}
			if failure {
				r.addErr = errors.New("injected atomic add failure")
			}
			srv.charRepo = r
			srv.erupeConfig.GameplayOptions.MaximumNP = 100
			err := addPointNetcafe(createMockSession(100, srv), 200)
			if (err != nil) != failure || (failure && !errors.Is(err, r.addErr)) {
				t.Fatalf("wrong add error: %v", err)
			}
			if r.addCalls != 1 || r.delta != 200 || r.maximumNP != 100 || r.legacyCalls != 0 || r.claimCalls != 0 {
				t.Fatal("capped add used split or duplicate operations")
			}
		})
	}
}

func TestDailyCafePointReadFailureACK(t *testing.T) {
	srv := createMockServer()
	r := newMockCharacterRepo()
	r.readErr = errors.New("injected NP read failure")
	srv.charRepo = r
	s := createMockSession(100, srv)
	handleMsgMhfUpdateCafepoint(s, &mhfpacket.MsgMhfUpdateCafepoint{AckHandle: 823})
	assertDailyPointIntegrityAck(t, s, 823, false, true, make([]byte, 4))
}
