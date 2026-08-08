// handlers.go — /sensor/v1/* 핸들러 (docs/00-spec.md 7.8).
// 요청·응답 DTO는 internal/sensorwire가 단일 계약이다. 상태 전이는 store의
// 리스 상태 머신이 가드하고, 여기서는 store 오류를 HTTP 상태로만 매핑한다.
package sensorgw

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

const (
	// maxRequestBytes는 일반 요청(enroll·renew·전이 보고) 본문 상한이다.
	maxRequestBytes = 1 << 20
	// maxObservationsBytes는 관측 배치 본문 상한이다.
	// wire 상한 1000건 × 체인 수 개 × base64(DER) 기준 여유를 둔다.
	maxObservationsBytes = 32 << 20
)

// handleEnroll은 센서 최초 등록이다: 1회용 토큰 + CSR → 클라이언트 인증서 발급.
// 토큰 소진(원자적)과 감사는 store.EnrollSensor가 같은 트랜잭션으로 처리하고,
// 토큰 이전 단계의 실패(형식·CSR 불량)도 여기서 감사를 남긴다 (00-spec 7.8 등록 흐름).
func (g *Gateway) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req sensorwire.EnrollRequest
	if !decodeJSON(w, r, maxRequestBytes, &req) {
		g.auditEnrollFailure(r, "invalid_request")
		return
	}
	if err := req.Validate(); err != nil {
		g.auditEnrollFailure(r, "invalid_request")
		writeError(w, r, http.StatusBadRequest, "invalid_request", "등록 요청 형식이 올바르지 않습니다.")
		return
	}

	// 인증서를 먼저 서명해 fingerprint를 얻는다 — 토큰 검증(EnrollSensor)이
	// 실패하면 인증서는 응답으로 나가지 않고 버려진다 (DB에 남는 것도 없다).
	certPEM, err := g.ca.SignClientCSR([]byte(req.CSRPEM))
	if err != nil {
		g.auditEnrollFailure(r, "invalid_csr")
		writeError(w, r, http.StatusBadRequest, "invalid_csr", "CSR을 처리할 수 없습니다.")
		return
	}
	fp, err := fingerprintFromCertPEM(certPEM)
	if err != nil {
		slog.Error("sensorgw: fingerprint issued certificate failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
		return
	}

	sn, err := g.st.EnrollSensor(r.Context(), store.EnrollSensorInput{
		Token:                       req.Token,
		ClientCertFingerprintSHA256: fp,
		RequestID:                   requestIDFrom(r.Context()),
		SourceIP:                    clientIP(r),
	})
	if errors.Is(err, store.ErrEnrollTokenInvalid) {
		// 무효·소진·만료를 구분하지 않는다 (store 규약 — 응답 구분은 토큰 추측 단서).
		writeError(w, r, http.StatusUnauthorized, "invalid_token", "등록 토큰이 유효하지 않습니다.")
		return
	}
	if err != nil {
		slog.Error("sensorgw: enroll failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
		return
	}

	writeJSON(w, http.StatusOK, sensorwire.EnrollResponse{
		SensorID:      sn.ID,
		ClientCertPEM: string(certPEM),
		CACertPEM:     string(g.ca.CertPEM()),
	})
}

// handleRenew는 클라이언트 인증서 갱신이다: 새 CSR 서명 → fingerprint 원자 교체.
// 현재 mTLS 인증서의 fingerprint가 아이덴티티 증명이다. 교체 경합(이미 갱신됨)은 409.
func (g *Gateway) handleRenew(w http.ResponseWriter, r *http.Request) {
	var req sensorwire.RenewRequest
	if !decodeJSON(w, r, maxRequestBytes, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "갱신 요청 형식이 올바르지 않습니다.")
		return
	}
	certPEM, err := g.ca.SignClientCSR([]byte(req.CSRPEM))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_csr", "CSR을 처리할 수 없습니다.")
		return
	}
	newFP, err := fingerprintFromCertPEM(certPEM)
	if err != nil {
		slog.Error("sensorgw: fingerprint renewed certificate failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
		return
	}

	_, err = g.st.RenewSensorFingerprint(r.Context(), store.RenewSensorInput{
		OldFingerprint: clientFingerprintFrom(r.Context()),
		NewFingerprint: newFP,
		RequestID:      requestIDFrom(r.Context()),
		SourceIP:       clientIP(r),
	})
	if errors.Is(err, store.ErrSensorNotFound) {
		// 동시 갱신 경합 — 현재 인증서가 더 이상 등록된 fingerprint가 아니다.
		writeError(w, r, http.StatusConflict, "renew_conflict", "인증서가 이미 교체되었습니다.")
		return
	}
	if err != nil {
		slog.Error("sensorgw: renew failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, sensorwire.RenewResponse{ClientCertPEM: string(certPEM)})
}

// handleLease는 pull 리스다: 자기(sensor_id) pending Job 1건을 leased로 가져간다.
// 배정할 작업이 없으면 204 No Content (wire 규약).
func (g *Gateway) handleLease(w http.ResponseWriter, r *http.Request) {
	sn := sensorFrom(r.Context())
	l, err := g.st.LeaseNextSensorJob(r.Context(), sn.ID)
	if errors.Is(err, store.ErrNoPendingSensorJob) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		slog.Error("sensorgw: lease failed", "error", err.Error(), "sensor_id", sn.ID)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, sensorwire.LeaseResponse{
		JobID:          l.JobID,
		JobType:        l.JobType,
		Targets:        l.Targets,
		Ports:          l.Ports,
		TimeoutMs:      l.TimeoutMs,
		Concurrency:    l.Concurrency,
		LeaseExpiresAt: l.LeaseExpiresAt,
	})
}

// handleJobStart는 leased → running 전이 보고다 (총 엔드포인트 수 기록 + 리스 연장).
func (g *Gateway) handleJobStart(w http.ResponseWriter, r *http.Request) {
	jobID, ok := jobIDFromPath(w, r)
	if !ok {
		return
	}
	var req sensorwire.JobStartRequest
	if !decodeJSON(w, r, maxRequestBytes, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "전이 보고 형식이 올바르지 않습니다.")
		return
	}
	sn := sensorFrom(r.Context())
	g.writeJobTransition(w, r, "start",
		g.st.MarkSensorJobRunning(r.Context(), jobID, sn.ID, req.TotalEndpoints))
}

