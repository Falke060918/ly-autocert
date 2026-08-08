package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// maxUserRequestBytes는 사용자 API 요청 본문 상한이다.
const maxUserRequestBytes = 1 << 20 // 1 MiB

type createUserRequest struct {
	Username    string      `json:"username"`
	Password    string      `json:"password"`
	DisplayName *string     `json:"display_name"`
	Role        domain.Role `json:"role"`
}

// handleCreateUser는 POST /api/v1/users다 (System Administrator 전용 — 00-spec 7.11 사용자 관리).
// 사용자 생성은 감사 대상이다 (00-spec 7.12: 사용자 및 권한 변경).
// 생성+감사 기록은 서비스가 단일 트랜잭션으로 처리한다 — 감사 실패 시 생성도 롤백 (이슈 #7).
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
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
	actor := userFrom(r.Context())
	created, err := s.auth.CreateUser(r.Context(), req.Username, req.Password, req.DisplayName, req.Role,
		&actor.ID, auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordEqidentifier):
		// 정책 위반 사유만 반환한다. 비밀번호 값은 포함하지 않는다.
		writeError(w, r, http.StatusBadRequest, "weak_password", err.Error())
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
// display_name은 null을 명시해 지울 수 있어야 하므로 "키 없음"과 "값 null"을
// 구분해야 한다 — json.RawMessage(포인터가 아님)로 받는다. 포인터 필드는
// 명시적 null에도 nil이 되어 두 경우를 구분할 수 없다.
type updateUserRequest struct {
	Role        *domain.Role    `json:"role"`
	DisplayName json.RawMessage `json:"display_name"`
}

// handleUpdateUser는 PATCH /api/v1/users/{id}다 (System Administrator 전용).
// role·display_name 부분 수정. user.update 감사 이벤트를 같은 트랜잭션으로 남긴다.
// 자기 역할 변경과 마지막 활성 관리자 강등은 409로 거부한다 (락아웃 방지).
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var req updateUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserRequestBytes)).Decode(&req); err != nil {
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserRequestBytes)).Decode(&req); err != nil {
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
			"at least one of role, display_name is required")
	case errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordEqidentifier):
		writeError(w, r, http.StatusBadRequest, "weak_password", err.Error())
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
