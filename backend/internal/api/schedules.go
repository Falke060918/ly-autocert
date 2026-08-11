package api

// 정기 스캔 스케줄 API (0019 scan_schedules).
//
// 노출면은 넷이다: 목록 조회 / 설정 부분 수정 / '지금 실행' / 실행 취소.
// 생성·삭제 라우트는 아직 없다 — full·registered는 시드 1행이 정본이고 preset
// 등록 화면이 나오는 시점에 함께 연다(설계상 store에는 이미 있다).
//
// 발주 자체는 이 패키지가 하지 않는다. tick 루프와 '지금 실행'은 discovery가
// 갖고 있고 배선은 cmd/server가 한다 — 재스캔(RescanController)과 같은 실패 격리
// 규약이다: 배선이 없어도 조회·수정은 뜨고 실행·취소만 503이다.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxScheduleRequestBytes는 스케줄 수정 요청 본문 상한이다(대상·포트 목록은 작다).
const maxScheduleRequestBytes = 64 << 10 // 64 KiB

// scheduleNotWiredMessage는 스케줄러 배선이 없을 때의 안내다.
// 설정값을 담지 않는다 — 재스캔 미배선 사유와 같은 규약이다.
const scheduleNotWiredMessage = "정기 스캔 스케줄러가 이 서버에 배선되어 있지 않습니다."

// ScheduleController는 정기 스캔 스케줄러의 API 노출면이다.
// cmd/server가 discovery 쪽 구현을 WithScheduleController로 주입한다.
type ScheduleController interface {
	// RunNow는 주기를 기다리지 않고 1회 실행을 연다.
	// **next_run_at은 건드리지 않는다**(사용자 확정 규약 — '지금 실행'은 1회 추가다).
	RunNow(ctx context.Context, scheduleID string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error)
	// CancelRun은 실행 1건을 취소한다(발주한 스윕을 먼저 멈춘다).
	CancelRun(ctx context.Context, runID string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error)
}

// scheduleSpecValidator는 store에 주입하는 port_spec 검증자다.
//
// **이 주입이 비면 포트 문법 검증이 "비었는지"까지만 남는다** — store는 discovery를
// import할 수 없어(순환) 파서를 알지 못한다. 파서는 한 곳(discovery.ParsePortSpec)뿐이며
// 스캔·스윕 생성이 쓰는 것과 같은 구현이어야 스케줄만 다른 규칙으로 통과하지 않는다.
func scheduleSpecValidator(spec string) error {
	if _, err := discovery.ParsePortSpec(spec); err != nil {
		// 내부 센티널 접두사를 떼고 한국어 사유만 남긴다(스캔 입력 오류와 같은 규약).
		return errors.New(scanInputMessage(err))
	}
	return nil
}

// scheduleResponseItem은 설정 1건 + 실행 문맥이다.
// 화면이 [지금 실행] 버튼을 열고 닫으려면 "지금 도는 실행이 있는가"가 필요하고,
// "마지막으로 언제 확인했나"는 마지막 실행에 있다.
type scheduleResponseItem struct {
	store.ScanSchedule
	ActiveRun *store.ScanScheduleRun `json:"active_run"`
	LastRun   *store.ScanScheduleRun `json:"last_run"`
}

type scheduleListResponse struct {
	RequestID string                 `json:"request_id"`
	Items     []scheduleResponseItem `json:"items"`
	// Wired는 실행·취소가 가능한 배선이 있는지다(재스캔 상태 응답과 같은 필드).
	Wired bool `json:"wired"`
}

// scheduleRunLookback은 목록 응답에 실을 실행 이력 조회 상한이다.
// 스케줄당 마지막 1건만 쓰므로 스케줄 수보다 충분히 크면 된다.
const scheduleRunLookback = 200

