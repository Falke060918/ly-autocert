package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxScanRequestBytes는 스캔 생성 요청 본문 상한이다. 대상·포트 목록은 작다.
const maxScanRequestBytes = 64 << 10 // 64 KiB

type createScanRequest struct {
	Targets     []string `json:"targets"`     // IP 또는 CIDR 목록
	Ports       []int    `json:"ports"`       // 미지정 시 [443]
	TimeoutMs   int      `json:"timeout_ms"`  // 미지정 시 기본값으로 클램프
	Concurrency int      `json:"concurrency"` // 미지정 시 기본값으로 클램프
}

type createScanResponse struct {
	JobID          string `json:"job_id"`
	Status         string `json:"status"`
	TotalEndpoints int    `json:"total_endpoints"`
	RequestID      string `json:"request_id"`
}

// handleCreateScan은 POST /api/v1/discovery/scans를 처리한다.
// 스캔 Job을 만들어 즉시 job id를 반환하고(202), 실제 스캔은 백그라운드에서 돈다.
// 생성은 감사 이벤트를 남긴다(store.CreateScanJob 내부). 허용 역할은 라우트에서 강제한다.
func handleCreateScan(mgr *discovery.Manager) http.HandlerFunc {
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
		if len(req.Ports) == 0 {
			req.Ports = []int{443}
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
			ActorID:     actorID,
			RequestID:   requestIDFrom(ctx),
			SourceIP:    clientIP(r),
		})
		if err != nil {
			if errors.Is(err, discovery.ErrInvalidScanInput) {
				// 오류 메시지는 입력 형식 사유만 담는다(인증서 정보 없음).
				writeError(w, r, http.StatusBadRequest, "invalid_scan_request", err.Error())
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
	ID                 string     `json:"id"`
	Status             string     `json:"status"`
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
