package api

// 알림 정책·채널·발송 이력 API (docs/design/05-notifications.md 9.2).
//
// 허용 역할은 System Administrator + Security Administrator다 — 후자의 근거는
// 00-spec 7.11 "보안 관리자 = 정책 설정"이며, 알림 정책은 보안 정책의 하위다.
// 발송 이력 조회만 Auditor에도 열어 준다("왜 안 왔는지"의 답, 설계 14.3).
//
// 변경 3종(create/update/delete)은 store가 변경+감사를 단일 트랜잭션으로 처리하고
// 감사 INSERT를 트랜잭션의 마지막 문장에 둔다(감사 해시 체인 advisory lock 구간
// 최소화 — docs/GOTCHAS.md). 테스트 발송도 같은 규약으로 notification.test를 남긴다.
//
// 보안: GET /notification-channels 응답은 {channel, enabled, reason}뿐이다.
// 호스트·계정·비밀번호·웹훅 URL을 절대 싣지 않는다 (설계 9.3).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxNotificationRequestBytes는 정책 API 요청 본문 상한이다.
const maxNotificationRequestBytes = 256 << 10 // 256 KiB

// EventNotificationTest는 테스트 발송 감사 이벤트 타입이다 (설계 9.2).
const EventNotificationTest = "notification.test"

// 정책 입력 상한 — 형식 검증 단계에서 막아 DB까지 내려보내지 않는다.
const (
	maxPolicyNameLen    = 200
	maxPolicyThresholds = 50
	maxPolicyRecipients = 200
	maxThresholdDays    = 3650
	maxTestErrorRunes   = 300
)

