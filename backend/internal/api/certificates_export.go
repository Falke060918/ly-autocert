package api

// 인증서 인벤토리 CSV 내보내기 (M3 감사 축 — 02-goals.md 성공 기준 5).
// 목록과 같은 검색·필터·정렬 파라미터를 받고 페이지네이션 없이 전체를 내린다.
// 모든 내보내기는 certificate.export 감사 이벤트를 남긴다(store가 같은 트랜잭션으로
// 기록 — 감사 실패 시 내보내기도 실패한다). 응답은 인벤토리 메타데이터뿐이며
// 인증서 원문(PEM/DER)·개인키는 저장 자체를 하지 않으므로 포함되지 않는다.

import (
	"encoding/csv"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// exportCSVHeader는 CSV 열 순서다. exportCSVRow와 일치해야 한다.
var exportCSVHeader = []string{
	"id", "common_name", "subject_dn", "issuer_dn", "serial_number",
	"fingerprint_sha256", "sans", "not_before", "not_after",
	"public_key_algorithm", "public_key_size", "signature_algorithm",
	"certificate_type", "status", "environment", "auto_renew",
	"created_at", "updated_at",
}

func exportCSVRow(c domain.Certificate) []string {
	env := ""
	if c.Environment != nil {
		env = *c.Environment
	}
	return []string{
		c.ID,
		csvGuard(c.CommonName),
		csvGuard(c.SubjectDN),
		csvGuard(c.IssuerDN),
		csvGuard(c.SerialNumber),
		c.FingerprintSHA256,
		csvGuard(strings.Join(c.SANs, " ")),
		c.NotBefore.UTC().Format(time.RFC3339),
		c.NotAfter.UTC().Format(time.RFC3339),
		c.PublicKeyAlgorithm,
		strconv.Itoa(c.PublicKeySize),
		c.SignatureAlgorithm,
		string(c.CertificateType),
		string(c.Status),
		csvGuard(env),
		strconv.FormatBool(c.AutoRenew),
		c.CreatedAt.UTC().Format(time.RFC3339),
		c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// csvGuard는 스프레드시트 수식 주입(=, +, -, @ 시작 셀)을 무력화한다.
// DN·SAN은 업로드된 인증서에서 온 외부 입력이다.
func csvGuard(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// handleExportCertificates는 GET /api/v1/certificates/export를 처리한다.
// 파라미터는 목록과 동일하다 (page/page_size는 무시 — 조건 전체를 내보낸다).
func handleExportCertificates(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		in, perr := parseListParams(r)
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}

		var actorID *string
		if u := userFrom(ctx); u != nil {
			actorID = &u.ID
		}

		items, err := st.ExportCertificates(ctx, store.ExportInput{
			List:      in,
			ActorID:   actorID,
			RequestID: requestIDFrom(ctx),
			SourceIP:  clientIP(r),
		})
		if err != nil {
			slog.Error("certificates export failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(ctx)))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"인증서 내보내기에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}

		filename := "certificates-" + time.Now().UTC().Format("20060102-150405") + ".csv"
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)

		// Excel이 UTF-8을 인식하도록 BOM을 앞세운다.
		if _, err := w.Write([]byte("\uFEFF")); err != nil {
			return
		}
		cw := csv.NewWriter(w)
		if err := cw.Write(exportCSVHeader); err != nil {
			return
		}
		for _, c := range items {
			if err := cw.Write(exportCSVRow(c)); err != nil {
				return
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			// 헤더는 이미 나갔으므로 로그만 남긴다 (인증서 내용은 로그 금지 — 오류만).
			slog.Error("certificates export write failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(ctx)))
		}
	}
}
