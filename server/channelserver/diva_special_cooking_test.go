package channelserver

import (
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// The special hall doubles the client's great-success gauge in 107f5310.
// REGIST_GUILD_COOKING already contains the final result: applying a second
// server-side boost would turn the native easier minigame into guaranteed food.
func TestDivaSpecialCookingPreservesNativeResult(t *testing.T) {
	for _, overwrite := range []uint32{0, 31} {
		for success := uint8(0); success < 4; success++ {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.GameplayOptions.ClanMealDuration = 3600
			g := &mockGuildRepo{
				guild:         &Guild{ID: 10, RankRP: 0},
				membership:    &GuildMember{GuildID: 10, CharID: 7},
				createdMealID: 41,
			}
			srv.guildRepo = g
			s := createMockSession(7, srv)
			before := TimeAdjusted().Unix()
			handleMsgMhfRegistGuildCooking(s, &mhfpacket.MsgMhfRegistGuildCooking{
				AckHandle: 1, OverwriteID: overwrite, MealID: 5, Success: success,
			})
			after := TimeAdjusted().Unix()
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(ack.Payload) != 18 {
				t.Fatalf("overwrite=%d result=%d: ACK=%+v", overwrite, success, ack)
			}
			bf := byteframe.NewByteFrameFromBytes(ack.Payload)
			wantID := overwrite
			if wantID == 0 {
				wantID = 41
			}
			if bf.ReadUint16() != 1 || bf.ReadUint32() != wantID ||
				bf.ReadUint32() != 5 || bf.ReadUint32() != uint32(success) {
				t.Fatalf("result changed: %x", ack.Payload)
			}
			at := int64(bf.ReadUint32())
			if bf.Err() != nil || at < before || at > after {
				t.Fatalf("unexpected creation timestamp: %d (%d..%d)", at, before, after)
			}
			if len(g.updateMealArgs) != 5 || g.updateMealArgs[0] != 10 ||
				g.updateMealArgs[1] != 7 || g.updateMealArgs[2] != overwrite ||
				g.updateMealArgs[3] != 5 || g.updateMealArgs[4] != uint32(success) {
				t.Fatalf("result or guild ownership changed: %v", g.updateMealArgs)
			}
		}
	}
}

func TestDivaSpecialCookingReopeningDoesNotRefreshMeals(t *testing.T) {
	now := TimeAdjusted().Truncate(time.Second)
	activeAt := now.Add(-59 * time.Minute)
	srv := createMockServer()
	srv.guildRepo = &mockGuildRepo{
		guild: &Guild{ID: 10},
		meals: []*GuildMeal{
			{ID: 1, MealID: 5, Level: 3, CreatedAt: activeAt},
			{ID: 2, MealID: 6, Level: 1, CreatedAt: now.Add(-61 * time.Minute)},
		},
	}
	s := createMockSession(7, srv)
	for range 2 {
		handleMsgMhfLoadGuildCooking(s, &mhfpacket.MsgMhfLoadGuildCooking{AckHandle: 1})
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || len(ack.Payload) != 18 {
			t.Fatalf("unexpected meal list: %+v", ack)
		}
		bf := byteframe.NewByteFrameFromBytes(ack.Payload)
		if bf.ReadUint16() != 1 || bf.ReadUint32() != 1 || bf.ReadUint32() != 5 ||
			bf.ReadUint32() != 3 || bf.ReadUint32() != uint32(activeAt.Unix()) || bf.Err() != nil {
			t.Fatalf("reopening changed meal or expiry: %x", ack.Payload)
		}
	}
}

func TestDivaSpecialCookingPreservesConfiguredStorageDuration(t *testing.T) {
	for _, seconds := range []int{1800, 3600, 7200} {
		srv := createMockServer()
		srv.erupeConfig.GameplayOptions.ClanMealDuration = seconds
		srv.guildRepo = &mockGuildRepo{
			membership: &GuildMember{GuildID: 10, CharID: 7}, createdMealID: 41,
		}
		s := createMockSession(7, srv)
		before := TimeAdjusted().Unix()
		handleMsgMhfRegistGuildCooking(s, &mhfpacket.MsgMhfRegistGuildCooking{AckHandle: 1, MealID: 5, Success: 2})
		after := TimeAdjusted().Unix()
		ack := readAck(t, s)
		if ack.ErrorCode != 0 || len(ack.Payload) != 18 {
			t.Fatalf("duration=%d: %+v", seconds, ack)
		}
		bf := byteframe.NewByteFrameFromBytes(ack.Payload[14:])
		expires := int64(bf.ReadUint32()) + 3600
		if bf.Err() != nil || expires < before+int64(seconds) || expires > after+int64(seconds) {
			t.Fatalf("duration=%d: expires=%d now=%d..%d", seconds, expires, before, after)
		}
	}
}