// ChannelStatus는 GET /notification-channels 응답 원소다.
// **필드는 이 셋뿐이다** — 설정값(호스트·계정·URL)을 담을 자리가 타입에 없다.
// reason은 기동 경고 로그와 같은 문자열이며 secret을 담지 않는다 (설계 9.3·10.1).
type ChannelStatus struct {
	Channel string `json:"channel"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

// NotificationChannels는 채널 상태 조회와 테스트 발송 실행자다.
// 배선(cmd/server)이 config·notify로 만든 구현을 WithNotificationChannels로 주입한다.
// 주입되지 않아도 코어 API는 정상 동작한다 — 상태 조회는 전 채널을
// "배선되지 않음"으로 보고하고 테스트 발송은 503이다 (설계 10.1 실패 격리).
type NotificationChannels interface {
	// Status는 채널별 활성 여부와 사유를 반환한다. 사유에 secret을 담지 않는다.
	Status() []ChannelStatus
	// SendTest는 채널 하나로 테스트 알림 1건을 보낸다. recipient가 비면 구현이
	// 채널 기본 수신자를 고른다. 반환 에러 문자열에 수신자 주소·비밀값을 담지
	// 않는 것은 구현 책임이다 (설계 11.4) — 이 문자열은 응답·감사에 실린다.
	SendTest(ctx context.Context, channel, recipient string) error
}

// channelsNotWiredReason은 알림 배선이 없을 때의 사유다(전 채널 공통).
const channelsNotWiredReason = "notification dispatcher is not wired on this server"

// notifyChannelOrder는 응답 순서를 고정한다 (설계 1.2의 4채널).
var notifyChannelOrder = []string{
	store.NotifyChannelEmail,
	store.NotifyChannelWebhook,
	store.NotifyChannelInbox,
	store.NotifyChannelSyslog,
}

// ===== 정책 =====

// notificationPolicyRequest는 정책 생성 본문이다.
type notificationPolicyRequest struct {
	Name                string                        `json:"name"`
	Enabled             *bool                         `json:"enabled"`
	EventTypes          []string                      `json:"event_types"`
	Thresholds          []int                         `json:"thresholds"`
	Channels            []string                      `json:"channels"`
	Recipients          []store.NotificationRecipient `json:"recipients"`
	FilterEnvironments  []string                      `json:"filter_environments"`
	FilterDepartmentIDs []string                      `json:"filter_department_ids"`
}

// notificationPolicyPatchRequest는 부분 수정 본문이다.
// 키가 없으면 nil이라 그 필드는 바뀌지 않는다.
type notificationPolicyPatchRequest struct {
	Name                *string                        `json:"name"`
	Enabled             *bool                          `json:"enabled"`
	EventTypes          *[]string                      `json:"event_types"`
	Thresholds          *[]int                         `json:"thresholds"`
	Channels            *[]string                      `json:"channels"`
	Recipients          *[]store.NotificationRecipient `json:"recipients"`
	FilterEnvironments  *[]string                      `json:"filter_environments"`
	FilterDepartmentIDs *[]string                      `json:"filter_department_ids"`
}

type notificationPolicyListResponse struct {
	RequestID string                     `json:"request_id"`
	Items     []store.NotificationPolicy `json:"items"`
	Total     int                        `json:"total"`
}

// validateName은 정책 이름을 검사한다.
func validateName(name string) *paramError {
	switch n := strings.TrimSpace(name); {
	case n == "":
		return &paramError{"invalid_name", "정책 이름은 비울 수 없습니다."}
	case len(n) > maxPolicyNameLen:
		return &paramError{"invalid_name",
			fmt.Sprintf("정책 이름은 %d자 이하여야 합니다.", maxPolicyNameLen)}
	}
	return nil
}

func validateEventTypes(types []string) *paramError {
	if len(types) == 0 {
		return &paramError{"invalid_event_types", "event_types에 사건 종류를 하나 이상 지정하십시오."}
	}
	for _, t := range types {
		switch t {
		case store.NotifyEventCertExpiring, store.NotifyEventCertExpired:
		default:
			return &paramError{"invalid_event_types",
				"event_types는 certificate.expiring/certificate.expired 중 하나여야 합니다."}
		}
	}
	return nil
}

func validateThresholds(days []int) *paramError {
	if len(days) > maxPolicyThresholds {
		return &paramError{"invalid_thresholds",
			fmt.Sprintf("thresholds는 %d개 이하여야 합니다.", maxPolicyThresholds)}
	}
	for _, d := range days {
		if d < 1 || d > maxThresholdDays {
			return &paramError{"invalid_thresholds",
				fmt.Sprintf("thresholds의 각 값은 1~%d 사이 정수여야 합니다.", maxThresholdDays)}
		}
	}
	return nil
}

func validateNotifyChannels(channels []string) *paramError {
	if len(channels) == 0 {
		return &paramError{"invalid_channels", "channels에 채널을 하나 이상 지정하십시오."}
	}
	for _, ch := range channels {
		if !isKnownNotifyChannel(ch) {
			return &paramError{"invalid_channels",
				"channels는 email/webhook/inbox/syslog 중 하나여야 합니다."}
		}
	}
	return nil
}

func isKnownNotifyChannel(ch string) bool {
	for _, known := range notifyChannelOrder {
		if ch == known {
			return true
		}
	}
	return false
}

// validateRecipients는 수신자 종류·참조 형식을 검사한다.
// 정본 kind는 owner|role|user|static이다 (설계 14.2).
// 오류 메시지에 메일 주소를 넣지 않는다 (설계 11.4).
func validateRecipients(rs []store.NotificationRecipient) *paramError {
	if len(rs) > maxPolicyRecipients {
		return &paramError{"invalid_recipients",
			fmt.Sprintf("recipients는 %d개 이하여야 합니다.", maxPolicyRecipients)}
	}
	for _, r := range rs {
		switch r.Kind {
		case store.RecipientOwner:
		case store.RecipientRole:
			if !domain.Role(r.Role).Valid() {
				return &paramError{"invalid_recipients", "recipients의 role이 정의된 6개 역할이 아닙니다."}
			}
		case store.RecipientUser:
			if !isUUID(r.UserID) {
				return &paramError{"invalid_recipients", "recipients의 user_id는 uuid여야 합니다."}
			}
		case store.RecipientStatic:
			if i := strings.Index(r.Email, "@"); i < 1 {
				return &paramError{"invalid_recipients", "static 수신자에는 메일 주소가 필요합니다."}
			}
		default:
			return &paramError{"invalid_recipients",
				"recipients의 kind는 owner/role/user/static 중 하나여야 합니다."}
		}
	}
	return nil
}

func validateDepartmentIDs(ids []string) *paramError {
	for _, id := range ids {
		if !isUUID(id) {
			return &paramError{"invalid_filter_department_ids", "filter_department_ids는 uuid 목록이어야 합니다."}
		}
	}
	return nil
}

// handleListNotificationPolicies는 GET /api/v1/notification-policies다(읽기 전용).
func (s *Server) handleListNotificationPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListNotificationPolicies(r.Context())
	if err != nil {
		slog.Error("notification policies list failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"알림 정책 목록을 조회할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, notificationPolicyListResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Items:     items,
		Total:     len(items),
	})
}

// handleCreateNotificationPolicy는 POST /api/v1/notification-policies다.
// 생성+감사(notification.policy.create)는 store가 단일 트랜잭션으로 처리한다.
func (s *Server) handleCreateNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	var req notificationPolicyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNotificationRequestBytes)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "요청 본문을 JSON으로 파싱할 수 없습니다.")
		return
	}
	// 형식 검증은 핸들러가 한다 — store의 검증 오류는 센티널이 아니라 일반 오류라
	// 400과 500을 구분할 수 없다. store 쪽 검증은 그대로 두 번째 방어선으로 남긴다.
	for _, perr := range []*paramError{
		validateName(req.Name),
		validateEventTypes(req.EventTypes),
		validateThresholds(req.Thresholds),
		validateNotifyChannels(req.Channels),
		validateRecipients(req.Recipients),
		validateDepartmentIDs(req.FilterDepartmentIDs),
	} {
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	actor := userFrom(r.Context())
	policy, err := s.store.CreateNotificationPolicy(r.Context(), store.NotificationPolicyInput{
		Name:                req.Name,
		Enabled:             enabled,
		EventTypes:          req.EventTypes,
		Thresholds:          req.Thresholds,
		Channels:            req.Channels,
		Recipients:          req.Recipients,
		FilterEnvironments:  req.FilterEnvironments,
		FilterDepartmentIDs: req.FilterDepartmentIDs,
		ActorID:             &actor.ID,
		RequestID:           requestIDFrom(r.Context()),
		SourceIP:            clientIP(r),
	})
	if writeNotificationPolicyError(w, r, err, "create notification policy failed") {
		return
	}
	writeJSON(w, http.StatusCreated, policy)
}

// handleUpdateNotificationPolicy는 PATCH /api/v1/notification-policies/{id}다.
// 지정한 필드만 바꾸고 before/after 감사(notification.policy.update)를 같은 트랜잭션으로 남긴다.
func (s *Server) handleUpdateNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "해당 알림 정책을 찾을 수 없습니다.")
		return
	}
	var req notificationPolicyPatchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNotificationRequestBytes)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "요청 본문을 JSON으로 파싱할 수 없습니다.")
		return
	}

	patch := store.NotificationPolicyPatch{
		Enabled:            req.Enabled,
		FilterEnvironments: req.FilterEnvironments,
	}
	var checks []*paramError
	if req.Name != nil {
		checks = append(checks, validateName(*req.Name))
		patch.Name = req.Name
	}
	if req.EventTypes != nil {
		checks = append(checks, validateEventTypes(*req.EventTypes))
		patch.EventTypes = req.EventTypes
	}
	if req.Thresholds != nil {
		checks = append(checks, validateThresholds(*req.Thresholds))
		patch.Thresholds = req.Thresholds
	}
	if req.Channels != nil {
		checks = append(checks, validateNotifyChannels(*req.Channels))
		patch.Channels = req.Channels
	}
	if req.Recipients != nil {
		checks = append(checks, validateRecipients(*req.Recipients))
		patch.Recipients = req.Recipients
	}
	if req.FilterDepartmentIDs != nil {
		checks = append(checks, validateDepartmentIDs(*req.FilterDepartmentIDs))
		patch.FilterDepartmentIDs = req.FilterDepartmentIDs
	}
	for _, perr := range checks {
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}
	}

	actor := userFrom(r.Context())
	patch.ActorID = &actor.ID
	patch.RequestID = requestIDFrom(r.Context())
	patch.SourceIP = clientIP(r)

	policy, err := s.store.UpdateNotificationPolicy(r.Context(), id, patch)
	if writeNotificationPolicyError(w, r, err, "update notification policy failed") {
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

// handleDeleteNotificationPolicy는 DELETE /api/v1/notification-policies/{id}다.
// 이미 만들어진 이벤트는 policy_id가 NULL로 남아 이력이 보존된다(0012 ON DELETE SET NULL).
func (s *Server) handleDeleteNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "해당 알림 정책을 찾을 수 없습니다.")
		return
	}
	actor := userFrom(r.Context())
	err := s.store.DeleteNotificationPolicy(r.Context(), id, &actor.ID,
		requestIDFrom(r.Context()), clientIP(r))
	if writeNotificationPolicyError(w, r, err, "delete notification policy failed") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeNotificationPolicyError는 정책 API의 공통 오류 매핑이다.
// 처리했으면(=오류였으면) true를 반환한다.
func writeNotificationPolicyError(w http.ResponseWriter, r *http.Request, err error, logMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "해당 알림 정책을 찾을 수 없습니다.")
	case errors.Is(err, store.ErrNotificationPolicyNameTaken):
		writeError(w, r, http.StatusConflict, "policy_name_taken", "같은 이름의 알림 정책이 이미 있습니다.")
	default:
		slog.Error(logMsg, "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"알림 정책을 처리할 수 없습니다. 잠시 후 다시 시도하십시오.")
	}
	return true
}

// ===== 채널 =====

// handleListNotificationChannels는 GET /api/v1/notification-channels다.
// 응답은 {channel, enabled, reason}뿐이다 — 설정값은 서버 환경변수에서 확인한다.
func (s *Server) handleListNotificationChannels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": deref(requestIDFrom(r.Context())),
		"items":      s.channelStatuses(),
	})
}

// channelStatuses는 주입된 구현의 상태를 반환한다. 배선이 없으면 4채널 전부
// 비활성 + 사유로 답한다(진단 경로 일원화 — 설계 10.1).
func (s *Server) channelStatuses() []ChannelStatus {
	if s.channels == nil {
		out := make([]ChannelStatus, 0, len(notifyChannelOrder))
		for _, ch := range notifyChannelOrder {
			out = append(out, ChannelStatus{Channel: ch, Reason: channelsNotWiredReason})
		}
		return out
	}
	items := s.channels.Status()
	if items == nil {
		items = []ChannelStatus{}
	}
	return items
}

// ===== 테스트 발송 =====

type testNotificationRequest struct {
	// Channel이 비면 정책의 전 채널을 테스트한다. 지정하면 그 채널만.
	Channel string `json:"channel"`
	// Recipient는 선택이다. 비면 채널 구현이 기본 수신자를 고른다.
	Recipient string `json:"recipient"`
}

// testChannelResult는 채널 1개의 테스트 결과다. 수신자 주소는 담지 않는다.
type testChannelResult struct {
	Channel string `json:"channel"`
	Status  string `json:"status"` // sent | skipped | failed
	Reason  string `json:"reason,omitempty"`
}

const (
	testResultSent    = "sent"
	testResultSkipped = "skipped"
	testResultFailed  = "failed"
)

// handleTestNotificationPolicy는 POST /api/v1/notification-policies/{id}/test다.
// 정책이 쓰는 채널로 테스트 알림을 보내고 notification.test 감사를 남긴다.
// 감사 삽입은 트랜잭션의 마지막(이자 유일한) 문장이다 (docs/GOTCHAS.md).
func (s *Server) handleTestNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "해당 알림 정책을 찾을 수 없습니다.")
		return
	}
	var req testNotificationRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNotificationRequestBytes)).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "요청 본문을 JSON으로 파싱할 수 없습니다.")
			return
		}
	}

	policy, err := s.store.GetNotificationPolicy(r.Context(), id)
	if writeNotificationPolicyError(w, r, err, "test notification policy failed") {
		return
	}

	targets := policy.Channels
	if req.Channel != "" {
		if !containsString(policy.Channels, req.Channel) {
			writeError(w, r, http.StatusBadRequest, "invalid_channel",
				"이 정책이 쓰지 않는 채널입니다.")
			return
		}
		targets = []string{req.Channel}
	}

	if s.channels == nil {
		// 배선이 없으면 보낼 수단이 없다. 시도 자체는 감사에 남긴다.
		if err := s.auditNotificationTest(r, policy, nil, domain.AuditFailure, channelsNotWiredReason); err != nil {
			slog.Error("audit notification.test failed", "error", err.Error())
			writeError(w, r, http.StatusInternalServerError, "internal_error", "감사 기록에 실패했습니다.")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "channels_unavailable",
			"이 서버에는 알림 발송이 배선되어 있지 않습니다.")
		return
	}

	enabled := map[string]ChannelStatus{}
	for _, st := range s.channels.Status() {
		enabled[st.Channel] = st
	}

	results := make([]testChannelResult, 0, len(targets))
	sent := 0
	for _, ch := range targets {
		if st, ok := enabled[ch]; ok && !st.Enabled {
			results = append(results, testChannelResult{Channel: ch, Status: testResultSkipped, Reason: st.Reason})
			continue
		}
		if err := s.channels.SendTest(r.Context(), ch, req.Recipient); err != nil {
			// 채널 구현이 주소·비밀값을 제거한 문자열만 돌려준다는 계약이다.
			results = append(results, testChannelResult{
				Channel: ch, Status: testResultFailed, Reason: truncateRunes(err.Error(), maxTestErrorRunes),
			})
			continue
		}
		results = append(results, testChannelResult{Channel: ch, Status: testResultSent})
		sent++
	}

	result := domain.AuditSuccess
	if sent == 0 {
		result = domain.AuditFailure
	}
	if err := s.auditNotificationTest(r, policy, results, result, ""); err != nil {
		slog.Error("audit notification.test failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal_error", "감사 기록에 실패했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": deref(requestIDFrom(r.Context())),
		"policy_id":  policy.ID,
		"results":    results,
	})
}

// auditNotificationTest는 테스트 발송 감사를 단일 트랜잭션으로 남긴다.
// after 값에 수신자 주소를 담지 않는다 — 채널별 결과만 남긴다 (설계 11.4).
func (s *Server) auditNotificationTest(r *http.Request, policy *store.NotificationPolicy,
	results []testChannelResult, result domain.AuditResult, reason string) error {
	after := map[string]any{"policy_name": policy.Name}
	if results != nil {
		after["results"] = results
	}
	if reason != "" {
		after["reason"] = reason
	}
	actor := userFrom(r.Context())

	tx, err := s.pool.BeginTx(r.Context(), nil)
	if err != nil {
		return fmt.Errorf("api: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := audit.Insert(r.Context(), tx, audit.Event{
		Type:       EventNotificationTest,
		ActorID:    &actor.ID,
		TargetType: "notification_policy",
		TargetID:   &policy.ID,
		RequestID:  requestIDFrom(r.Context()),
		After:      after,
		Result:     result,
		SourceIP:   clientIP(r),
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// ===== 발송 이력 =====

type deliveryListResponse struct {
	RequestID string                 `json:"request_id"`
	Items     []store.DeliveryRecord `json:"items"`
	Total     int                    `json:"total"`
	Page      int                    `json:"page"`
	PageSize  int                    `json:"page_size"`
}

// handleListNotificationDeliveries는 GET /api/v1/notification-deliveries다.
// status·channel·event_id 필터 + 최신순 페이지네이션 (읽기 전용, 감사 없음).
func (s *Server) handleListNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := store.ListDeliveriesInput{
		Status:  strings.TrimSpace(q.Get("status")),
		Channel: strings.TrimSpace(q.Get("channel")),
		EventID: strings.TrimSpace(q.Get("event_id")),
	}
	switch in.Status {
	case "", store.DeliveryStatusPending, store.DeliveryStatusSending,
		store.DeliveryStatusSent, store.DeliveryStatusFailed, store.DeliveryStatusSkipped:
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_status",
			"status는 pending/sending/sent/failed/skipped 중 하나여야 합니다.")
		return
	}
	if in.Channel != "" && !isKnownNotifyChannel(in.Channel) {
		writeError(w, r, http.StatusBadRequest, "invalid_channel",
			"channel은 email/webhook/inbox/syslog 중 하나여야 합니다.")
		return
	}
	if in.EventID != "" && !isUUID(in.EventID) {
		writeError(w, r, http.StatusBadRequest, "invalid_event_id", "event_id는 uuid여야 합니다.")
		return
	}
	page, pageSize, perr := parsePageParams(r)
	if perr != nil {
		writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
		return
	}
	in.Page, in.PageSize = page, pageSize

	res, err := s.store.ListNotificationDeliveries(r.Context(), in)
	if err != nil {
		slog.Error("notification deliveries list failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"발송 이력을 조회할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, deliveryListResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Items:     res.Items,
		Total:     res.Total,
		Page:      res.Page,
		PageSize:  res.PageSize,
	})
}

// containsString은 문자열 슬라이스 포함 여부다.
func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// truncateRunes는 응답·감사에 싣는 문자열을 룬 단위로 자른다
// (바이트로 자르면 한글이 깨진다).
func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}
