package channelserver

import (
	"encoding/binary"
	"sync"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestMezFesRotationTuneWire(t *testing.T) {
	baseline := make(map[uint16]uint16)
	for _, enabled := range []bool{false, true} {
		s := newEnumerateQuestSession(0)
		s.server.erupeConfig.GameplayOptions.MezFesRandomMinigame = enabled
		s.mezFesMinigame = 2
		handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 1})
		p := readAck(t, s).Payload
		if binary.BigEndian.Uint16(p) != 0 {
			t.Fatal("expected empty quest list")
		}
		key := binary.BigEndian.Uint16(p[2:])
		count := int(binary.BigEndian.Uint16(p[4:]))
		found := 0
		ordinary := make(map[uint16]uint16)
		for i := 0; i < count; i++ {
			row := p[6+i*10 : 6+(i+1)*10]
			id, value := binary.BigEndian.Uint16(row)^key, binary.BigEndian.Uint16(row[8:])^key
			if id != mezFesMinigameTuneID {
				ordinary[id] = value
			}
			if binary.BigEndian.Uint16(row)^key == mezFesMinigameTuneID {
				found++
				if binary.BigEndian.Uint16(row[8:])^key != 2 {
					t.Fatal("wrong game on wire")
				}
			}
		}
		if (found == 1) != enabled || count > tuneLimitZZ {
			t.Fatalf("enabled=%v custom entries=%d count=%d", enabled, found, count)
		}
		if !enabled {
			baseline = ordinary
		} else {
			if len(ordinary) != len(baseline) {
				t.Fatal("custom signal truncated an existing tune")
			}
			for id, value := range baseline {
				if got, ok := ordinary[id]; !ok || got != value {
					t.Fatalf("existing tune %d changed", id)
				}
			}
		}
	}
}

func TestMezFesRotationWorldLifecycle(t *testing.T) {
	count := 0
	r := &MezFesRotation{choose: func() uint16 { count++; return uint16(count * 2) }}
	c := &cfg.Config{GameplayOptions: cfg.GameplayOptions{MezFesRandomMinigame: true}}
	first := &Session{server: &Server{worldType: 6, erupeConfig: c, mezFesRotation: r}}
	second := &Session{server: &Server{worldType: 6, erupeConfig: c, mezFesRotation: r}}
	first.acquireMezFesMinigame()
	first.acquireMezFesMinigame() // duplicate login must not retain two seats
	second.acquireMezFesMinigame()
	if r.sessions != 2 || count != 1 || first.effectiveMezFesMinigame() != 2 || second.effectiveMezFesMinigame() != 2 {
		t.Fatal("channels did not share one draw")
	}
	first.releaseMezFesMinigame()
	first.releaseMezFesMinigame() // normal and panic cleanup are idempotent
	if r.sessions != 1 || r.game != 2 {
		t.Fatal("rotation changed while a player remained")
	}
	second.releaseMezFesMinigame()
	if r.sessions != 0 || r.game != 0 {
		t.Fatal("empty world did not reset")
	}
	first.acquireMezFesMinigame()
	if first.effectiveMezFesMinigame() != 4 || count != 2 {
		t.Fatal("next first login did not draw again")
	}
	first.releaseMezFesMinigame()
}

func TestMezFesRotationIgnoresOtherWorldsDisabledAndClosedSessions(t *testing.T) {
	for _, tt := range []struct {
		world           uint8
		enabled, closed bool
	}{{1, true, false}, {6, false, false}, {6, true, true}} {
		r := &MezFesRotation{choose: func() uint16 { t.Fatal("unexpected draw"); return 2 }}
		c := &cfg.Config{GameplayOptions: cfg.GameplayOptions{MezFesRandomMinigame: tt.enabled}}
		s := &Session{server: &Server{worldType: tt.world, erupeConfig: c, mezFesRotation: r}}
		s.closed.Store(tt.closed)
		s.acquireMezFesMinigame()
		s.releaseMezFesMinigame()
		if r.sessions != 0 {
			t.Fatal("unrelated session changed occupancy")
		}
	}
}

func TestMezFesRotationConcurrentLogins(t *testing.T) {
	draws := 0
	r := &MezFesRotation{choose: func() uint16 { draws++; return 4 }}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if game, _ := r.acquire(); game != 4 {
				t.Error("wrong game")
			}
		}()
	}
	wg.Wait()
	if r.sessions != 100 || draws != 1 {
		t.Fatal("simultaneous logins rerolled")
	}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.release() }()
	}
	wg.Wait()
	if r.sessions != 0 || r.game != 0 {
		t.Fatal("concurrent logout did not empty world")
	}
}

func TestMezFesRotationWorldIsolationAndLegacyFallback(t *testing.T) {
	a := &MezFesRotation{choose: func() uint16 { return 2 }}
	b := &MezFesRotation{choose: func() uint16 { return 4 }}
	a.acquire()
	b.acquire()
	a.release()
	if b.sessions != 1 || b.game != 4 {
		t.Fatal("another world's logout reset the occupied world")
	}
	c := &cfg.Config{}
	s := &Session{server: &Server{erupeConfig: c}}
	if s.effectiveMezFesMinigame() != 4 {
		t.Fatal("default fallback changed")
	}
	c.GameplayOptions.MezFesSwitchMinigame = true
	if s.effectiveMezFesMinigame() != 2 {
		t.Fatal("fixed switch fallback changed")
	}
}