// handleHeartbeat는 running 중 리스 연장이다. running이 아니면 409 —
// 리스 만료로 이미 회수된 Job의 하트비트도 여기서 걸린다.
func (g *Gateway) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	jobID, ok := jobIDFromPath(w, r)
	if !ok {
		return
	}
	var req sensorwire.HeartbeatRequest
	if !decodeJSON(w, r, maxRequestBytes, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "하트비트 형식이 올바르지 않습니다.")
		return
	}
	sn := sensorFrom(r.Context())
	g.writeJobTransition(w, r, "heartbeat",
		g.st.ExtendSensorJobLease(r.Context(), jobID, sn.ID))
}

// handleObservations는 관측 배치 반영이다: base64(DER) → x509 파싱 →
// RecordObservationBatch가 (job_id, ip, port) 아비터로 멱등 처리한다.
// 같은 배치를 다시 POST해도 결과가 중복되지 않는다 (00-spec 7.8 결과 수집).
func (g *Gateway) handleObservations(w http.ResponseWriter, r *http.Request) {
	jobID, ok := jobIDFromPath(w, r)
	if !ok {
		return
	}
	var batch sensorwire.ObservationBatch
	if !decodeJSON(w, r, maxObservationsBytes, &batch) {
		return
	}
	if err := batch.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "관측 배치 형식이 올바르지 않습니다.")
		return
	}

	obs := make([]store.EndpointObservation, 0, len(batch.Observations))
	for i, o := range batch.Observations {
		chain, err := parseChainDER(o.ChainDER)
		if err != nil {
			// 항목 위치만 알린다 — 인증서 바이트는 싣지 않는다 (보안 하드 규칙 3).
			writeError(w, r, http.StatusBadRequest, "invalid_chain",
				fmt.Sprintf("관측 %d번의 인증서 체인을 해석할 수 없습니다.", i))
			return
		}
		obs = append(obs, store.EndpointObservation{
			IP:         o.IP,
			Port:       o.Port,
			Reachable:  o.Reachable,
			TLSVersion: o.TLSVersion,
			Hostname:   o.Hostname,
			Chain:      chain,
		})
	}

	sn := sensorFrom(r.Context())
	_, err := g.st.RecordObservationBatch(r.Context(), jobID, sn.ID, obs)
	g.writeJobTransition(w, r, "observations", err)
}