// handleListScanSchedules는 GET /api/v1/discovery/schedules다.
// 읽기 전용 — 전 역할, 감사 이벤트 없음 (00-spec 7.12).
func (s *Server) handleListScanSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	schedules, err := s.store.ListScanSchedules(ctx)
	if err != nil {
		slog.Error("discovery: list scan schedules failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(ctx)))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"정기 스캔 설정을 조회할 수 없습니다.")
		return
	}
	runs, err := s.store.ListScanScheduleRuns(ctx, store.ListScanScheduleRunsInput{
		Page: 1, PageSize: scheduleRunLookback,
	})
	if err != nil {
		slog.Error("discovery: list scan schedule runs failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(ctx)))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"정기 스캔 실행 이력을 조회할 수 없습니다.")
		return
	}

	// 최신순 목록이라 스케줄별 첫 항목이 마지막 실행이다.
	last := map[string]*store.ScanScheduleRun{}
	active := map[string]*store.ScanScheduleRun{}
	for i := range runs.Items {
		run := &runs.Items[i]
		if _, ok := last[run.ScheduleID]; !ok {
			last[run.ScheduleID] = run
		}
		if run.Status == store.ScheduleRunRunning {
			active[run.ScheduleID] = run
		}
	}

	items := make([]scheduleResponseItem, 0, len(schedules))
	for _, sc := range schedules {
		items = append(items, scheduleResponseItem{
			ScanSchedule: sc,
			ActiveRun:    active[sc.ID],
			LastRun:      last[sc.ID],
		})
	}
	writeJSON(w, http.StatusOK, scheduleListResponse{
		RequestID: deref(requestIDFrom(ctx)),
		Items:     items,
		Wired:     s.schedules != nil,
	})
}

// updateScanScheduleRequest는 PATCH 본문이다. 대상은 id 또는 kind로 지목한다 —
// full·registered는 시드 1행이라 화면이 id를 몰라도 kind로 집을 수 있어야 한다.
//
// interval_seconds·rate_pps는 null을 명시해 "환경변수 기본값으로 되돌림"을 표현하므로
// "키 없음"과 "값 null"을 구분해야 한다 — json.RawMessage(포인터가 아님)로 받는다
// (docs/GOTCHAS.md의 *json.RawMessage 항목).
type updateScanScheduleRequest struct {
	ID              *string         `json:"id"`
	Kind            *string         `json:"kind"`
	Name            *string         `json:"name"`
	Enabled         *bool           `json:"enabled"`
	Targets         *[]string       `json:"targets"`
	PortSpec        *string         `json:"port_spec"`
	IntervalSeconds json.RawMessage `json:"interval_seconds"`
	RatePPS         json.RawMessage `json:"rate_pps"`
	ExecutorScope   *string         `json:"executor_scope"`
}

// handleUpdateScanSchedule은 PATCH /api/v1/discovery/schedules다.
// 대상 1건을 부분 수정하고 discovery.schedule 감사 이벤트를 남긴다(store 동일-tx).
// 허용 역할은 라우트에서 강제한다(스캔 발주와 같은 3역할).
func (s *Server) handleUpdateScanSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateScanScheduleRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleRequestBytes))
	// 모르는 필드는 거부한다 — 조용히 버리면 "200을 받았는데 값이 안 들어갔다"가 된다.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"요청 본문을 JSON으로 파싱할 수 없습니다.")
		return
	}

	target, ok := s.resolveScanSchedule(w, r, req)
	if !ok {
		return
	}

	upd := store.ScanScheduleUpdate{
		Name:     req.Name,
		Enabled:  req.Enabled,
		Targets:  req.Targets,
		PortSpec: req.PortSpec,
		// 포트 문법 검증자 주입 — 비면 store는 "비었는지"까지만 본다.
		ValidatePortSpec: scheduleSpecValidator,
		ExecutorScope:    req.ExecutorScope,
		Actor:            s.scanScheduleActor(r),
	}
	if len(req.IntervalSeconds) > 0 {
		v, err := decodeNullableInt(req.IntervalSeconds)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request",
				"interval_seconds는 정수 또는 null이어야 합니다.")
			return
		}
		upd.IntervalSeconds, upd.ClearIntervalSeconds = v, v == nil
	}
	if len(req.RatePPS) > 0 {
		v, err := decodeNullableInt(req.RatePPS)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request",
				"rate_pps는 정수 또는 null이어야 합니다.")
			return
		}
		upd.RatePPS, upd.ClearRatePPS = v, v == nil
	}

	updated, err := s.store.UpdateScanSchedule(ctx, target, upd)
	switch {
	case errors.Is(err, store.ErrScanScheduleNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 설정을 찾을 수 없습니다.")
		return
	case errors.Is(err, store.ErrScanScheduleInput):
		writeError(w, r, http.StatusBadRequest, "invalid_schedule", scheduleInputMessage(err))
		return
	case err != nil:
		slog.Error("discovery: update scan schedule failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(ctx)))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"정기 스캔 설정을 변경할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// resolveScanSchedule은 수정 대상 id를 정한다(id 또는 kind 중 정확히 하나).
// 문제가 있으면 응답을 직접 쓰고 false를 반환한다.
func (s *Server) resolveScanSchedule(w http.ResponseWriter, r *http.Request, req updateScanScheduleRequest) (string, bool) {
	hasID := req.ID != nil && strings.TrimSpace(*req.ID) != ""
	hasKind := req.Kind != nil && strings.TrimSpace(*req.Kind) != ""
	if hasID == hasKind {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"수정 대상을 id 또는 kind 중 하나로 지정해야 합니다.")
		return "", false
	}
	if hasID {
		id := strings.TrimSpace(*req.ID)
		if !isUUID(id) {
			writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 설정을 찾을 수 없습니다.")
			return "", false
		}
		return id, true
	}

	kind := strings.TrimSpace(*req.Kind)
	if kind != store.ScheduleKindFull && kind != store.ScheduleKindRegistered {
		// preset은 N행이라 kind로 특정할 수 없다.
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"kind로 지정할 수 있는 것은 full·registered뿐입니다. preset은 id로 지정하십시오.")
		return "", false
	}
	sc, err := s.store.GetScanScheduleByKind(r.Context(), kind)
	if errors.Is(err, store.ErrScanScheduleNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 설정을 찾을 수 없습니다.")
		return "", false
	}
	if err != nil {
		slog.Error("discovery: get scan schedule by kind failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"정기 스캔 설정을 조회할 수 없습니다.")
		return "", false
	}
	return sc.ID, true
}

// decodeNullableInt는 정수 또는 null을 읽는다(null이면 nil).
func decodeNullableInt(raw json.RawMessage) (*int, error) {
	var v *int
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// scheduleInputMessage는 400 응답에 실을 사용자 문구를 만든다.
// store 센티널 접두사("store: invalid scan schedule input: ")를 떼고 사유만 남긴다 —
// 내부 패키지명이 UI에 노출되면 안 된다(스캔 입력 오류와 같은 규약).
func scheduleInputMessage(err error) string {
	if reason, ok := strings.CutPrefix(err.Error(),
		store.ErrScanScheduleInput.Error()+": "); ok && reason != "" {
		return reason
	}
	return "정기 스캔 설정 입력이 올바르지 않습니다."
}

// scanScheduleActor는 변경 요청의 감사 주체를 만든다.
func (s *Server) scanScheduleActor(r *http.Request) store.ScanScheduleActor {
	var actorID *string
	if u := userFrom(r.Context()); u != nil {
		actorID = &u.ID
	}
	return store.ScanScheduleActor{
		ActorID:   actorID,
		RequestID: requestIDFrom(r.Context()),
		SourceIP:  clientIP(r),
	}
}

// scheduleRunActiveResponse는 409 응답이다 — 이미 도는 실행으로 화면이 붙을 수 있게
// run id와 발동 주체를 함께 싣는다(중복 스캔 409와 같은 형태).
type scheduleRunActiveResponse struct {
	Error     errorBody `json:"error"`
	RunID     string    `json:"run_id"`
	Trigger   string    `json:"trigger"`
	RequestID string    `json:"request_id,omitempty"`
}

// handleRunScanSchedule은 POST /api/v1/discovery/schedules/{id}/run이다.
// '지금 실행'은 주기를 리셋하지 않는다 — 예정된 다음 실행은 그대로다.
// 감사(discovery.schedule, phase=run_started)는 store가 같은 트랜잭션으로 남긴다.
func (s *Server) handleRunScanSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 설정을 찾을 수 없습니다.")
		return
	}
	if s.schedules == nil {
		// 배선 없음은 사용자가 고칠 수 없는 서버 구성 문제다 — 503으로 답해
		// "권한이 없다"(403)·"입력이 틀렸다"(400)와 섞이지 않게 한다.
		writeError(w, r, http.StatusServiceUnavailable, "schedule_unavailable", scheduleNotWiredMessage)
		return
	}

	run, err := s.schedules.RunNow(r.Context(), id, s.scanScheduleActor(r))
	var active *store.ScanScheduleRunActiveError
	switch {
	case errors.Is(err, store.ErrScanScheduleNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 설정을 찾을 수 없습니다.")
		return
	case errors.As(err, &active):
		writeJSON(w, http.StatusConflict, scheduleRunActiveResponse{
			Error: errorBody{Code: "schedule_run_active",
				Message: "이 정기 스캔이 이미 실행 중입니다."},
			RunID:     active.RunID,
			Trigger:   active.Trigger,
			RequestID: deref(requestIDFrom(r.Context())),
		})
		return
	case errors.Is(err, store.ErrScanScheduleInput):
		// 대상·포트가 비었거나 스윕 ETA가 상한을 넘는 등 "지금은 실행할 수 없다".
		writeError(w, r, http.StatusUnprocessableEntity, "schedule_not_runnable", scheduleInputMessage(err))
		return
	case err != nil:
		slog.Error("discovery: run scan schedule failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"정기 스캔을 실행할 수 없습니다.")
		return
	}
	// 스캔·스윕 발주와 같은 202다 — 실제 프로브는 백그라운드에서 돈다.
	writeJSON(w, http.StatusAccepted, run)
}

// handleCancelScanScheduleRun은 POST /api/v1/discovery/schedules/runs/{id}/cancel이다.
// 발주된 스윕을 먼저 멈추고 실행 이력을 canceled로 닫는다(discovery.schedule 감사).
func (s *Server) handleCancelScanScheduleRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 실행을 찾을 수 없습니다.")
		return
	}
	if s.schedules == nil {
		writeError(w, r, http.StatusServiceUnavailable, "schedule_unavailable", scheduleNotWiredMessage)
		return
	}

	run, err := s.schedules.CancelRun(r.Context(), id, s.scanScheduleActor(r))
	switch {
	case errors.Is(err, store.ErrScanScheduleRunNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "정기 스캔 실행을 찾을 수 없습니다.")
		return
	case errors.Is(err, store.ErrScanScheduleRunState):
		writeError(w, r, http.StatusConflict, "schedule_run_state",
			"이미 종료된 실행이라 취소할 수 없습니다. 목록을 새로고침하십시오.")
		return
	case err != nil:
		slog.Error("discovery: cancel scan schedule run failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"정기 스캔 실행을 취소할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, run)
}
