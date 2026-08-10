package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxScanRequestBytes는 스캔 생성 요청 본문 상한이다. 대상·포트 목록은 작다.
const maxScanRequestBytes = 64 << 10 // 64 KiB

type createScanRequest struct {
	Targets []string `json:"targets"` // IP 또는 CIDR 목록
	// PortSpec은 자유 텍스트 포트 사양이다("443,8443", "8000-9000", "web"/"common").
	// 지정하면 Ports보다 우선한다.
	PortSpec string `json:"port_spec"`
	// Ports는 정수 목록 입력이다(하위 호환 — 기존 호출자를 위해 유지).
	Ports       []int `json:"ports"`
	TimeoutMs   int   `json:"timeout_ms"`  // 미지정 시 기본값으로 클램프
	Concurrency int   `json:"concurrency"` // 미지정 시 기본값으로 클램프
	// SensorID를 지정하면 서버 직접 실행 대신 해당 센서가 스캔한다 (Phase 3a).
	// active 센서만 허용 — pending Job만 만들어지고 센서가 pull 리스로 가져간다.
	SensorID *string `json:"sensor_id"`
}

// resolveScanPorts는 단발 스캔의 포트를 정한다 (04-port-autoscan 9.2).
//
//	port_spec 지정 → 파싱 / 아니면 ports 지정 → 그대로 / 아니면 기본값 common
//
// 기본값은 [443]이 아니라 common 프리셋(26개)이다 — 설계 6.1의 기본값 변경.
// all 프리셋은 대상 1개만으로도 6.5만 엔드포인트라 즉시 스캔의 응답성 전제를
// 깨므로 단발 스캔에서는 거부한다(스윕 전용, 설계 4.2).
func resolveScanPorts(spec string, ports []int) ([]int, error) {
	if s := strings.TrimSpace(spec); s != "" {
		if hasAllPreset(s) {
			return nil, fmt.Errorf("%w: 프리셋 %q는 전 포트 백그라운드 스윕 전용입니다. "+
				"POST /api/v1/discovery/sweeps를 쓰거나 범위를 좁히십시오",
				discovery.ErrInvalidScanInput, discovery.PresetAll)
		}
		return discovery.ParsePortSpec(s)
	}
	if len(ports) > 0 {
		return ports, nil
	}
	return discovery.ParsePortSpec(discovery.DefaultPortSpec)
}

// hasAllPreset은 포트 사양에 all 프리셋 토큰이 들어 있는지 본다.
func hasAllPreset(spec string) bool {
	for _, raw := range strings.Split(spec, ",") {
		if strings.ToLower(strings.TrimSpace(raw)) == discovery.PresetAll {
			return true
		}
	}
	return false
}

// invalidScanInputPrefix는 파서가 오류를 감쌀 때 붙는 내부 센티널 접두사다
// ("discovery: invalid scan input: "). 응답에서 이걸 떼어낸다.
var invalidScanInputPrefix = discovery.ErrInvalidScanInput.Error() + ": "

// scanInputMessage는 400 응답에 실을 사용자 문구를 만든다.
// 파서 오류는 "<센티널>: <한국어 사유>" 형식이므로 접두사를 떼고 사유만 남긴다 —
// 프론트가 서버 메시지를 그대로 띄우므로 내부 패키지명이 UI에 노출되면 안 된다.
// 예상 형식이 아닌 오류는 원문을 흘리지 않고 일반 문구로 대체한다(원문은 로그에만).
func scanInputMessage(err error) string {
	if reason, ok := strings.CutPrefix(err.Error(), invalidScanInputPrefix); ok && reason != "" {
		return reason
	}
	return "스캔 입력이 올바르지 않습니다."
}

// writeScanInputError는 스캔·스윕 입력 오류를 400으로 돌려준다.
// 응답에는 한국어 사유만 싣고 접두사가 붙은 원문은 서버 로그에만 남긴다.
func writeScanInputError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("discovery: invalid scan input", "error", err.Error(),
		"request_id", deref(requestIDFrom(r.Context())))
	writeError(w, r, http.StatusBadRequest, "invalid_scan_request", scanInputMessage(err))
}

