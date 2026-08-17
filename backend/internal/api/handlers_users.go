package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// maxUserRequestBytes는 사용자 API 요청 본문 상한이다.
const maxUserRequestBytes = 1 << 20 // 1 MiB

// maxEmailLen은 메일 주소 상한이다 (RFC 5321 forward-path 상한).
const maxEmailLen = 254

type createUserRequest struct {
	Username    string      `json:"username"`
	Password    string      `json:"password"`
	DisplayName *string     `json:"display_name"`
	Email       *string     `json:"email"`
	Role        domain.Role `json:"role"`
}

// decodeUserRequest는 사용자 API 본문을 디코드한다. 모르는 필드는 거부한다 —
// 조용히 버리면 "201을 받았는데 값이 안 들어갔다"는 오진을 부른다 (email 결함의 원인).
func decodeUserRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserRequestBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// normalizeEmail은 메일 주소를 정규화·검증한다.
// 빈 문자열(공백만인 경우 포함)은 nil로 낮춘다 — 0012의 users_email_shape CHECK가
// 빈 문자열을 거부하므로 "지움"과 같게 취급한다.
// 검증 기준은 그 CHECK(로컬부가 있는 '@' 포함)와 일관되게 두되, DB 제약이 잡지 못하는
// 공백·중복 '@'·빈 도메인부·길이는 API 계층에서 미리 거른다.
func normalizeEmail(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	if len(v) > maxEmailLen {
		return nil, fmt.Errorf("메일 주소는 %d자 이하여야 합니다", maxEmailLen)
	}
	if strings.ContainsFunc(v, unicode.IsSpace) {
		return nil, errors.New("메일 주소에는 공백을 넣을 수 없습니다")
	}
	at := strings.IndexByte(v, '@')
	if at < 1 || at != strings.LastIndexByte(v, '@') || at == len(v)-1 {
		return nil, errors.New("메일 주소는 local@domain 형식이어야 합니다")
	}
	return &v, nil
}

