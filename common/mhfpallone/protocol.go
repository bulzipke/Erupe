package mhfpallone

import (
	"errors"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
)

const (
	MaxRankingRows       = 100
	MaxGuildContributors = 5
)

// Score is a native RESPONSE_CARAVAN_MY_SCORE row. Best is the base score,
// not a clear count; Premium contributes only to reward-progress totals.
type Score struct {
	RouteKey int32 `db:"route_key"`
	Total    int32 `db:"total_score"`
	Best     int32 `db:"best_score"`
	Premium  int32 `db:"premium_score"`
}

type RankScore struct {
	Key   int32  `db:"route_key"`
	Rank  uint32 `db:"rank"`
	Score int32  `db:"score"`
}

type PersonalRanking struct {
	Score int32  `db:"score"`
	Name  string `db:"name"`
}

// The score is FIRST. FUN_103860d6 reads the second word as the guild ID
// before requesting selector 5; FUN_10390440 displays the first as points.
type GuildRanking struct {
	Score   int32  `db:"score"`
	GuildID uint32 `db:"guild_id"`
	Name    string `db:"name"`
}

func responseHeader(eventID int32, count, capacity int) (*byteframe.ByteFrame, error) {
	if count > capacity {
		return nil, fmt.Errorf("pallone: %d rows exceed native capacity %d", count, capacity)
	}
	bf := byteframe.NewByteFrame()
	bf.WriteInt32(eventID)
	bf.WriteUint32(0)
	bf.WriteUint32(0)
	bf.WriteUint32(uint32(count))
	return bf, nil
}

func EncodeScores(eventID int32, rows []Score) ([]byte, error) {
	bf, err := responseHeader(eventID, len(rows), MaxRoutes)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		bf.WriteInt32(row.RouteKey)
		bf.WriteInt32(row.Total)
		bf.WriteInt32(row.Best)
		bf.WriteInt32(row.Premium)
	}
	return bf.Data(), bf.Err()
}

func EncodeRanks(eventID int32, rows []RankScore) ([]byte, error) {
	bf, err := responseHeader(eventID, len(rows), MaxRoutes)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		bf.WriteInt32(row.Key)
		bf.WriteUint32(row.Rank)
		bf.WriteInt32(row.Score)
	}
	return bf.Data(), bf.Err()
}

func EncodePersonalRanking(eventID int32, rows []PersonalRanking, capacity int) ([]byte, error) {
	if capacity != MaxRankingRows && capacity != MaxGuildContributors {
		return nil, errors.New("pallone: unsupported personal ranking capacity")
	}
	bf, err := responseHeader(eventID, len(rows), capacity)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		bf.WriteInt32(row.Score)
		bf.WriteBytes(stringsupport.PaddedString(row.Name, 14, true))
	}
	return bf.Data(), bf.Err()
}

func EncodeGuildRanking(eventID int32, rows []GuildRanking) ([]byte, error) {
	bf, err := responseHeader(eventID, len(rows), MaxRankingRows)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		bf.WriteInt32(row.Score)
		bf.WriteUint32(row.GuildID)
		bf.WriteBytes(stringsupport.PaddedString(row.Name, 26, true))
	}
	return bf.Data(), bf.Err()
}
