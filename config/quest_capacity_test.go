package config

import "testing"

func TestEventQuestCapacityDefaultsAndWireBounds(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        Mode
		value, want int
	}{
		{"omitted expanded", ZZ, 0, DefaultEventQuestCapacity},
		{"minimum expanded", ZZ, 513, 513},
		{"expanded ZZ", ZZ, 16384, 16384},
		{"server wire maximum", ZZ, 65535, 65535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{RealClientMode: tc.mode, EventQuestCapacity: tc.value}
			got, err := c.EffectiveEventQuestCapacity()
			if err != nil || got != tc.want {
				t.Fatalf("capacity=%d err=%v, want %d", got, err, tc.want)
			}
		})
	}
	for _, value := range []int{-1, 1, 511, 512, 65536} {
		c := &Config{RealClientMode: ZZ, EventQuestCapacity: value}
		if got, err := c.EffectiveEventQuestCapacity(); err == nil || got != 0 {
			t.Fatalf("invalid %d accepted: %d %v", value, got, err)
		}
		// Unmanaged enumeration uses the same expanded path and capacity.
		if err := c.LoadQuestDelivery(); err == nil {
			t.Fatalf("disabled managed delivery bypassed capacity check: %d", value)
		}
	}
}

func TestEventQuestCapacityDoesNotInspectClientVersion(t *testing.T) {
	for mode := S1; mode <= ZZ; mode++ {
		c := &Config{RealClientMode: mode}
		if got, err := c.EffectiveEventQuestCapacity(); err != nil || got != DefaultEventQuestCapacity {
			t.Fatalf("capacity added a client-version gate: %d %v", mode, err)
		}
		if err := c.LoadQuestDelivery(); err != nil {
			t.Fatalf("disabled manifest added a client-version gate: %d %v", mode, err)
		}
	}
}
