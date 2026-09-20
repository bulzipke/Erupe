package channelserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

const (
	dailyCoinAmount     = 10
	dailyCoinRequiredMS = int64(time.Hour / time.Millisecond)
	dailyCoinCheckpoint = 15 * time.Second
)

// Real Korean calendar time, independent of debug overrides/container timezone.
var dailyCoinLocation = time.FixedZone("Asia/Seoul", 9*60*60)

type dailyCoinInterval [2]int64

type dailyCoinResult struct {
	Day      string
	First    bool
	Bonus    bool
	Complete bool
	OnlineMS int64
	Balance  int64 // Premium coin balance after the committed settlement.
}

func dailyCoinMidnight(t time.Time) time.Time {
	t = t.In(dailyCoinLocation)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, dailyCoinLocation)
}

// The union handles retries, overlapping sessions and out-of-order reports.
// Gaps (offline time) are never credited.
func mergeDailyCoinIntervals(spans []dailyCoinInterval, next dailyCoinInterval) ([]dailyCoinInterval, int64) {
	if next[1] > next[0] {
		spans = append(spans, next)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	merged := make([]dailyCoinInterval, 0, len(spans))
	for _, span := range spans {
		if len(merged) == 0 || span[0] > merged[len(merged)-1][1] {
			merged = append(merged, span)
		} else if span[1] > merged[len(merged)-1][1] {
			merged[len(merged)-1][1] = span[1]
		}
	}
	var total int64
	for _, span := range merged {
		total += span[1] - span[0]
	}
	return merged, min(total, dailyCoinRequiredMS)
}

// Claim flags, elapsed time and account balance commit together. includeEnd
// admits the next day at exactly midnight for live sessions, but not logout.
func settleDailyCoins(ctx context.Context, db *sqlx.DB, userID uint32, from, until time.Time, includeEnd bool) ([]dailyCoinResult, error) {
	if userID == 0 || until.Before(from) {
		return nil, fmt.Errorf("invalid daily coin interval")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize all channels/processes on the account, not just this Session.
	var balance int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(gacha_premium,0) FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&balance); err != nil {
		return nil, err
	}
	results := []dailyCoinResult{}
	award := 0
	for day := dailyCoinMidnight(from); day.Before(until) || (includeEnd && day.Equal(until)); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		_, err = tx.ExecContext(ctx, `INSERT INTO daily_gacha_coins (user_id, reward_day) VALUES ($1,$2) ON CONFLICT DO NOTHING`, userID, date)
		if err != nil {
			return nil, err
		}
		var first, bonus bool
		var online int64
		var raw []byte
		err = tx.QueryRowContext(ctx, `SELECT first_claimed, bonus_claimed, online_ms, intervals FROM daily_gacha_coins WHERE user_id=$1 AND reward_day=$2 FOR UPDATE`, userID, date).Scan(&first, &bonus, &online, &raw)
		if err != nil {
			return nil, err
		}
		result := dailyCoinResult{Day: date, First: !first}
		if !first {
			award += dailyCoinAmount
		}
		if !bonus {
			var spans []dailyCoinInterval
			if err = json.Unmarshal(raw, &spans); err != nil {
				return nil, err
			}
			start := max(from.UnixMilli(), day.UnixMilli()) - day.UnixMilli()
			end := min(until.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()) - day.UnixMilli()
			spans, online = mergeDailyCoinIntervals(spans, dailyCoinInterval{start, end})
			if online >= dailyCoinRequiredMS {
				bonus = true
				result.Bonus = true
				award += dailyCoinAmount
				spans = []dailyCoinInterval{} // No further time tracking needed today.
			}
			raw, err = json.Marshal(spans)
			if err != nil {
				return nil, err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE daily_gacha_coins SET first_claimed=true, bonus_claimed=$3, online_ms=$4, intervals=$5::jsonb WHERE user_id=$1 AND reward_day=$2`, userID, date, bonus, online, string(raw))
		if err != nil {
			return nil, err
		}
		result.Complete, result.OnlineMS = bonus, online
		results = append(results, result)
	}
	if award > 0 {
		// Overflow or any database failure rolls back the claims as well.
		err = tx.QueryRowContext(ctx, `UPDATE users SET gacha_premium=COALESCE(gacha_premium,0)+$2 WHERE id=$1 RETURNING gacha_premium`, userID, award).Scan(&balance)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for i := range results {
		results[i].Balance = balance
	}
	return results, nil
}

func dailyCoinMessages(result dailyCoinResult, entering bool) []string {
	var messages []string
	balanceMessage := fmt.Sprintf("뽑기 코인 보유: %d개", result.Balance)
	if result.First {
		messages = append(messages, "오늘의 첫 접속으로 뽑기 코인 10개를 획득했습니다!")
	}
	if result.Bonus {
		messages = append(messages, "뽑기 코인 10개를 추가 획득했습니다!")
	}
	if result.First || result.Bonus || entering {
		if !result.Complete && !result.Bonus {
			minutes := (dailyCoinRequiredMS - result.OnlineMS + 59999) / 60000
			messages = append(messages, fmt.Sprintf("%d분 더 접속하면 뽑기 코인 10개를 추가로 획득할 수 있습니다.", minutes))
		}
		messages = append(messages, balanceMessage)
	}
	return messages
}

// Start after authenticated stage entry packets are queued. The launcher and
// character-creation screen do not count as playing.
func (s *Session) startDailyCoins() {
	if s.server == nil || s.server.db == nil || s.server.erupeConfig == nil ||
		s.server.erupeConfig.GameplayOptions.DisableDailyGachaCoins || s.userID == 0 || s.done == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	if s.closed.Load() || s.dailyCoinsDone != nil {
		return
	}
	s.dailyCoinsDone = make(chan struct{})
	go s.runDailyCoins(s.userID, time.Now(), s.dailyCoinsDone)
}

func (s *Session) waitDailyCoins() {
	s.Lock()
	done := s.dailyCoinsDone
	s.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Session) runDailyCoins(userID uint32, from time.Time, done chan struct{}) {
	defer close(done)
	entering := true
	settle := func(until time.Time, live bool) (dailyCoinResult, bool) {
		if until.Before(from) {
			return dailyCoinResult{}, false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		results, err := settleDailyCoins(ctx, s.server.db, userID, from, until, live)
		if err != nil {
			s.logger.Error("Daily gacha coin settlement failed; interval retained for retry", zap.Uint32("userID", userID), zap.Error(err))
			return dailyCoinResult{}, false
		}
		from = until
		var latest dailyCoinResult
		for _, result := range results {
			latest = result
			if result.First || result.Bonus {
				s.logger.Info("Daily gacha coins granted", zap.Uint32("userID", userID), zap.String("day", result.Day), zap.Bool("first", result.First), zap.Bool("bonus", result.Bonus))
			}
			if live && !s.closed.Load() {
				isEntryNotice := entering && result.Day == dailyCoinMidnight(until).Format("2006-01-02")
				messages := dailyCoinMessages(result, isEntryNotice)
				if len(messages) > 0 {
					s.logger.Info("Daily gacha coin notice",
						zap.Uint32("userID", userID), zap.Uint32("charID", s.charID),
						zap.String("day", result.Day), zap.Bool("entering", isEntryNotice),
						zap.Bool("first", result.First), zap.Bool("bonus", result.Bonus),
						zap.Int64("balance", result.Balance), zap.Int("lines", len(messages)))
					sendServerChatMessages(s, messages)
				}
			}
		}
		entering = false
		return latest, true
	}
	// Allow initial stage rendering before the chat notification.
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.done:
			until := time.Unix(0, s.dailyCoinsClosedAt.Load())
			if _, ok := settle(until, false); !ok {
				settle(until, false) // Safe to replay even after an ambiguous COMMIT.
			}
			return
		case <-timer.C:
			now := time.Now()
			// Capture time before checking the close timestamp, so a racing
			// disconnect cannot credit the next day after the socket was closed.
			if s.dailyCoinsClosedAt.Load() != 0 {
				continue
			}
			result, ok := settle(now, true)
			wait := dailyCoinCheckpoint
			midnight := dailyCoinMidnight(now).AddDate(0, 0, 1)
			if ok && result.Complete {
				wait = time.Until(midnight)
			} else if ok {
				wait = min(wait, time.Duration(dailyCoinRequiredMS-result.OnlineMS)*time.Millisecond)
			}
			wait = min(wait, time.Until(midnight))
			timer.Reset(max(wait, time.Millisecond))
		}
	}
}
