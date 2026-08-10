package api

// 주기 재스캔 상태 조회·수동 1배치 실행 (06-rescan-and-replacement 11.2).
//
// 재스캔 정책 화면은 만들지 않는다(설계 11.5) — 설정은 환경변수 zero-config이고
// 이 API는 "지금 어떻게 돌고 있나"를 읽는 창 하나와 "지금 한 번 돌려라" 버튼뿐이다.
//
// 스케줄러는 이 패키지가 아니라 discovery가 갖고 있고 배선은 cmd/server가 한다.
// 여기서는 RescanController 인터페이스로만 잡는다 — 알림 채널(NotificationChannels)과
// 같은 실패 격리 규약이다: 배선이 없어도 코어 API는 뜨고, 상태 조회는 "미배선"을
// 명시하고 실행은 503이다.

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// defaultRescanInterval은 배선이 없을 때 상태 응답에 실을 기본 주기다.
// 배선이 있으면 RescanController.Settings()의 값이 이긴다.
const defaultRescanInterval = 12 * time.Hour

// rescanNotWiredReason은 스케줄러 배선이 없을 때의 사유다.
// 설정값(호스트·주소·간격 원문)을 담지 않는다 — 채널 상태 응답과 같은 규약이다.
const rescanNotWiredReason = "rescan scheduler is not wired on this server"

// rescanDisabledReason은 배선은 있으나 기능이 꺼진 경우의 사유다.
const rescanDisabledReason = "rescan is disabled by configuration (RESCAN_ENABLED)"

// RescanSettings는 배선이 알려주는 재스캔 설정이다.
// 값 자체가 아니라 화면에 띄울 요약만 담는다(비밀값 없음).
type RescanSettings struct {
	// Enabled는 RESCAN_ENABLED다. false면 스케줄러가 아예 뜨지 않는다.
	Enabled bool
	// Interval은 monitor별 override가 없을 때의 전역 재확인 주기다.
	Interval time.Duration
}

// RescanRunActor는 수동 1배치 실행의 감사 컨텍스트다.
// 구현은 발주한 스캔 Job마다 discovery.scan 감사를 남긴다(설계 10.4 — 새 이벤트
// 타입을 만들지 않고 after_value의 origin으로 구분한다).
type RescanRunActor struct {
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// RescanRunResult는 수동 1배치의 발주 결과다.
type RescanRunResult struct {
	// Jobs는 만들어진 스캔 Job 수다(포트별 그룹 발주라 보통 1~2건, 설계 4.1).
	Jobs int
	// Endpoints는 이번 배치가 claim한 엔드포인트 수다.
	Endpoints int
}

// RescanController는 재스캔 스케줄러의 API 노출면이다.
// cmd/server가 discovery 쪽 구현을 WithRescanController로 주입한다.
type RescanController interface {
	// Settings는 현재 재스캔 설정을 반환한다.
	Settings() RescanSettings
	// RunOnce는 due 판정을 무시하고 즉시 1배치를 발주한다.
	// 발주할 것이 없으면 0건을 담아 nil 에러로 돌아온다(오류가 아니다).
	RunOnce(ctx context.Context, actor RescanRunActor) (RescanRunResult, error)
}

// rescanStatusResponse는 GET /discovery/rescan/status 응답이다.
// 설정값 원문이 아니라 초 단위 간격과 집계 수치만 담는다.
type rescanStatusResponse struct {
	RequestID string `json:"request_id"`
	// Enabled는 "지금 자동으로 돌고 있는가"다 — 배선이 없으면 false다.
	Enabled bool `json:"enabled"`
	// Wired는 스케줄러 배선 자체의 유무다. Enabled=false의 두 원인
	// (배선 없음 / 설정으로 끔)을 화면이 구분할 수 있어야 한다.
	Wired           bool   `json:"wired"`
	IntervalSeconds int    `json:"interval_seconds"`
	Reason          string `json:"reason,omitempty"`
	store.RescanOverview
}

// handleGetRescanStatus는 GET /api/v1/discovery/rescan/status다 (읽기 전용·전 역할).
func (s *Server) handleGetRescanStatus(w http.ResponseWriter, r *http.Request) {
	enabled, wired, interval, reason := s.rescanSettings()

	overview, err := s.store.GetRescanOverview(r.Context(), interval)
	if err != nil {
		slog.Error("discovery: rescan overview failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"재스캔 현황을 조회할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, rescanStatusResponse{
		RequestID:       deref(requestIDFrom(r.Context())),
		Enabled:         enabled,
		Wired:           wired,
		IntervalSeconds: int(interval / time.Second),
		Reason:          reason,
		RescanOverview:  *overview,
	})
}

// rescanSettings는 배선 유무를 흡수해 (활성, 배선됨, 유효 간격, 사유)를 돌려준다.
// 배선이 없어도 간격은 defaultRescanInterval로 답한다 — 감시 엔드포인트 수와
// 적체 수는 스케줄러가 없어도 의미가 있는 값이라 화면을 비우지 않는다.
func (s *Server) rescanSettings() (enabled, wired bool, interval time.Duration, reason string) {
	if s.rescan == nil {
		return false, false, defaultRescanInterval, rescanNotWiredReason
	}
	set := s.rescan.Settings()
	interval = set.Interval
	if interval <= 0 {
		interval = defaultRescanInterval
	}
	if !set.Enabled {
		return false, true, interval, rescanDisabledReason
	}
	return true, true, interval, ""
}

type rescanRunResponse struct {
	RequestID string `json:"request_id"`
	// DispatchedJobs·DispatchedEndpoints가 0이면 지금 due인 엔드포인트가 없다는 뜻이다.
	DispatchedJobs      int `json:"dispatched_jobs"`
	DispatchedEndpoints int `json:"dispatched_endpoints"`
}

// handleRunRescan은 POST /api/v1/discovery/rescan/run이다.
// 스캔 발주와 같은 3역할만 호출할 수 있고(라우트에서 강제), 발주된 Job마다
// discovery.scan 감사 이벤트가 남는다(구현 책임 — 설계 10.4).
func (s *Server) handleRunRescan(w http.ResponseWriter, r *http.Request) {
	if s.rescan == nil {
		// 배선 없음은 사용자가 고칠 수 없는 서버 구성 문제다 — 503으로 답해
		// "권한이 없다"(403)·"입력이 틀렸다"(400)와 섞이지 않게 한다.
		writeError(w, r, http.StatusServiceUnavailable, "rescan_unavailable",
			"주기 재스캔 스케줄러가 이 서버에 배선되어 있지 않습니다.")
		return
	}
	if !s.rescan.Settings().Enabled {
		writeError(w, r, http.StatusConflict, "rescan_disabled",
			"주기 재스캔이 비활성화되어 있습니다. 서버 설정을 확인하십시오.")
		return
	}

	var actorID *string
	if u := userFrom(r.Context()); u != nil {
		actorID = &u.ID
	}
	res, err := s.rescan.RunOnce(r.Context(), RescanRunActor{
		ActorID:   actorID,
		RequestID: requestIDFrom(r.Context()),
		SourceIP:  clientIP(r),
	})
	if err != nil {
		slog.Error("discovery: rescan run failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"재스캔 배치를 시작할 수 없습니다.")
		return
	}
	// 스캔 생성과 같은 202다 — 실제 프로브는 백그라운드에서 돈다.
	writeJSON(w, http.StatusAccepted, rescanRunResponse{
		RequestID:           deref(requestIDFrom(r.Context())),
		DispatchedJobs:      res.Jobs,
		DispatchedEndpoints: res.Endpoints,
	})
}
