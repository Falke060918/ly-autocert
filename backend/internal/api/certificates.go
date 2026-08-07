package api

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxUploadBytes는 업로드 본문 상한이다. 인증서 체인은 수 KB 수준이므로 충분하다.
const maxUploadBytes = 1 << 20 // 1 MiB

type importedCertJSON struct {
	ID                string                   `json:"id"`
	FingerprintSHA256 string                   `json:"fingerprint_sha256"`
	CommonName        string                   `json:"common_name"`
	NotBefore         time.Time                `json:"not_before"`
	NotAfter          time.Time                `json:"not_after"`
	CertificateType   domain.CertificateType   `json:"certificate_type"`
	Status            domain.CertificateStatus `json:"status"`
	Duplicate         bool                     `json:"duplicate"`
}

type importResponse struct {
	RequestID  string             `json:"request_id"`
	Imported   []importedCertJSON `json:"imported"`
	LocationID string             `json:"location_id"`
}

// handleImportCertificates는 POST /api/v1/certificates/import를 처리한다.
// PEM(체인 포함)·DER 업로드를 파싱해 fingerprint 기준으로 중복 판정하고,
// 중복이면 새 Certificate 없이 설치 위치만 추가한다 (이슈 #3).
// 업로드 내용(인증서·키 바이트)은 어떤 경로로도 로그에 남기지 않는다 (보안 하드 규칙 3).
func handleImportCertificates(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := requestIDFrom(ctx)
		srcIP := clientIP(r)

		// 거부된 업로드도 감사 이벤트를 남긴다 (보안 하드 규칙 5). 사유 코드만 기록.
		auditFailure := func(reason string) {
			if auditErr := st.RecordImportFailure(ctx, reason, &reqID, srcIP); auditErr != nil {
				slog.Error("audit: record import failure", "error", auditErr.Error(), "request_id", reqID)
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
		data, fileName, err := readUpload(r)
		if err != nil {
			auditFailure("invalid_upload")
			writeError(w, r, http.StatusBadRequest, "invalid_upload",
				"업로드 본문을 읽을 수 없습니다. multipart/form-data의 file 필드 또는 원시 본문으로 보내십시오.")
			return
		}
		if len(data) == 0 {
			auditFailure("empty_upload")
			writeError(w, r, http.StatusBadRequest, "empty_upload", "업로드된 파일이 비어 있습니다.")
			return
		}

		certs, err := certparse.Parse(data)
		if err != nil {
			reason := "invalid_certificate"
			if errors.Is(err, certparse.ErrPrivateKeyFound) {
				reason = "private_key_rejected"
			}
			auditFailure(reason)
			if reason == "private_key_rejected" {
				// 개인키는 저장·로그 없이 전체 거부한다 (보안 하드 규칙 1·3).
				writeError(w, r, http.StatusBadRequest, "private_key_rejected",
					"업로드에 개인키가 포함되어 있어 거부되었습니다. 개인키를 제외하고 인증서만 업로드하십시오.")
				return
			}
			writeError(w, r, http.StatusBadRequest, "invalid_certificate",
				"인증서를 파싱할 수 없습니다. 지원 형식: PEM(체인 포함), DER.")
			return
		}

		now := time.Now()
		domainCerts := make([]domain.Certificate, len(certs))
		for i, c := range certs {
			domainCerts[i] = certparse.Describe(c, now)
		}

		res, err := st.ImportCertificates(ctx, store.ImportInput{
			Certificates: domainCerts,
			FileName:     fileName,
			RequestID:    &reqID,
			SourceIP:     srcIP,
		})
		if err != nil {
			// store 오류에는 인증서 본문이 포함되지 않는다.
			slog.Error("certificates import failed", "error", err.Error(), "request_id", reqID)
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"인증서 저장에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}

		resp := importResponse{RequestID: reqID, LocationID: res.LocationID}
		status := http.StatusOK // 전부 중복 — 위치만 추가됨
		for _, ic := range res.Certificates {
			if ic.Created {
				status = http.StatusCreated
			}
			resp.Imported = append(resp.Imported, importedCertJSON{
				ID:                ic.Certificate.ID,
				FingerprintSHA256: ic.Certificate.FingerprintSHA256,
				CommonName:        ic.Certificate.CommonName,
				NotBefore:         ic.Certificate.NotBefore,
				NotAfter:          ic.Certificate.NotAfter,
				CertificateType:   ic.Certificate.CertificateType,
				Status:            ic.Certificate.Status,
				Duplicate:         !ic.Created,
			})
		}
		writeJSON(w, status, resp)
	}
}

// readUpload는 multipart(file 필드) 또는 원시 본문에서 업로드 바이트를 읽는다.
func readUpload(r *http.Request) (data []byte, fileName *string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		f, hdr, err := r.FormFile("file")
		if err != nil {
			return nil, nil, err
		}
		defer f.Close() //nolint:errcheck
		data, err = io.ReadAll(f)
		if err != nil {
			return nil, nil, err
		}
		if hdr.Filename != "" {
			name := hdr.Filename
			fileName = &name
		}
		return data, fileName, nil
	}
	data, err = io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, err
	}
	return data, nil, nil
}

// clientIP는 RemoteAddr에서 IP만 추출한다. 파싱 불가면 nil (audit source_ip NULL).
func clientIP(r *http.Request) *string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if net.ParseIP(host) == nil {
		return nil
	}
	return &host
}
