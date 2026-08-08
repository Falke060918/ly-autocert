// sensors.go — 센서(원격 수집기) 관리자 API (docs/00-spec.md 7.8, Phase 3a).
// 등록 토큰 평문은 생성 응답에서 **1회만** 내려간다 — 서버는 SHA-256 해시만 저장하므로
// 이후 어떤 응답·로그·감사에도 다시 나타나지 않는다 (보안 하드 규칙 1·3).
// 응답 형태는 frontend/lib/api.ts의 Sensor* 계약(U8 선정의)과 1:1이다.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxSensorRequestBytes는 센서 생성 요청 본문 상한이다 (이름 하나뿐이라 작다).
const maxSensorRequestBytes = 4 << 10 // 4 KiB

// sensorCACommonName은 서버가 부트스트랩하는 센서 mTLS 내부 CA의 CN이다.
const sensorCACommonName = "INHERIT AutoCert Sensor CA"

// errSensorCAKeyMissing은 센서 CA를 새로 만들어야 하는데 봉인 키가 없을 때다.
// 운영자가 조치할 수 있도록 핸들러가 설정 방법을 응답 메시지로 안내한다.
var errSensorCAKeyMissing = errors.New("api: sensor ca encryption key not configured")

type createSensorRequest struct {
	Name string `json:"name"`
}

type sensorCreateResponse struct {
	RequestID string         `json:"request_id"`
	Sensor    *domain.Sensor `json:"sensor"`
	// EnrollmentToken은 이 응답에서만 1회 노출되는 평문 토큰이다 (24h 유효).
	EnrollmentToken      string `json:"enrollment_token"`
	CAFingerprint        string `json:"ca_fingerprint"`
	EnrollCommandExample string `json:"enroll_command_example"`
}

type sensorListResponse struct {
	RequestID string          `json:"request_id"`
	Items     []domain.Sensor `json:"items"`
}

type sensorDetailResponse struct {
	RequestID string         `json:"request_id"`
	Sensor    *domain.Sensor `json:"sensor"`
}

// handleCreateSensor는 POST /api/v1/sensors를 처리한다 (System Administrator 전용 —
// 라우트에서 강제). 센서 생성 + 1회용 등록 토큰 발급. 감사(sensor.token.issue)는
// store.CreateSensor가 같은 트랜잭션으로 남긴다.
// 센서 CA가 아직 없으면 여기서 부트스트랩한다 — 개인키는 sensorCAKey로 봉인한
// 암호문만 저장된다 (sensorca.Seal, 보안 하드 규칙 1).
func handleCreateSensor(st *store.Store, sensorCAKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqIDStr := ""
		if reqID := requestIDFrom(ctx); reqID != nil {
			reqIDStr = *reqID
		}

		var req createSensorRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSensorRequestBytes))
		if err := dec.Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request",
				"요청 본문을 JSON으로 파싱할 수 없습니다.")
			return
		}
		if req.Name == "" {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "센서 이름(name)이 필요합니다.")
			return
		}

		// 토큰 발급 전에 CA를 준비한다 — CA 부트스트랩이 실패하면 센서도 만들지 않는다.
		caFingerprint, err := ensureSensorCAFingerprint(r, st, sensorCAKey)
		if err != nil {
			if errors.Is(err, errSensorCAKeyMissing) {
				writeError(w, r, http.StatusInternalServerError, "sensor_ca_unconfigured",
					"센서 CA 암호화 키가 설정되지 않았습니다. SENSOR_CA_ENC_KEY(hex 64자)를 설정한 뒤 다시 시도하십시오.")
				return
			}
			slog.Error("sensors: ensure sensor ca failed", "error", err.Error(), "request_id", reqIDStr)
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"센서 CA를 준비할 수 없습니다.")
			return
		}

		var actorID *string
		if u := userFrom(ctx); u != nil {
			actorID = &u.ID
		}
		sn, token, err := st.CreateSensor(ctx, store.CreateSensorInput{
			Name:      req.Name,
			ActorID:   actorID,
			RequestID: requestIDFrom(ctx),
			SourceIP:  clientIP(r),
		})
		if err != nil {
			// store 오류 문자열에 토큰 관련 값은 없다 (store 계층 규약).
			slog.Error("sensors: create sensor failed", "error", err.Error(), "request_id", reqIDStr)
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"센서를 생성할 수 없습니다.")
			return
		}

		writeJSON(w, http.StatusCreated, sensorCreateResponse{
			RequestID:       reqIDStr,
			Sensor:          sn,
			EnrollmentToken: token,
			CAFingerprint:   caFingerprint,
			// 서버 주소는 배치 세그먼트에 따라 다르므로 자리표시로 남긴다.
			EnrollCommandExample: fmt.Sprintf(
				"sensor enroll --server https://<서버 주소> --token %s --ca-fingerprint %s",
				token, caFingerprint),
		})
	}
}

