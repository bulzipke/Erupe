package channelserver

import (
	"errors"
	"testing"
	"time"
)

type divaMapActivationSessionRepo struct {
	*divaMapHandlerRepo
	activation             time.Time
	enabled                bool
	activationErr, bindErr error
	bindCalls              int
	bound                  divaInterceptionRun
	routeEligible          bool
}

func (r *divaMapActivationSessionRepo) GetDivaMapActivation(uint32) (time.Time, bool, error) {
	return r.activation, r.enabled, r.activationErr
}

func (r *divaMapActivationSessionRepo) BindDivaMapDeparture(_ uint32, guild, event uint32, quest uint16, key string, started, _ time.Time) (DivaMapDeparture, error) {
	r.bindCalls++
	r.bound = divaInterceptionRun{StartedAt: started, QuestID: quest, EventID: event, GuildID: guild, Key: key}
	return DivaMapDeparture{Enabled: r.routeEligible, MapNumber: 1}, r.bindErr
}

func TestDivaMapLegacySessionRequiresActivationAndValidBinding(t *testing.T) {
	for _, which := range []string{"enabled", "locked route keeps personal", "missing activation", "before activation", "activation lookup failed", "map binding failed", "shifted clock"} {
		t.Run(which, func(t *testing.T) {
			s, base, event := newDivaMapHandlerSession(t)
			base.interception[event.ID] = DivaInterceptionProgress{}
			r := &divaMapActivationSessionRepo{divaMapHandlerRepo: base, activation: TimeAdjusted().Add(-time.Minute), enabled: true, routeEligible: true}
			s.server.divaRepo = r
			want := false
			switch which {
			case "enabled":
				want = true
			case "locked route keeps personal":
				want = true
				r.routeEligible = false
			case "missing activation":
				r.enabled = false
			case "before activation":
				r.activation = TimeAdjusted().Add(time.Minute)
			case "activation lookup failed":
				r.activationErr = errors.New("offline")
			case "map binding failed":
				r.bindErr = errors.New("offline")
			case "shifted clock":
				hour := 12
				s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
			}
			enterDivaInterceptionTestQuest(t, s, "sl2Qs200p0a1u0", 58043)
			got := s.divaInterceptionRunSnapshot()
			if validDivaInterceptionRunKey(got.Key) != want {
				t.Fatalf("bound=%+v want bound=%v", got, want)
			}
			if want && (r.bindCalls != 1 || got.Key != r.bound.Key || got.EventID != event.ID || got.GuildID != 99 || got.QuestID != 58043 || !got.StartedAt.Equal(r.bound.StartedAt)) {
				t.Fatalf("wrong validated departure: %+v %+v", got, r.bound)
			}
			if !want && which != "map binding failed" && r.bindCalls != 0 {
				t.Fatalf("ineligible departure attempted map binding: %d", r.bindCalls)
			}
		})
	}
}
