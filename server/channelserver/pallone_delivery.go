package channelserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"erupe-ce/common/mhfpallone"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

// This first publication profile is deliberately limited to reconstructed 001.
// An operator installs it together with the matching stage/client candidates,
// and separately selects a registered event. Merely building the bundle does
// neither. A profile never authorizes score credit or manufactures a receipt.
type palloneDeliveryProfile struct {
	Kind       string                     `json:"kind"`
	Key        int32                      `json:"native_route_key"`
	OverallKey int32                      `json:"overall_ranking_key"`
	Route      mhfpallone.Route           `json:"route"`
	Rows       []mhfpallone.RouteDelivery `json:"delivery_rows"`
	Hashes     map[string]string          `json:"files_sha256"`
}

func (s *Session) handlePalloneRouteDelivery(pkt *mhfpacket.MsgMhfGetWeeklySeibatuRankingReward) {
	if pkt.Unk0 != 0 || pkt.Unk2 != 0 || pkt.Unk3 != 0 {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	e, err := s.server.palloneEvent(TimeAdjusted())
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	if e == nil {
		s.observePallone("delivery_empty", zap.String("reason", "no_visible_selected_event"))
		doAckPalloneEmpty(s, pkt.AckHandle)
		return
	}
	rows, err := s.server.palloneDeliveryRows(e.ID)
	if err != nil {
		s.logger.Warn("Pallone route publication refused", zap.Error(err))
		s.observePallone("delivery_refused", zap.Int32("eventID", e.ID))
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	data, err := mhfpallone.EncodeRouteDelivery(e.ID, rows)
	if err != nil {
		doAckBufFail(s, pkt.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, data)
	s.observePallone("delivery_reply_queued", zap.Int32("eventID", e.ID), zap.Int("routeCount", len(rows)))
}

func (s *Server) palloneDeliveryRows(eventID int32) ([]mhfpallone.RouteDelivery, error) {
	profile, err := loadPallonePublication(s.erupeConfig.BinPath)
	if err != nil || profile == nil {
		return nil, err
	}
	routes, err := s.palloneRepo.GetPalloneRoutes(eventID)
	if err != nil {
		return nil, err
	}
	if len(routes) != 1 || routes[0].Key != profile.Key || routes[0].Star != 0 || !reflect.DeepEqual(routes[0].QuestIDs, profile.Route.StageQuestIDs) {
		return nil, fmt.Errorf("Pallone registered routes do not match the installed client profile")
	}
	return profile.Rows, nil
}

// Rotation and delivery share the same publication checks. Merely enabling
// rotation without an installed, verified route must not create an event.
func loadPallonePublication(root string) (*palloneDeliveryProfile, error) {
	data, err := os.ReadFile(filepath.Join(root, "pallone-client-profile.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var profile palloneDeliveryProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	ids := []uint16{27438, 27479, 27348, 27365}
	wantRows := []mhfpallone.RouteDelivery{{Star: 0, Ordinal: uint16(profile.Key)}}
	if profile.Kind != "offline_route_001_client_candidate" || profile.Key <= 0 || profile.Key >= mhfpallone.MaxRoutes || profile.OverallKey != 0 ||
		profile.Route.Index != int(profile.Key) || profile.Route.Star != 0 || profile.Route.Number != 1 || profile.Route.NativeCode != 10001 ||
		!reflect.DeepEqual(profile.Route.StageQuestIDs, ids) || !reflect.DeepEqual(profile.Rows, wantRows) || len(profile.Hashes) != 26 {
		return nil, fmt.Errorf("Pallone route-001 client profile is incompatible")
	}
	for _, name := range []string{"client/dat/mhfdat.bin", "client/dat/mhfinf.bin"} {
		if raw, err := hex.DecodeString(profile.Hashes[name]); err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("Pallone profile has no client data fingerprint")
		}
	}
	for _, id := range ids {
		for _, suffix := range []string{"d0", "d1", "d2", "n0", "n1", "n2"} {
			name := fmt.Sprintf("%05d%s.bin", id, suffix)
			expected := profile.Hashes["server/quests/"+name]
			body, err := os.ReadFile(filepath.Join(root, "quests", name))
			if err != nil {
				return nil, fmt.Errorf("Pallone stage %s unavailable: %w", name, err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(body)) != expected {
				return nil, fmt.Errorf("Pallone stage %s differs from the client profile", name)
			}
			info, err := mhfpallone.InspectQuestTemplate(body)
			if err != nil || info.EmbeddedID != id || !info.HasGeometry {
				return nil, fmt.Errorf("Pallone stage %s has invalid identity/geometry", name)
			}
		}
	}
	return &profile, nil
}