// ensureSensorCAFingerprint는 센서 CA 인증서의 SHA-256 지문(hex 64자)을 반환한다.
// CA가 없으면 새로 만들어 저장한다. 동시 부트스트랩 경합은 store 규약을 따른다 —
// SaveSensorCA가 ErrSensorCAExists를 반환하면 진 쪽이 LoadSensorCA로 다시 읽는다.
// 지문 계산에는 인증서(공개 정보)만 쓰므로 기존 CA 로드 경로는 봉인 키가 필요 없다.
func ensureSensorCAFingerprint(r *http.Request, st *store.Store, sensorCAKey []byte) (string, error) {
	ctx := r.Context()
	rec, err := st.LoadSensorCA(ctx)
	if err == nil {
		return sensorCACertFingerprint(rec.CertPEM)
	}
	if !errors.Is(err, store.ErrSensorCANotFound) {
		return "", err
	}

	if len(sensorCAKey) != sensorca.KeySize {
		return "", errSensorCAKeyMissing
	}
	ca, err := sensorca.New(sensorCACommonName)
	if err != nil {
		return "", err
	}
	keyDER, err := ca.KeyDER()
	if err != nil {
		return "", err
	}
	ciphertext, nonce, err := sensorca.Seal(sensorCAKey, keyDER)
	if err != nil {
		return "", err
	}
	saveErr := st.SaveSensorCA(ctx, store.SensorCARecord{
		CertPEM:       ca.CertPEM(),
		KeyCiphertext: ciphertext,
		KeyNonce:      nonce,
	})
	if errors.Is(saveErr, store.ErrSensorCAExists) {
		// 동시 부트스트랩에서 진 쪽 — 이긴 쪽의 CA를 다시 읽는다.
		rec, err := st.LoadSensorCA(ctx)
		if err != nil {
			return "", err
		}
		return sensorCACertFingerprint(rec.CertPEM)
	}
	if saveErr != nil {
		return "", saveErr
	}
	return sensorCACertFingerprint(ca.CertPEM())
}

// sensorCACertFingerprint는 CA 인증서 PEM에서 DER의 SHA-256 지문(hex 소문자)을 만든다.
// 센서 enroll의 --ca-fingerprint 핀과 같은 계산이다 (인증서 DER 전체 해시).
func sensorCACertFingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("api: stored sensor ca cert is not a certificate PEM block")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// handleListSensors는 GET /api/v1/sensors를 처리한다 (전 역할, 읽기 전용 — 감사 없음).
// 응답에 토큰 관련 값은 없다 — domain.Sensor가 해시조차 직렬화에서 제외한다.
func handleListSensors(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqIDStr := ""
		if reqID := requestIDFrom(r.Context()); reqID != nil {
			reqIDStr = *reqID
		}
		items, err := st.ListSensors(r.Context())
		if err != nil {
			slog.Error("sensors: list failed", "error", err.Error())
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"센서 목록을 조회할 수 없습니다.")
			return
		}
		writeJSON(w, http.StatusOK, sensorListResponse{RequestID: reqIDStr, Items: items})
	}
}

// handleGetSensor는 GET /api/v1/sensors/{id}를 처리한다 (전 역할, 읽기 전용).
func handleGetSensor(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !isUUID(id) {
			writeError(w, r, http.StatusNotFound, "not_found", "센서를 찾을 수 없습니다.")
			return
		}
		sn, err := st.GetSensor(r.Context(), id)
		if errors.Is(err, store.ErrSensorNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "센서를 찾을 수 없습니다.")
			return
		}
		if err != nil {
			slog.Error("sensors: get failed", "error", err.Error())
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"센서를 조회할 수 없습니다.")
			return
		}
		reqIDStr := ""
		if reqID := requestIDFrom(r.Context()); reqID != nil {
			reqIDStr = *reqID
		}
		writeJSON(w, http.StatusOK, sensorDetailResponse{RequestID: reqIDStr, Sensor: sn})
	}
}

// handleDisableSensor는 POST /api/v1/sensors/{id}/disable을 처리한다
// (System Administrator 전용 — 라우트에서 강제). pending 센서의 토큰 회수 겸용이다.
// 감사(sensor.disable)는 store.DisableSensor가 같은 트랜잭션으로 남긴다.
func handleDisableSensor(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !isUUID(id) {
			writeError(w, r, http.StatusNotFound, "not_found", "센서를 찾을 수 없습니다.")
			return
		}
		ctx := r.Context()
		var actorID *string
		if u := userFrom(ctx); u != nil {
			actorID = &u.ID
		}
		sn, err := st.DisableSensor(ctx, store.DisableSensorInput{
			SensorID:  id,
			ActorID:   actorID,
			RequestID: requestIDFrom(ctx),
			SourceIP:  clientIP(r),
		})
		if errors.Is(err, store.ErrSensorNotFound) {
			// 이미 disabled인 경우도 같은 404다 (store 규약 — 멱등 재시도 시 구분 불필요).
			writeError(w, r, http.StatusNotFound, "not_found", "센서를 찾을 수 없습니다.")
			return
		}
		if err != nil {
			slog.Error("sensors: disable failed", "error", err.Error())
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"센서를 비활성화할 수 없습니다.")
			return
		}
		reqIDStr := ""
		if reqID := requestIDFrom(ctx); reqID != nil {
			reqIDStr = *reqID
		}
		writeJSON(w, http.StatusOK, sensorDetailResponse{RequestID: reqIDStr, Sensor: sn})
	}
}
