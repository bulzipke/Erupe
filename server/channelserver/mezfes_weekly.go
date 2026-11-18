package channelserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

const mezWeek = 7 * 24 * time.Hour
const mezWeeklyRequest uint32 = 0xff4d0000
const mezWalletCap = 9_999_999
const mezMaxWeeklyGP = 11_000

var errMezWalletFull = errors.New("insufficient room for the full MezFes reward")

func mezWeekStart(now time.Time) time.Time {
	local := now.In(time.FixedZone("KST", 9*60*60))
	noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, local.Location())
	monday := noon.AddDate(0, 0, -(int(noon.Weekday())+6)%7)
	if local.Before(monday) {
		return monday.Add(-mezWeek)
	}
	return monday
}

// Multiple channels can reach noon together. The winning insert freezes all
// seven games, including empty games, before other transactions see the round.
func settleMezWeek(ctx context.Context, tx *sqlx.Tx, start time.Time) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO mezfes_settlements(week_start)
		SELECT $1::timestamptz WHERE NOT EXISTS (SELECT 1 FROM mezfes_settlements WHERE week_start=$1)
		ON CONFLICT DO NOTHING`, start)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	// Include scoring transactions that were already writing at the cutoff.
	if _, err = tx.ExecContext(ctx, "LOCK TABLE mezfes_plays IN SHARE MODE"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mezfes_weekly_awards
		(week_start,character_id,stage_id,place,gp)
		SELECT $1,character_id,stage_id,place,gp FROM mezfes_weekly_standings($1,$2)`, start, start.Add(mezWeek))
	return err
}

type mezAward struct {
	Stage uint16 `db:"stage_id"`
	Place int    `db:"place"`
	GP    int    `db:"gp"`
}

func mezAwardNotice(rows []mezAward) ([]string, int) {
	var total int
	var lines []string
	for _, stage := range [...]uint16{465, 463, 464, 467, 469, 466, 468} {
		for _, r := range rows {
			if r.Stage != stage {
				continue
			}
			total += r.GP
			name := MezFesGameNameKO(stage)
			place := "참가상"
			if r.Place >= 1 && r.Place <= 3 {
				place = fmt.Sprintf("%d위", r.Place)
			}
			marker := "▷"
			if len(lines)%2 == 1 {
				marker = "▶"
			}
			lines = append(lines, fmt.Sprintf(" %s %s%s%s: %d", marker, name,
				strings.Repeat(" ", 18-len(stringsupport.UTF8ToSJIS(name))), place, r.GP))
		}
	}
	if total == 0 {
		return nil, 0
	}
	return append([]string{fmt.Sprintf("메제포르타 페스타 참여로 길드 공헌도를 %d 받았습니다.", total)}, lines...), total
}

