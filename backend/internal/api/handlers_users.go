package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

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
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.auth.ListUsers(r.Context())
	if err != nil {
		slog.Error("list users failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users, "total": len(users)})
}
