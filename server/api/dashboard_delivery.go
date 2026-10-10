package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"go.uber.org/zap"
)

const (
	dashboardDeliveryMailLimit = 32 // Matches MailRepository.GetListForCharacter.
	dashboardDeliveryStackSize = 99 // Conservative attachment size, not pouch max.
	dashboardDeliveryMaxAmount = dashboardDeliveryMailLimit * dashboardDeliveryStackSize
)

var dashboardDeliveryUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type dashboardDeliveryCharacter struct {
	ID   uint32 `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
	Code string `json:"code"`
}

type dashboardDeliveryRequest struct {
	RequestID     string `json:"requestId"`
	CharacterID   uint32 `json:"characterId"`
	CharacterName string `json:"characterName"`
	CharacterCode string `json:"characterCode"`
	ItemID        uint16 `json:"itemId"`
	ItemName      string `json:"itemName"`
	Quantity      int    `json:"quantity"`
}

type dashboardDeliveryReceipt struct {
	RequestID     string        `json:"requestId" db:"request_id"`
	CharacterID   uint32        `json:"characterId" db:"character_id"`
	CharacterName string        `json:"characterName" db:"character_name"`
	CharacterCode string        `json:"characterCode" db:"character_code"`
	ItemID        uint16        `json:"itemId" db:"item_id"`
	ItemName      string        `json:"itemName" db:"item_name"`
	Quantity      int           `json:"quantity" db:"quantity"`
	MailIDs       pq.Int64Array `json:"-" db:"mail_ids"`
	CreatedAt     time.Time     `json:"createdAt" db:"created_at"`
	MailCount     int           `json:"mailCount"`
	Repeated      bool          `json:"alreadyDelivered"`
}

type dashboardDeliveryError struct {
	status  int
	message string
}

func (e *dashboardDeliveryError) Error() string { return e.message }

func (s *APIServer) registerDashboardDeliveryRoutes(r *mux.Router) {
	r.HandleFunc("/api/dashboard/delivery/characters", s.DashboardDeliveryCharacters).Methods(http.MethodGet)
	r.HandleFunc("/api/dashboard/delivery/items", s.DashboardDeliveryItems).Methods(http.MethodGet)
	r.HandleFunc("/api/dashboard/delivery", s.DashboardDelivery).Methods(http.MethodPost)
}

func (s *APIServer) SetDeliveryMailNotifier(notifier func(uint32)) {
	s.chatMu.Lock()
	s.deliveryMailNotifier = notifier
	s.chatMu.Unlock()
}

func (s *APIServer) authorizeDashboardDelivery(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if !s.dashboardOperatorAuthorized(r) {
		writeDashboardChatError(w, http.StatusForbidden, "운영자 권한이 필요합니다.")
		return false
	}
	return true
}

// Hunter IDs use little-endian base 32 and omit 0/I/O/S, not ordinary base 36.
func dashboardCharacterCode(id uint32) string {
	if id == 0 || id >= 1<<30 {
		return ""
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRTUVWXYZ"
	code := make([]byte, 6)
	for i := range code {
		code[i], id = alphabet[id&31], id>>5
	}
	return string(code)
}

func (s *APIServer) DashboardDeliveryCharacters(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeDashboardDelivery(w, r) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(query) > 40 {
		writeDashboardChatError(w, http.StatusBadRequest, "캐릭터 검색어가 너무 깁니다.")
		return
	}
	characters := make([]dashboardDeliveryCharacter, 0, 20)
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"characters": characters})
		return
	}
	if s.db == nil {
		writeDashboardChatError(w, http.StatusServiceUnavailable, "데이터베이스에 연결되지 않았습니다.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// Treat LIKE metacharacters as literal name characters, never wildcard input.
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	if err := s.db.SelectContext(ctx, &characters, `SELECT id,name FROM characters
		WHERE deleted=false AND is_new_character=false AND name IS NOT NULL AND name<>''
		AND id>0 AND id<1073741824 AND name ILIKE $1 ESCAPE '\'
		ORDER BY (lower(name)=lower($2)) DESC,name,id LIMIT 20`, "%"+escaped+"%", query); err != nil {
		s.deliveryFailure(w, err)
		return
	}
	for i := range characters {
		characters[i].Code = dashboardCharacterCode(characters[i].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"characters": characters})
}

func (s *APIServer) DashboardDeliveryItems(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeDashboardDelivery(w, r) {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(query) > 160 {
		writeDashboardChatError(w, http.StatusBadRequest, "아이템 검색어가 너무 깁니다.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, loadedAt, err := s.deliveryCatalog.load(ctx, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		s.deliveryFailure(w, &dashboardDeliveryError{http.StatusBadGateway, "Ferias 아이템 목록을 불러오지 못했습니다. 잠시 후 다시 시도해 주세요."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": searchDashboardDeliveryItems(items, query), "updatedAt": loadedAt,
		"source": dashboardFeriasPageURL, "total": len(items), "maxQuantity": dashboardDeliveryMaxAmount,
	})
}

func validateDashboardDeliveryRequest(request dashboardDeliveryRequest) error {
	if !dashboardDeliveryUUID.MatchString(request.RequestID) || request.CharacterID == 0 ||
		request.CharacterCode == "" || request.CharacterCode != dashboardCharacterCode(request.CharacterID) || request.CharacterName == "" ||
		utf8.RuneCountInString(request.CharacterName) > 40 || request.ItemID == 0 || request.ItemName == "" ||
		utf8.RuneCountInString(request.ItemName) > 160 || request.Quantity < 1 || request.Quantity > dashboardDeliveryMaxAmount {
		return &dashboardDeliveryError{http.StatusBadRequest, "캐릭터와 아이템을 후보에서 선택하고 수량을 1~3168개로 입력해 주세요."}
	}
	return nil
}

func (s *APIServer) DashboardDelivery(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeDashboardDelivery(w, r) {
		return
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	origin, originErr := url.Parse(r.Header.Get("Origin"))
	if mediaType != "application/json" || (r.Header.Get("Origin") != "" &&
		(originErr != nil || (origin.Scheme != "http" && origin.Scheme != "https") || !strings.EqualFold(origin.Host, r.Host))) ||
		r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeDashboardChatError(w, http.StatusForbidden, "같은 운영자 화면에서만 지급할 수 있습니다.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request dashboardDeliveryRequest
	if err := decoder.Decode(&request); err != nil {
		writeDashboardChatError(w, http.StatusBadRequest, "지급 요청 형식이 올바르지 않습니다.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeDashboardChatError(w, http.StatusBadRequest, "지급 요청에는 하나의 JSON 객체만 사용할 수 있습니다.")
		return
	}
	request.RequestID = strings.ToLower(request.RequestID)
	if err := validateDashboardDeliveryRequest(request); err != nil {
		s.deliveryFailure(w, err)
		return
	}
	if s.db == nil {
		s.deliveryFailure(w, &dashboardDeliveryError{http.StatusServiceUnavailable, "데이터베이스에 연결되지 않았습니다."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// Retries remain successful even if the translation changed or Ferias is down.
	if receipt, err := readDashboardDelivery(ctx, s.db, request.RequestID); err == nil {
		if err = matchDashboardDelivery(receipt, request); err != nil {
			s.deliveryFailure(w, err)
			return
		}
		receipt.Repeated, receipt.MailCount = true, len(receipt.MailIDs)
		writeJSON(w, http.StatusOK, receipt)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.deliveryFailure(w, err)
		return
	}
	items, _, err := s.deliveryCatalog.load(ctx, false)
	if err != nil {
		s.deliveryFailure(w, &dashboardDeliveryError{http.StatusBadGateway, "Ferias 목록을 확인할 수 없어 지급하지 않았습니다. 목록을 다시 불러와 주세요."})
		return
	}
	matched := false
	for _, item := range items {
		if item.ID == request.ItemID && item.Name == request.ItemName {
			matched = true
			break
		}
	}
	if !matched {
		s.deliveryFailure(w, &dashboardDeliveryError{http.StatusConflict, "아이템이 없거나 번역명이 변경됐습니다. 목록을 갱신하고 다시 선택해 주세요."})
		return
	}
	receipt, err := s.deliverDashboardItem(ctx, request)
	if err != nil {
		s.deliveryFailure(w, err)
		return
	}
	if !receipt.Repeated {
		if s.logger != nil {
			s.logger.Info("Dashboard operator item delivery", zap.String("requestID", receipt.RequestID),
				zap.Uint32("characterID", receipt.CharacterID), zap.String("characterName", receipt.CharacterName),
				zap.Uint16("itemID", receipt.ItemID), zap.String("itemName", receipt.ItemName), zap.Int("quantity", receipt.Quantity))
		}
		s.chatMu.RLock()
		notifier := s.deliveryMailNotifier
		s.chatMu.RUnlock()
		if notifier != nil {
			notifier(receipt.CharacterID)
		}
	}
	status := http.StatusCreated
	if receipt.Repeated {
		status = http.StatusOK
	}
	writeJSON(w, status, receipt)
}

const dashboardDeliveryReceiptQuery = `SELECT request_id::text,character_id,character_name,character_code,
	item_id,item_name,quantity,mail_ids,created_at FROM dashboard_item_deliveries WHERE request_id=$1::uuid`

func readDashboardDelivery(ctx context.Context, db sqlx.QueryerContext, requestID string) (dashboardDeliveryReceipt, error) {
	var receipt dashboardDeliveryReceipt
	err := sqlx.GetContext(ctx, db, &receipt, dashboardDeliveryReceiptQuery, requestID)
	return receipt, err
}

func matchDashboardDelivery(receipt dashboardDeliveryReceipt, request dashboardDeliveryRequest) error {
	if receipt.CharacterID != request.CharacterID || receipt.CharacterName != request.CharacterName ||
		receipt.CharacterCode != request.CharacterCode || receipt.ItemID != request.ItemID ||
		receipt.ItemName != request.ItemName || receipt.Quantity != request.Quantity {
		return &dashboardDeliveryError{http.StatusConflict, "이미 사용된 지급 요청입니다. 새 지급 요청을 만들어 주세요."}
	}
	return nil
}

func (s *APIServer) deliverDashboardItem(ctx context.Context, request dashboardDeliveryRequest) (dashboardDeliveryReceipt, error) {
	var receipt dashboardDeliveryReceipt
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback()
	// Serialize the same request across tabs/processes before any mail is added.
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(request.RequestID))
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(hash.Sum64())); err != nil {
		return receipt, err
	}
	if existing, lookupErr := readDashboardDelivery(ctx, tx, request.RequestID); lookupErr == nil {
		if err = matchDashboardDelivery(existing, request); err != nil {
			return receipt, err
		}
		existing.Repeated, existing.MailCount = true, len(existing.MailIDs)
		return existing, nil
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return receipt, lookupErr
	}
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM characters WHERE id=$1 AND deleted=false
		AND is_new_character=false FOR UPDATE`, request.CharacterID).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return receipt, &dashboardDeliveryError{http.StatusNotFound, "캐릭터가 없거나 삭제됐습니다. 다시 검색해 주세요."}
	} else if err != nil {
		return receipt, err
	}
	if name != request.CharacterName {
		return receipt, &dashboardDeliveryError{http.StatusConflict, "캐릭터 이름이 변경됐습니다. 다시 검색해 주세요."}
	}
	var mailCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail WHERE recipient_id=$1 AND deleted=false`, request.CharacterID).Scan(&mailCount); err != nil {
		return receipt, err
	}
	needed := (request.Quantity + dashboardDeliveryStackSize - 1) / dashboardDeliveryStackSize
	if mailCount+needed > dashboardDeliveryMailLimit {
		return receipt, &dashboardDeliveryError{http.StatusConflict, fmt.Sprintf("메일함 공간이 부족합니다. %d통이 필요하고 현재 %d통이 있습니다. 기존 메일을 정리한 뒤 다시 지급해 주세요.", needed, mailCount)}
	}
	receipt = dashboardDeliveryReceipt{RequestID: request.RequestID, CharacterID: request.CharacterID,
		CharacterName: name, CharacterCode: request.CharacterCode, ItemID: request.ItemID,
		ItemName: request.ItemName, Quantity: request.Quantity, MailIDs: make(pq.Int64Array, 0, needed), MailCount: needed}
	for remaining := request.Quantity; remaining > 0; {
		amount := min(remaining, dashboardDeliveryStackSize)
		body := fmt.Sprintf("운영자가 지급한 아이템입니다.\n%s\n아이템 ID: %d (0x%04X)\n수량: %d개\n첨부 아이템을 수령해 주세요.", request.ItemName, request.ItemID, request.ItemID, amount)
		var mailID int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO mail
			(sender_id,recipient_id,subject,body,attached_item,attached_item_amount,is_guild_invite,is_sys_message)
			VALUES (NULL,$1,'아이템 지급',$2,$3,$4,false,true) RETURNING id`, request.CharacterID, body, request.ItemID, amount).Scan(&mailID); err != nil {
			return dashboardDeliveryReceipt{}, err
		}
		receipt.MailIDs = append(receipt.MailIDs, mailID)
		remaining -= amount
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO dashboard_item_deliveries
		(request_id,character_id,character_name,character_code,item_id,item_name,quantity,mail_ids)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, receipt.RequestID,
		receipt.CharacterID, receipt.CharacterName, receipt.CharacterCode, receipt.ItemID,
		receipt.ItemName, receipt.Quantity, receipt.MailIDs).Scan(&receipt.CreatedAt); err != nil {
		return dashboardDeliveryReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return dashboardDeliveryReceipt{}, err
	}
	return receipt, nil
}

func (s *APIServer) deliveryFailure(w http.ResponseWriter, err error) {
	var expected *dashboardDeliveryError
	if errors.As(err, &expected) {
		writeDashboardChatError(w, expected.status, expected.message)
		return
	}
	if s.logger != nil {
		s.logger.Error("Dashboard item delivery failed", zap.Error(err))
	}
	writeDashboardChatError(w, http.StatusServiceUnavailable, "처리 결과를 확인하지 못했습니다. 같은 요청으로 다시 시도해 주세요.")
}
