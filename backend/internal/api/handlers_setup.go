package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
)

// 초기 설정(첫 접속 시 관리자 계정 생성)은 인증 없이 열리는 유일한 변경 경로다.
// 그래서 두 라우트 모두 "사용자 0명"이라는 조건 하나에만 의존하고, 조건이 깨지는
// 순간(계정이 1명이라도 생긴 순간) 영구히 닫힌다 — 열려 있으면 누구나 관리자를
// 만들 수 있으므로 판정은 auth.CreateInitialAdmin이 DB 레벨에서 원자적으로 한다.

type setupStatusResponse struct {
	SetupRequired bool `json:"setup_required"`
}

type initialAdminRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleSetupStatus는 GET /api/v1/setup/status다 (인증 없음).
// 응답은 불리언 1개뿐이다 — 계정 수·계정명 같은 정보는 담지 않는다.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	required, err := s.auth.SetupRequired(r.Context())
	if err != nil {
		slog.Error("setup status failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, setupStatusResponse{SetupRequired: required})
}

// handleCreateInitialAdmin은 POST /api/v1/setup/admin이다 (인증 없음).
// 사용자가 0명일 때만 최초 System Administrator를 만들고, 그 외에는 409다.
// 비밀번호 정책은 일반 사용자 생성과 동일하며 감사 이벤트를 남긴다.
func (s *Server) handleCreateInitialAdmin(w http.ResponseWriter, r *http.Request) {
	var req initialAdminRequest
	// 모르는 필드는 거부한다 — role을 실어 보내도 무시되는 것이 아니라 400이다.
	if err := decodeUserRequest(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	if req.Username == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_username", "username is required")
		return
	}
	created, err := s.auth.CreateInitialAdmin(r.Context(), req.Username, req.Password,
		auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	switch {
	case errors.Is(err, auth.ErrSetupCompleted), errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, r, http.StatusConflict, "setup_completed",
			"initial setup is already completed")
		return
	case errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordEqidentifier):
		// 정책 위반 사유만 반환한다. 비밀번호 값은 포함하지 않는다.
		writeError(w, r, http.StatusBadRequest, "weak_password", err.Error())
		return
	case err != nil:
		slog.Error("create initial admin failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