// handleComplete는 종결 보고다: completed는 running에서만, failed는 leased/running에서.
// 터미널 상태의 Job에 대한 재보고는 409 (store 가드).
func (g *Gateway) handleComplete(w http.ResponseWriter, r *http.Request) {
	jobID, ok := jobIDFromPath(w, r)
	if !ok {
		return
	}
	var req sensorwire.CompleteRequest
	if !decodeJSON(w, r, maxRequestBytes, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "종결 보고 형식이 올바르지 않습니다.")
		return
	}
	var scanErr *string
	if req.Status == sensorwire.CompleteFailed {
		msg := req.Error
		if msg == "" {
			msg = "sensor reported failure"
		}
		scanErr = &msg
	}
	sn := sensorFrom(r.Context())
	g.writeJobTransition(w, r, "complete",
		g.st.MarkSensorJobFinished(r.Context(), jobID, sn.ID, string(req.Status), scanErr))
}

// writeJobTransition은 리스 상태 머신 호출 결과를 HTTP 상태로 매핑한다.
// 성공 204 / Job 부재 404 / 전이 불가(터미널 포함) 409 / 그 외 500.
func (g *Gateway) writeJobTransition(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrScanJobNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "스캔 작업을 찾을 수 없습니다.")
	case errors.Is(err, store.ErrSensorJobState):
		writeError(w, r, http.StatusConflict, "state_conflict", "허용되지 않는 작업 상태 전이입니다.")
	default:
		slog.Error("sensorgw: job transition failed", "op", op, "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
	}
}

// jobIDFromPath는 {id} 경로값을 UUID 형식 검증과 함께 꺼낸다. 불량이면 404.
func jobIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "스캔 작업을 찾을 수 없습니다.")
		return "", false
	}
	return id, true
}

// fingerprintFromCertPEM은 발급 인증서 PEM의 SHA-256 지문(소문자 hex)을 만든다.
func fingerprintFromCertPEM(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("sensorgw: issued certificate is not a certificate PEM block")
	}
	return certFingerprint(block.Bytes), nil
}

// parseChainDER는 base64(DER) 배열을 x509 인증서 슬라이스로 파싱한다.
// 오류 메시지에 인증서 바이트를 넣지 않는다 (보안 하드 규칙 3).
func parseChainDER(chainDER []string) ([]*x509.Certificate, error) {
	if len(chainDER) == 0 {
		return nil, nil
	}
	chain := make([]*x509.Certificate, 0, len(chainDER))
	for _, b64 := range chainDER {
		der, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, errors.New("sensorgw: chain item is not valid base64")
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, errors.New("sensorgw: chain item is not a valid certificate")
		}
		chain = append(chain, cert)
	}
	return chain, nil
}

// auditEnrollFailure는 토큰 대조 이전 단계의 등록 실패를 감사로 남긴다
// (토큰 무효 실패는 store.EnrollSensor가 동일 트랜잭션으로 기록한다).
// 사유 코드만 담고 토큰·CSR 내용은 담지 않는다.
func (g *Gateway) auditEnrollFailure(r *http.Request, reason string) {
	if g.rec == nil {
		return
	}
	if err := g.rec.Record(r.Context(), audit.Event{
		Type:       store.EventSensorEnroll,
		TargetType: "sensor",
		RequestID:  requestIDFrom(r.Context()),
		After:      map[string]string{"reason": reason},
		Result:     domain.AuditFailure,
		SourceIP:   clientIP(r),
	}); err != nil {
		slog.Error("sensorgw: audit enroll failure failed", "error", err.Error())
	}
}
