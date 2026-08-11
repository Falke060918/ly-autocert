package api

// 스캔 기본값 노출 (GET /api/v1/discovery/defaults).
//
// 환경변수(SCAN_SWEEP_*)를 바꾼 현장에서 화면의 placeholder가 거짓말하지 않게
// 하는 것이 목적이다. 프론트가 기본값을 상수로 들고 있으면 "서버 기본값(10)"이라
// 적힌 칸에 실제로는 40이 적용되는 상태가 되고, 그 어긋남은 화면 어디에도 안 뜬다.
//
// 여기 실리는 값은 **주입된 설정이 실제로 적용된 유효값**이다(미주입 필드는 store
// 기본값으로 폴백 — 스윕 생성 경로와 같은 규칙). 비밀값은 없다: 레이트·청크·상한뿐이다.
//
// 단발 스캔의 프로브 속도(discovery.DefaultScanRatePPS)는 싣지 않는다 — 요청으로
// 바꿀 수 없는 내부 상수라 화면에 노출할 자리가 없다.
// 읽기 전용이라 전 역할이며 감사 이벤트를 남기지 않는다 (00-spec 7.12).

import (
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// sweepDefaultsBody는 스윕 생성 폼의 기본값이다(SCAN_SWEEP_*).
type sweepDefaultsBody struct {
	RatePPS        int `json:"rate_pps"`
	ChunkEndpoints int `json:"chunk_endpoints"`
	MaxDays        int `json:"max_days"`
}

// scanDefaultsBody는 포트 미지정 시 서버가 적용하는 사양이다.
type scanDefaultsBody struct {
	PortSpec string `json:"port_spec"`
}

// discoveryLimitsBody는 요청이 넘을 수 없는 상한이다(입력 검증 안내용).
type discoveryLimitsBody struct {
	MaxRatePPS   int `json:"max_rate_pps"`
	MaxEndpoints int `json:"max_endpoints"`
}

type discoveryDefaultsResponse struct {
	RequestID string              `json:"request_id"`
	Sweep     sweepDefaultsBody   `json:"sweep"`
	Scan      scanDefaultsBody    `json:"scan"`
	Limits    discoveryLimitsBody `json:"limits"`
}

// handleDiscoveryDefaults는 GET /api/v1/discovery/defaults다.
func (s *Server) handleDiscoveryDefaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, discoveryDefaultsResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Sweep: sweepDefaultsBody{
			// 스윕 생성이 쓰는 폴백과 같은 순서다(요청값 → 주입 설정 → store 기본값).
			RatePPS:        orDefaultInt(s.sweepDefaults.RatePPS, store.DefaultSweepRatePPS),
			ChunkEndpoints: orDefaultInt(s.sweepDefaults.ChunkEndpoints, store.DefaultSweepChunkEndpoints),
			MaxDays:        orDefaultInt(s.sweepDefaults.MaxDays, store.DefaultSweepMaxDays),
		},
		Scan: scanDefaultsBody{PortSpec: discovery.DefaultPortSpec},
		Limits: discoveryLimitsBody{
			MaxRatePPS:   discovery.MaxRatePPS,
			MaxEndpoints: discovery.MaxEndpoints,
		},
	})
}