type createScanResponse struct {
	JobID          string `json:"job_id"`
	Status         string `json:"status"`
	TotalEndpoints int    `json:"total_endpoints"`
	RequestID      string `json:"request_id"`
}

// handleCreateScan은 POST /api/v1/discovery/scans를 처리한다.
// 스캔 Job을 만들어 즉시 job id를 반환하고(202), 실제 스캔은 백그라운드에서 돈다.
// sensor_id가 있으면 센서 위임 pending Job만 만든다(실행은 센서 pull 리스, 00-spec 7.8).
// 생성은 감사 이벤트를 남긴다(store.CreateScanJob 내부). 허용 역할은 라우트에서 강제한다.
func handleCreateScan(mgr *discovery.Manager, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqIDStr := ""
		if reqID := requestIDFrom(ctx); reqID != nil {
			reqIDStr = *reqID
		}

		var req createScanRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScanRequestBytes))
		if err := dec.Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request",
				"요청 본문을 JSON으로 파싱할 수 없습니다.")
			return
		}
		ports, err := resolveScanPorts(req.PortSpec, req.Ports)
		if err != nil {
			// 파서의 한국어 사유만 전달한다(내부 접두사는 로그행).
			writeScanInputError(w, r, err)
			return
		}
		req.Ports = ports

		// 센서 위임 스캔: 존재하고 active인 센서만 허용한다. 무효·비활성은 422 —
		// 형식 오류(400)와 구분해, 폼 입력이 아니라 선택한 센서가 문제임을 드러낸다.
		if req.SensorID != nil && *req.SensorID == "" {
			req.SensorID = nil
		}
		if req.SensorID != nil && !checkActiveSensor(w, r, st, *req.SensorID) {
			return
		}

		var actorID *string
		if u := userFrom(ctx); u != nil {
			actorID = &u.ID
		}

		res, err := mgr.Start(ctx, store.ScanInput{
			Targets:     req.Targets,
			Ports:       req.Ports,
			TimeoutMs:   req.TimeoutMs,
			Concurrency: req.Concurrency,
			SensorID:    req.SensorID,
			ActorID:     actorID,
			RequestID:   requestIDFrom(ctx),
			SourceIP:    clientIP(r),
		})
		if err != nil {
			if errors.Is(err, discovery.ErrInvalidScanInput) {
				// 오류 메시지는 입력 형식 사유만 담는다(인증서 정보 없음).
				writeScanInputError(w, r, err)
				return
			}
			slog.Error("discovery: start scan failed", "error", err.Error(), "request_id", reqIDStr)
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"스캔 작업을 시작할 수 없습니다.")
			return
		}

		writeJSON(w, http.StatusAccepted, createScanResponse{
			JobID:          res.JobID,
			Status:         store.ScanStatusPending,
			TotalEndpoints: res.TotalEndpoints,
			RequestID:      reqIDStr,
		})
	}
}

type scanJobResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Origin은 발주 출처다 (manual/sweep/rescan — 0015). 목록에서 사람 발주와
	// 백그라운드 발주를 구분한다.
	Origin string `json:"origin"`
	// ExecutorType/SensorID는 Job의 실행 주체를 드러낸다 (server 직접 실행 vs 센서 위임).
	ExecutorType       string     `json:"executor_type"`
	SensorID           *string    `json:"sensor_id"`
	Targets            []string   `json:"targets"`
	Ports              []int      `json:"ports"`
	TimeoutMs          int        `json:"timeout_ms"`
	Concurrency        int        `json:"concurrency"`
	TotalEndpoints     int        `json:"total_endpoints"`
	ScannedEndpoints   int        `json:"scanned_endpoints"`
	ReachableEndpoints int        `json:"reachable_endpoints"`
	CertificatesFound  int        `json:"certificates_found"`
	CertificatesNew    int        `json:"certificates_new"`
	Error              *string    `json:"error"`
	CreatedAt          time.Time  `json:"created_at"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}

// handleGetScan은 GET /api/v1/discovery/scans/{id}를 처리한다(진행률·결과 요약 조회).
func handleGetScan(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !isUUID(id) {
			writeError(w, r, http.StatusNotFound, "not_found", "스캔 작업을 찾을 수 없습니다.")
			return
		}
		job, err := st.GetScanJob(r.Context(), id)
		if errors.Is(err, store.ErrScanJobNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "스캔 작업을 찾을 수 없습니다.")
			return
		}
		if err != nil {
			slog.Error("discovery: get scan failed", "error", err.Error())
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"스캔 작업을 조회할 수 없습니다.")
			return
		}
		writeJSON(w, http.StatusOK, toScanJobResponse(job))
	}
}

func toScanJobResponse(j *store.ScanJob) scanJobResponse {
	return scanJobResponse{
		ID:                 j.ID,
		Status:             j.Status,
		Origin:             j.Origin,
		ExecutorType:       j.ExecutorType,
		SensorID:           j.SensorID,
		Targets:            j.Targets,
		Ports:              j.Ports,
		TimeoutMs:          j.TimeoutMs,
		Concurrency:        j.Concurrency,
		TotalEndpoints:     j.TotalEndpoints,
		ScannedEndpoints:   j.ScannedEndpoints,
		ReachableEndpoints: j.ReachableEndpoints,
		CertificatesFound:  j.CertificatesFound,
		CertificatesNew:    j.CertificatesNew,
		Error:              j.Error,
		CreatedAt:          j.CreatedAt,
		StartedAt:          j.StartedAt,
		FinishedAt:         j.FinishedAt,
	}
}

type scanJobListResponse struct {
	RequestID string            `json:"request_id"`
	Items     []scanJobResponse `json:"items"`
	Total     int               `json:"total"`
	Page      int               `json:"page"`
	PageSize  int               `json:"page_size"`
}

// handleListScans는 GET /api/v1/discovery/scans다 (최신 생성 순, 상태·출처 필터 +
// 페이지네이션). 읽기 전용 — 전 역할, 감사 이벤트 없음. 이 API가 생기기 전에는
// 프론트가 localStorage로 자기 브라우저의 Job만 추적했다.
func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	in := store.ListScanJobsInput{}
	for _, v := range splitMulti(r.URL.Query()["status"]) {
		switch v {
		case store.ScanStatusPending, store.ScanStatusLeased, store.ScanStatusRunning,
			store.ScanStatusCompleted, store.ScanStatusFailed:
			in.Statuses = append(in.Statuses, v)
		default:
			writeError(w, r, http.StatusBadRequest, "invalid_status",
				"status는 pending/leased/running/completed/failed 중 하나여야 합니다.")
			return
		}
	}
	for _, v := range splitMulti(r.URL.Query()["origin"]) {
		switch v {
		case store.ScanOriginManual, store.ScanOriginSweep, store.ScanOriginRescan:
			in.Origins = append(in.Origins, v)
		default:
			writeError(w, r, http.StatusBadRequest, "invalid_origin",
				"origin은 manual/sweep/rescan 중 하나여야 합니다.")
			return
		}
	}
	page, pageSize, perr := parsePageParams(r)
	if perr != nil {
		writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
		return
	}
	in.Page, in.PageSize = page, pageSize

	res, err := s.store.ListScanJobs(r.Context(), in)
	if err != nil {
		slog.Error("discovery: list scans failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"스캔 작업 목록을 조회할 수 없습니다.")
		return
	}
	items := make([]scanJobResponse, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, toScanJobResponse(&res.Items[i]))
	}
	writeJSON(w, http.StatusOK, scanJobListResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Items:     items,
		Total:     res.Total,
		Page:      res.Page,
		PageSize:  res.PageSize,
	})
}

// 스캔 대상 제안의 조회 상한. 화면 드롭다운·칩 용도라 페이지네이션이 아니라 상한이다.
const (
	scanSuggestRecentLimit  = 10
	scanSuggestNetworkLimit = 20
)

type scanSuggestionsResponse struct {
	RequestID       string                 `json:"request_id"`
	RecentTargets   []string               `json:"recent_targets"`
	DerivedNetworks []store.DerivedNetwork `json:"derived_networks"`
	// ConfiguredNetworks는 SCAN_SUGGESTED_NETWORKS(운영자 지정, 정규화된 CIDR)다.
	ConfiguredNetworks []string `json:"configured_networks"`
}

// handleScanSuggestions는 GET /api/v1/discovery/scan-suggestions다.
// 수기 CIDR 입력을 줄이기 위한 제안 3종: 최근 수동 스캔 대상 + targets 파생
// /24 대역 + 운영자 지정 대역. 읽기 전용 — 전 역할, 감사 이벤트 없음.
func (s *Server) handleScanSuggestions(w http.ResponseWriter, r *http.Request) {
	sug, err := s.store.ScanSuggestions(r.Context(), scanSuggestRecentLimit, scanSuggestNetworkLimit)
	if err != nil {
		slog.Error("discovery: scan suggestions failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"스캔 대상 제안을 조회할 수 없습니다.")
		return
	}
	configured := s.scanSuggestedNetworks
	if configured == nil {
		configured = []string{}
	}
	writeJSON(w, http.StatusOK, scanSuggestionsResponse{
		RequestID:          deref(requestIDFrom(r.Context())),
		RecentTargets:      sug.RecentTargets,
		DerivedNetworks:    sug.DerivedNetworks,
		ConfiguredNetworks: configured,
	})
}

// checkActiveSensor는 sensor_id가 존재하고 active인지 확인한다.
// 문제가 있으면 응답을 직접 쓰고 false를 반환한다 — 무효·비활성 센서는 422로,
// 형식 오류(400)와 구분해 "폼 입력이 아니라 고른 센서가 문제"임을 드러낸다.
func checkActiveSensor(w http.ResponseWriter, r *http.Request, st *store.Store, sensorID string) bool {
	if !isUUID(sensorID) {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_sensor", "유효한 활성 센서가 아닙니다.")
		return false
	}
	sn, err := st.GetSensor(r.Context(), sensorID)
	if errors.Is(err, store.ErrSensorNotFound) {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_sensor", "유효한 활성 센서가 아닙니다.")
		return false
	}
	if err != nil {
		slog.Error("discovery: lookup sensor failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"작업을 시작할 수 없습니다.")
		return false
	}
	if sn.Status != domain.SensorActive {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_sensor", "유효한 활성 센서가 아닙니다.")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// 전 포트 백그라운드 스윕 (docs/design/04-port-autoscan.md 9.2)
// ---------------------------------------------------------------------------

// sensorScopePrefix는 스윕·타깃의 executor_scope 접두사다 (0010 규약과 같은 문자열).
const sensorScopePrefix = "sensor:"

// SweepDefaults는 SCAN_SWEEP_* 설정에서 파생한 스윕 기본값이다
// (WithSweepDefaults로 주입한다). 0인 필드는 store 기본값으로 폴백한다.
type SweepDefaults struct {
	RatePPS        int // SCAN_SWEEP_RATE_PPS
	ChunkEndpoints int // SCAN_SWEEP_CHUNK_ENDPOINTS
	MaxDays        int // SCAN_SWEEP_MAX_DAYS — ETA가 넘으면 생성 거부(422)
}

type createSweepRequest struct {
	Name    *string  `json:"name"`
	Targets []string `json:"targets"`
	// PortSpec은 자유 텍스트 포트 사양이다. 비면 all(1-65535) — 스윕의 기본 용도다.
	PortSpec string `json:"port_spec"`
	// SensorID를 지정하면 청크 Job이 그 센서에 배정된다(executor_scope=sensor:<id>).
	SensorID       *string `json:"sensor_id"`
	RatePPS        int     `json:"rate_pps"`        // 0이면 서버 기본값
	ChunkEndpoints int     `json:"chunk_endpoints"` // 0이면 서버 기본값
}

type createSweepResponse struct {
	SweepID           string     `json:"sweep_id"`
	Status            string     `json:"status"`
	TotalEndpoints    int64      `json:"total_endpoints"`
	TotalChunks       int        `json:"total_chunks"`
	EstimatedFinishAt *time.Time `json:"estimated_finish_at"`
	RequestID         string     `json:"request_id"`
}

type sweepListResponse struct {
	RequestID string        `json:"request_id"`
	Items     []store.Sweep `json:"items"`
	Total     int           `json:"total"`
	Page      int           `json:"page"`
	PageSize  int           `json:"page_size"`
}

// expandSweepIPs는 대상 원문(IP/CIDR)을 개별 IP 목록으로 펼친다.
//
// 포트 1개짜리 Expand를 쓰는 이유: IP 전개와 입력 검증(CIDR 형식·호스트비트 상한·
// 엔드포인트 상한)이 스캐너와 **같은 구현**이어야 하기 때문이다. 두 번째 파서를
// 두면 반드시 갈라진다(설계 9.3의 클라이언트 파서 제거와 같은 이유).
// 결과적으로 스윕의 대상 IP 수 상한은 discovery.MaxEndpoints(=65536, /16 하나)다.
func expandSweepIPs(targets []string) ([]string, error) {
	endpoints, err := discovery.Expand(targets, []int{1})
	if err != nil {
		return nil, err
	}
	ips := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		ips = append(ips, ep.IP)
	}
	return ips, nil
}

// handleCreateSweep은 POST /api/v1/discovery/sweeps다.
// 스윕 행만 만들고 즉시 202로 답한다 — 청크 발주는 백그라운드 발주자 소관이다.
// 생성은 discovery.sweep 감사 이벤트를 남긴다(store.CreateSweep 내부).
func (s *Server) handleCreateSweep(w http.ResponseWriter, r *http.Request) {
	var req createSweepRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScanRequestBytes)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "요청 본문을 JSON으로 파싱할 수 없습니다.")
		return
	}

	spec := strings.TrimSpace(req.PortSpec)
	if spec == "" {
		spec = discovery.PresetAll // 스윕의 기본은 전 포트다.
	}
	ports, err := discovery.ParsePortSpec(spec)
	if err != nil {
		writeScanInputError(w, r, err)
		return
	}
	ips, err := expandSweepIPs(req.Targets)
	if err != nil {
		writeScanInputError(w, r, err)
		return
	}

	scope := domain.ExecutorScopeServer
	if req.SensorID != nil && *req.SensorID != "" {
		if !checkActiveSensor(w, r, s.store, *req.SensorID) {
			return
		}
		scope = sensorScopePrefix + *req.SensorID
	}

	actor := userFrom(r.Context())
	sweep, err := s.store.CreateSweep(r.Context(), store.SweepInput{
		Name:           req.Name,
		Targets:        req.Targets,
		IPCount:        len(ips),
		PortSpec:       spec,
		PortCount:      len(ports),
		ExecutorScope:  scope,
		RatePPS:        orDefaultInt(req.RatePPS, s.sweepDefaults.RatePPS),
		ChunkEndpoints: orDefaultInt(req.ChunkEndpoints, s.sweepDefaults.ChunkEndpoints),
		MaxDays:        s.sweepDefaults.MaxDays,
		ActorID:        &actor.ID,
		RequestID:      requestIDFrom(r.Context()),
		SourceIP:       clientIP(r),
	})
	var tooLong *store.SweepTooLongError
	if errors.As(err, &tooLong) {
		// 실제 소요 일수를 담은 메시지를 그대로 전달한다 — 사용자가 대역을
		// 얼마나 줄여야 하는지 알 수 있어야 한다 (설계 5.4).
		writeError(w, r, http.StatusUnprocessableEntity, "sweep_too_long", tooLong.Error())
		return
	}
	if err != nil {
		slog.Error("discovery: create sweep failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "스윕을 생성할 수 없습니다.")
		return
	}

	writeJSON(w, http.StatusAccepted, createSweepResponse{
		SweepID:           sweep.ID,
		Status:            sweep.Status,
		TotalEndpoints:    sweep.TotalEndpoints,
		TotalChunks:       sweep.TotalChunks,
		EstimatedFinishAt: sweep.EstimatedFinishAt,
		RequestID:         deref(requestIDFrom(r.Context())),
	})
}

// orDefaultInt는 요청값이 0 이하면 서버 기본값을 쓴다(그것도 0이면 store 기본값).
func orDefaultInt(requested, fallback int) int {
	if requested > 0 {
		return requested
	}
	return fallback
}

// handleListSweeps는 GET /api/v1/discovery/sweeps다(상태 필터 + 최신순 페이지네이션).
func (s *Server) handleListSweeps(w http.ResponseWriter, r *http.Request) {
	in := store.ListSweepsInput{}
	for _, v := range splitMulti(r.URL.Query()["status"]) {
		switch v {
		case store.SweepStatusQueued, store.SweepStatusRunning, store.SweepStatusPaused,
			store.SweepStatusCompleted, store.SweepStatusFailed, store.SweepStatusCanceled:
			in.Statuses = append(in.Statuses, v)
		default:
			writeError(w, r, http.StatusBadRequest, "invalid_status",
				"status는 queued/running/paused/completed/failed/canceled 중 하나여야 합니다.")
			return
		}
	}
	page, pageSize, perr := parsePageParams(r)
	if perr != nil {
		writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
		return
	}
	in.Page, in.PageSize = page, pageSize

	res, err := s.store.ListSweeps(r.Context(), in)
	if err != nil {
		slog.Error("discovery: list sweeps failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "스윕 목록을 조회할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, sweepListResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Items:     res.Items,
		Total:     res.Total,
		Page:      res.Page,
		PageSize:  res.PageSize,
	})
}

// handleGetSweep은 GET /api/v1/discovery/sweeps/{id}다.
// 진행률은 store가 자식 Job 집계로 파생한다(부모에 카운터 없음).
func (s *Server) handleGetSweep(w http.ResponseWriter, r *http.Request) {
	sweep, err := s.store.GetSweep(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrSweepNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "해당 스윕을 찾을 수 없습니다.")
		return
	}
	if err != nil {
		slog.Error("discovery: get sweep failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "스윕을 조회할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, sweep)
}

// 상태 전이 3종. 전부 발주자에 대한 제어이며 진행 중인 청크 Job은 완주한다
// (취소를 센서까지 전파하지 않는다 — 설계 6.2). 감사는 store가 남긴다(discovery.sweep).
func (s *Server) handlePauseSweep(w http.ResponseWriter, r *http.Request) {
	s.transitionSweep(w, r, s.store.PauseSweep, "pause sweep failed")
}

func (s *Server) handleResumeSweep(w http.ResponseWriter, r *http.Request) {
	s.transitionSweep(w, r, s.store.ResumeSweep, "resume sweep failed")
}

func (s *Server) handleCancelSweep(w http.ResponseWriter, r *http.Request) {
	s.transitionSweep(w, r, s.store.CancelSweep, "cancel sweep failed")
}

type sweepTransitionFunc func(ctx context.Context, sweepID string, act store.SweepActor) (*store.Sweep, error)

func (s *Server) transitionSweep(w http.ResponseWriter, r *http.Request, fn sweepTransitionFunc, logMsg string) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "해당 스윕을 찾을 수 없습니다.")
		return
	}
	actor := userFrom(r.Context())
	sweep, err := fn(r.Context(), id, store.SweepActor{
		ActorID:   &actor.ID,
		RequestID: requestIDFrom(r.Context()),
		SourceIP:  clientIP(r),
	})
	switch {
	case errors.Is(err, store.ErrSweepNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "해당 스윕을 찾을 수 없습니다.")
		return
	case errors.Is(err, store.ErrSweepState):
		writeError(w, r, http.StatusConflict, "sweep_state", "현재 상태에서는 할 수 없는 전이입니다.")
		return
	case err != nil:
		slog.Error("discovery: "+logMsg, "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "스윕 상태를 바꿀 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, sweep)
}

// handleListPortPresets는 GET /api/v1/discovery/port-presets다.
// discovery.PortPresets()를 그대로 노출한다 — 프리셋의 단일 진실원천이며
// 프론트엔드가 목록을 따로 갖지 않게 한다.
func handleListPortPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": deref(requestIDFrom(r.Context())),
		"items":      discovery.PortPresets(),
		"default":    discovery.DefaultPortSpec,
	})
}

// isUUID는 UUID(하이픈 포함 36자, hex) 형식을 가볍게 검증한다.
// 잘못된 경로 값이 DB의 ::uuid 캐스트 오류(500)로 새지 않게 막는다.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
