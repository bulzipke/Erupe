package channelserver

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/mhfpallone"
	"erupe-ce/network/mhfpacket"
)

func installPalloneTestProfile(t *testing.T, s *Session, r *mockPalloneRepo) palloneDeliveryProfile {
	t.Helper()
	root := t.TempDir()
	s.server.erupeConfig.BinPath = root
	if err := os.Mkdir(filepath.Join(root, "quests"), 0750); err != nil {
		t.Fatal(err)
	}
	ids := []uint16{27438, 27479, 27348, 27365}
	r.routes = []PalloneRoute{{Key: 1, Star: 0, QuestIDs: ids}}
	p := palloneDeliveryProfile{
		Kind: "offline_route_001_client_candidate", Key: 1,
		Route:  mhfpallone.Route{Index: 1, Star: 0, Number: 1, NativeCode: 10001, StageQuestIDs: ids},
		Rows:   []mhfpallone.RouteDelivery{{Star: 0, Ordinal: 1}},
		Hashes: map[string]string{"client/dat/mhfdat.bin": strings.Repeat("a", 64), "client/dat/mhfinf.bin": strings.Repeat("b", 64)},
	}
	for _, id := range ids {
		body := make([]byte, 0x500)
		binary.LittleEndian.PutUint32(body, 0x100)
		binary.LittleEndian.PutUint32(body[0x14:], 0x490)
		binary.LittleEndian.PutUint16(body[0x12e:], id)
		for _, suffix := range []string{"d0", "d1", "d2", "n0", "n1", "n2"} {
			name := fmt.Sprintf("%05d%s.bin", id, suffix)
			if err := os.WriteFile(filepath.Join(root, "quests", name), body, 0600); err != nil {
				t.Fatal(err)
			}
			p.Hashes["server/quests/"+name] = fmt.Sprintf("%x", sha256.Sum256(body))
		}
	}
	writePalloneTestProfile(t, s, p)
	return p
}

func writePalloneTestProfile(t *testing.T, s *Session, p palloneDeliveryProfile) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.server.erupeConfig.BinPath, "pallone-client-profile.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPalloneDeliveryUsesVerifiedProfileAndEvent(t *testing.T) {
	for _, phase := range []string{"active", "claim"} {
		t.Run(phase, func(t *testing.T) {
			s, r := palloneHandlerSession()
			installPalloneTestProfile(t, s, r)
			if phase == "claim" {
				r.event.End = TimeAdjusted().Add(-time.Minute)
			}
			handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 73, Unk1: 3})
			a := readAck(t, s)
			want, err := mhfpallone.EncodeRouteDelivery(41, []mhfpallone.RouteDelivery{{Star: 0, Ordinal: 1}})
			if err != nil || a.ErrorCode != 0 || !a.IsBufferResponse || a.AckHandle != 73 || string(a.Payload) != string(want) {
				t.Fatalf("delivery response = %+v, expected %x", a, want)
			}
			if s.palloneReceipt != nil || r.settled != 0 {
				t.Fatal("listing a route manufactured a completion receipt or score")
			}
		})
	}
}

func TestPalloneDeliveryRefusesMismatchAndMissingVariant(t *testing.T) {
	for _, kind := range []string{"zero key", "wrong ordinal", "wrong stage order", "wrong registered key", "missing variant", "changed variant", "invalid identity", "invalid request"} {
		t.Run(kind, func(t *testing.T) {
			s, r := palloneHandlerSession()
			p := installPalloneTestProfile(t, s, r)
			request := &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 74, Unk1: 3}
			path := filepath.Join(s.server.erupeConfig.BinPath, "quests", "27438n2.bin")
			switch kind {
			case "zero key":
				p.Key = 0
			case "wrong ordinal":
				p.Rows[0].Ordinal = 2
			case "wrong stage order":
				p.Route.StageQuestIDs[0], p.Route.StageQuestIDs[1] = p.Route.StageQuestIDs[1], p.Route.StageQuestIDs[0]
			case "wrong registered key":
				r.routes[0].Key = 2
			case "missing variant":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed variant", "invalid identity":
				b := []byte("changed")
				if err := os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "invalid identity" {
					p.Hashes["server/quests/27438n2.bin"] = fmt.Sprintf("%x", sha256.Sum256(b))
				}
			case "invalid request":
				request.Unk2 = 1
			}
			writePalloneTestProfile(t, s, p)
			handleMsgMhfGetWeeklySeibatuRankingReward(s, request)
			if a := readAck(t, s); a.ErrorCode == 0 || !a.IsBufferResponse {
				t.Fatalf("accepted mismatched publication: %+v", a)
			}
		})
	}
}

func TestPalloneDeliveryWithoutInstalledProfileIsActuallyEmpty(t *testing.T) {
	for _, kind := range []string{"missing profile", "no event", "tower", "expired"} {
		t.Run(kind, func(t *testing.T) {
			s, r := palloneHandlerSession()
			s.server.erupeConfig.BinPath = t.TempDir()
			switch kind {
			case "no event":
				r.event = nil
			case "tower":
				s.server.erupeConfig.TowerRotation.Enabled = true
			case "expired":
				r.event.ClaimUntil = TimeAdjusted().Add(-time.Minute)
			}
			handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 75, Unk1: 3})
			a := readAck(t, s)
			if a.ErrorCode != 0 || len(a.Payload) != 16 || binary.BigEndian.Uint32(a.Payload[12:]) != 0 {
				t.Fatalf("unconfigured publication exposed a placeholder route: %+v", a)
			}
		})
	}
}

func TestPalloneDeliveryActualOfflineCandidate(t *testing.T) {
	root := os.Getenv("PALLONE_CLIENT_CANDIDATE")
	if root == "" {
		t.Skip("set PALLONE_CLIENT_CANDIDATE to the isolated combined bundle")
	}
	s, r := palloneHandlerSession()
	s.server.erupeConfig.BinPath = filepath.Join(root, "server")
	data, err := os.ReadFile(filepath.Join(root, "server", "pallone-client-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p palloneDeliveryProfile
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	r.routes = []PalloneRoute{{Key: p.Key, Star: 0, QuestIDs: []uint16{27438, 27479, 27348, 27365}}}
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 76, Unk1: 3})
	a := readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 40 || binary.BigEndian.Uint32(a.Payload[12:]) != 1 || binary.BigEndian.Uint32(a.Payload[32:]) != uint32(p.Key) {
		t.Fatalf("actual candidate did not publish its positive route ordinal: %+v", a)
	}
}