// handleCreateUser는 POST /api/v1/users다 (System Administrator 전용 — 00-spec 7.11 사용자 관리).
// 사용자 생성은 감사 대상이다 (00-spec 7.12: 사용자 및 권한 변경).
// 생성+감사 기록은 서비스가 단일 트랜잭션으로 처리한다 — 감사 실패 시 생성도 롤백 (이슈 #7).
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeUserRequest(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	if req.Username == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_username", "username is required")
		return
	}
	if !req.Role.Valid() {
		writeError(w, r, http.StatusBadRequest, "invalid_role", "role must be one of the 6 defined roles")
		return
	}
	var email *string
	if req.Email != nil {
		var err error
		if email, err = normalizeEmail(*req.Email); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_email", err.Error())
			return
		}
	}
	actor := userFrom(r.Context())
	created, err := s.auth.CreateUserWithEmail(r.Context(), req.Username, req.Password, req.DisplayName, email, req.Role,
		&actor.ID, auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordEqidentifier):
		// 정책 위반 사유만 한글로 반환한다. 원문 에러는 로그에만 남기고,
		// 어느 쪽에도 비밀번호 값은 포함하지 않는다.
		slog.Warn("create user rejected: password policy", "error", err.Error())
		writeError(w, r, http.StatusBadRequest, "weak_password", passwordPolicyMessage(err))
		return
	case errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, r, http.StatusConflict, "username_taken", "username already taken")
		return
	case err != nil:
		slog.Error("create user failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleListUsers는 GET /api/v1/users다 (System Administrator 전용).
// 응답 항목은 domain.User 그대로다 — role·is_active를 포함하며 password_hash는
// 직렬화에서 제외된다.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.auth.ListUsers(r.Context())
	if err != nil {
		slog.Error("list users failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users, "total": len(users)})
}

// updateUserRequest는 PATCH 부분 수정 본문이다. 필드가 없으면 그 값은 바뀌지 않는다.
// display_name·email은 null을 명시해 지울 수 있어야 하므로 "키 없음"과 "값 null"을
// 구분해야 한다 — json.RawMessage(포인터가 아님)로 받는다. 포인터 필드는
// 명시적 null에도 nil이 되어 두 경우를 구분할 수 없다.
type updateUserRequest struct {
	Role        *domain.Role    `json:"role"`
	DisplayName json.RawMessage `json:"display_name"`
	Email       json.RawMessage `json:"email"`
}

// handleUpdateUser는 PATCH /api/v1/users/{id}다 (System Administrator 전용).
// role·display_name·email 부분 수정. user.update 감사 이벤트를 같은 트랜잭션으로 남긴다.
// 자기 역할 변경과 마지막 활성 관리자 강등은 409로 거부한다 (락아웃 방지).
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var req updateUserRequest
	if err := decodeUserRequest(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	var upd auth.UserUpdate
	if req.Role != nil {
		if !req.Role.Valid() {
			writeError(w, r, http.StatusBadRequest, "invalid_role", "role must be one of the 6 defined roles")
			return
		}
		upd.Role = req.Role
	}
	if len(req.DisplayName) > 0 {
		var name *string
		if err := json.Unmarshal(req.DisplayName, &name); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_body", "display_name must be a string or null")
			return
		}
		upd.DisplayNameSet = true
		upd.DisplayName = name
	}
	if len(req.Email) > 0 {
		var raw *string
		if err := json.Unmarshal(req.Email, &raw); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_body", "email must be a string or null")
			return
		}
		upd.EmailSet = true
		if raw != nil {
			email, err := normalizeEmail(*raw)
			if err != nil {
				writeError(w, r, http.StatusBadRequest, "invalid_email", err.Error())
				return
			}
			upd.Email = email
		}
	}

	actor := userFrom(r.Context())
	updated, err := s.auth.UpdateUser(r.Context(), id, upd, actor.ID,
		auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	if writeUserMutationError(w, r, err, "update user failed") {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDisableUser는 POST /api/v1/users/{id}/disable이다 (System Administrator 전용).
// 비활성화는 대상의 활성 세션을 즉시 폐기한다 (auth.SetUserActive).
func (s *Server) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserActive(w, r, false)
}

// handleEnableUser는 POST /api/v1/users/{id}/enable이다 (System Administrator 전용).
func (s *Server) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserActive(w, r, true)
}

func (s *Server) setUserActive(w http.ResponseWriter, r *http.Request, active bool) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
		return
	}
	actor := userFrom(r.Context())
	updated, err := s.auth.SetUserActive(r.Context(), id, active, actor.ID,
		auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	if writeUserMutationError(w, r, err, "set user active failed") {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

// handleResetUserPassword는 POST /api/v1/users/{id}/password다
// (System Administrator 전용). 재설정은 대상의 활성 세션을 즉시 폐기한다.
// 본문·응답 어디에도 비밀번호를 되돌려 보내지 않으므로 성공은 204다.
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var req resetPasswordRequest
	if err := decodeUserRequest(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	actor := userFrom(r.Context())
	err := s.auth.ResetPassword(r.Context(), id, req.Password, actor.ID,
		auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	if writeUserMutationError(w, r, err, "reset password failed") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// passwordPolicyMessage는 비밀번호 정책 센티널을 사용자용 한글 메시지로 바꾼다.
// auth 패키지 원문(영문)은 로그 전용이다. 메시지에 비밀번호 값은 없다.
func passwordPolicyMessage(err error) string {
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort):
		return fmt.Sprintf("비밀번호는 %d자 이상이어야 합니다", auth.MinPasswordLength)
	case errors.Is(err, auth.ErrPasswordTooLong):
		return "비밀번호가 너무 깁니다 (최대 72바이트)"
	case errors.Is(err, auth.ErrPasswordEqidentifier):
		return "비밀번호는 사용자 이름과 같을 수 없습니다"
	}
	return "비밀번호가 정책에 맞지 않습니다"
}

// writeUserMutationError는 사용자 변경 API의 공통 오류 매핑이다.
// 처리했으면(=오류였으면) true를 반환한다. 로그·응답 어디에도 비밀번호는 없다 —
// auth 계층이 정책 위반 사유만 담은 센티널 에러를 반환하기 때문이다.
func writeUserMutationError(w http.ResponseWriter, r *http.Request, err error, logMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
	case errors.Is(err, auth.ErrNoUserChanges):
		writeError(w, r, http.StatusBadRequest, "no_fields",
			"at least one of role, display_name, email is required")
	case errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordEqidentifier):
		// 원문 에러는 로그에만 남긴다. 응답은 한글 정책 메시지다.
		slog.Warn(logMsg, "error", err.Error())
		writeError(w, r, http.StatusBadRequest, "weak_password", passwordPolicyMessage(err))
	case errors.Is(err, auth.ErrSelfDisable):
		writeError(w, r, http.StatusConflict, "self_disable_forbidden",
			"you cannot disable your own account")
	case errors.Is(err, auth.ErrSelfRoleChange):
		writeError(w, r, http.StatusConflict, "self_role_change_forbidden",
			"you cannot change your own role")
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, r, http.StatusConflict, "last_admin_protected",
			"the last active system administrator must remain")
	default:
		slog.Error(logMsg, "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
	return true
}