// Only the round that ended at the most recent Monday noon is payable.
// Both credit and receipt survive a lost response or simultaneous channel move.
func (s *Session) creditMezWeek(ctx context.Context, now time.Time, room int) (int, []mezAward, error) {
	tx, err := s.server.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	start := mezWeekStart(now).Add(-mezWeek)
	if err = settleMezWeek(ctx, tx, start); err != nil {
		return 0, nil, err
	}
	var balance int
	if err = tx.GetContext(ctx, &balance, `SELECT COALESCE(gcp,0) FROM characters
		WHERE id=$1 AND user_id=$2 AND NOT deleted FOR UPDATE`, s.charID, s.userID); err != nil {
		return 0, nil, err
	}
	var rows []mezAward
	if err = tx.SelectContext(ctx, &rows, `SELECT stage_id, COALESCE(place,0) AS place,gp FROM mezfes_weekly_awards
		WHERE character_id=$1 AND week_start=$2 AND paid_at IS NULL
		AND clock_timestamp() < $3`,
		s.charID, start, start.Add(2*mezWeek)); err != nil {
		return 0, nil, err
	}
	if len(rows) != 0 {
		var total int
		for _, r := range rows {
			total += r.GP
		}
		if total > room || total > mezWalletCap-balance {
			if err = tx.Commit(); err != nil {
				return 0, nil, err
			}
			return balance, nil, errMezWalletFull
		}
		res, err := tx.ExecContext(ctx, `UPDATE mezfes_weekly_awards SET paid_at=clock_timestamp()
			WHERE character_id=$1 AND week_start=$2 AND paid_at IS NULL AND clock_timestamp() < $3`,
			s.charID, start, start.Add(2*mezWeek))
		if err != nil {
			return 0, nil, err
		}
		n, err := res.RowsAffected()
		if err != nil || n != int64(len(rows)) {
			// The round can expire between selection and credit.
			return balance, nil, err
		}
		if err = tx.GetContext(ctx, &balance, `UPDATE characters SET gcp=COALESCE(gcp,0)+$2
			WHERE id=$1 RETURNING gcp`, s.charID, total); err != nil {
			return 0, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	return balance, rows, nil
}

func (s *Server) runMezSettlements() {
	for {
		now := TimeAdjusted()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		tx, err := s.db.BeginTxx(ctx, nil)
		if err == nil {
			err = settleMezWeek(ctx, tx, mezWeekStart(now).Add(-mezWeek))
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		cancel()
		next := mezWeekStart(now).Add(mezWeek)
		if err != nil {
			s.logger.Warn("MezFes weekly settlement failed", zap.Error(err))
			next = time.Now().Add(time.Minute)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.done:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// The initial balance can arrive before the first stage enables chat.
func (s *Session) sendMezLoginNotice() {
	if s.mezChatReady && len(s.mezLoginNotice) != 0 {
		sendServerChatMessages(s, s.mezLoginNotice)
		s.mezLoginNotice = nil
	}
}

// Reuse the buffered request used by other custom in-game listings. A
// read-only booth request does not consume a reward.
func handleMezWeekly(s *Session, p *mhfpacket.MsgMhfEnumerateItem) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := byteframe.NewByteFrame()
	op := uint16(p.CampaignID)
	now := TimeAdjusted()
	start := mezWeekStart(now)
	b.WriteUint32(0x4d5a5731) // MZW1
	b.WriteUint32(uint32(start.Add(mezWeek).Unix() - now.Unix()))
	var err error
	switch {
	case op >= 463 && op <= 469:
		var rows []struct {
			Name             string
			Score, Place, GP uint32
		}
		err = s.server.db.SelectContext(ctx, &rows, `SELECT hunter_name AS name,score,place,gp
			FROM mezfes_weekly_standings($1,$2) WHERE stage_id=$3 AND place<=3 ORDER BY place`,
			start, start.Add(mezWeek), op)
		b.WriteUint32(uint32(len(rows)))
		for _, r := range rows {
			b.WriteUint32(r.Place)
			b.WriteUint32(r.Score)
			b.WriteUint32(r.GP)
			b.WriteBytes(stringsupport.PaddedString(r.Name, 32, true))
		}
		if MezFesGameIsGroup(op) {
			b.WriteUint32(500)
		} else {
			b.WriteUint32(100)
		}
	case op >= 0x8000 && op <= 0x8000+mezMaxWeeklyGP && s.mezWalletLoaded:
		if s.mezUnseenGP == 0 {
			var rows []mezAward
			_, rows, err = s.creditMezWeek(ctx, now, int(op-0x8000))
			if errors.Is(err, errMezWalletFull) {
				// Allow a full wallet to collect as soon as spending makes space.
				b = byteframe.NewByteFrame()
				b.WriteUint32(0x4d5a5731)
				b.WriteUint32(5)
				err = nil
			}
			if err == nil {
				s.mezNotice, s.mezUnseenGP = mezAwardNotice(rows)
			}
		}
		b.WriteUint32(uint32(s.mezUnseenGP))
	case op == 1 && s.mezWalletLoaded:
		s.mezUnseenGP = 0
		b.WriteUint32(0)
	default:
		err = fmt.Errorf("invalid MezFes weekly request")
	}
	if err != nil {
		doAckBufFail(s, p.AckHandle, nil)
		return
	}
	doAckBufSucceed(s, p.AckHandle, b.Data())
	if op == 1 && len(s.mezNotice) > 0 {
		sendServerChatMessages(s, s.mezNotice)
		s.mezNotice = nil
	}
}
