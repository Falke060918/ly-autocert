package api

// parseListParams 단위 테스트 — DB 불필요.

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

func TestParseListParamsDefaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/certificates", nil)
	in, perr := parseListParams(r)
	if perr != nil {
		t.Fatalf("perr = %+v, want nil", perr)
	}
	if in.Search != "" || len(in.Statuses) != 0 || len(in.Types) != 0 ||
		in.Expired != nil || in.ExpiresWithinDays != nil ||
		in.SortField != "" || in.SortDesc || in.Page != 1 || in.PageSize != 50 {
		t.Errorf("defaults = %+v", in)
	}
}

func TestParseListParamsFull(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/certificates?q=web"+
		"&status=active,expired&type=server&expires_within_days=30&expired=false"+
		"&sort=common_name&order=desc&page=2&page_size=10", nil)
	in, perr := parseListParams(r)
	if perr != nil {
		t.Fatalf("perr = %+v, want nil", perr)
	}
	if in.Search != "web" {
		t.Errorf("search = %q", in.Search)
	}
	if len(in.Statuses) != 2 || in.Statuses[0] != domain.CertStatusActive || in.Statuses[1] != domain.CertStatusExpired {
		t.Errorf("statuses = %v", in.Statuses)
	}
	if len(in.Types) != 1 || in.Types[0] != domain.CertTypeServer {
		t.Errorf("types = %v", in.Types)
	}
	if in.ExpiresWithinDays == nil || *in.ExpiresWithinDays != 30 {
		t.Errorf("expires_within_days = %v", in.ExpiresWithinDays)
	}
	if in.Expired == nil || *in.Expired != false {
		t.Errorf("expired = %v", in.Expired)
	}
	if in.SortField != "common_name" || !in.SortDesc {
		t.Errorf("sort = %q desc=%v", in.SortField, in.SortDesc)
	}
	if in.Page != 2 || in.PageSize != 10 {
		t.Errorf("page/page_size = %d/%d", in.Page, in.PageSize)
	}
}

func TestParseListParamsDerivedFlags(t *testing.T) {
	// self_signed·unowned는 대시보드(이슈 #8) 숫자 → 목록 이동을 위한 파생 필터다.
	r := httptest.NewRequest("GET", "/api/v1/certificates?self_signed=true&unowned=false", nil)
	in, perr := parseListParams(r)
	if perr != nil {
		t.Fatalf("perr = %+v, want nil", perr)
	}
	if in.SelfSigned == nil || *in.SelfSigned != true {
		t.Errorf("self_signed = %v, want true", in.SelfSigned)
	}
	if in.Unowned == nil || *in.Unowned != false {
		t.Errorf("unowned = %v, want false", in.Unowned)
	}

	// 미지정이면 nil (필터 없음).
	r = httptest.NewRequest("GET", "/api/v1/certificates", nil)
	in, _ = parseListParams(r)
	if in.SelfSigned != nil || in.Unowned != nil {
		t.Errorf("defaults self_signed/unowned = %v/%v, want nil/nil", in.SelfSigned, in.Unowned)
	}
}

func TestParseListParamsRepeatedFilterValues(t *testing.T) {
	// 반복 파라미터와 쉼표 구분 혼용을 모두 허용한다.
	r := httptest.NewRequest("GET", "/api/v1/certificates?status=active&status=revoked", nil)
	in, perr := parseListParams(r)
	if perr != nil {
		t.Fatalf("perr = %+v, want nil", perr)
	}
	if len(in.Statuses) != 2 {
		t.Errorf("statuses = %v, want 2 values", in.Statuses)
	}
}

func TestParseListParamsInvalid(t *testing.T) {
	cases := map[string]struct {
		query    string
		wantCode string
	}{
		"unknown status":           {"status=nope", "invalid_status"},
		"unknown type":             {"type=zzz", "invalid_type"},
		"zero expires_within_days": {"expires_within_days=0", "invalid_expires_within_days"},
		"non-numeric expires":      {"expires_within_days=abc", "invalid_expires_within_days"},
		"bad expired":              {"expired=maybe", "invalid_expired"},
		"bad self_signed":          {"self_signed=maybe", "invalid_self_signed"},
		"bad unowned":              {"unowned=maybe", "invalid_unowned"},
		"non-whitelisted sort":     {"sort=fingerprint_sha256", "invalid_sort"},
		"bad order":                {"order=up", "invalid_order"},
		"zero page":                {"page=0", "invalid_page"},
		"non-numeric page":         {"page=abc", "invalid_page"},
		"oversized page_size":      {"page_size=1000", "invalid_page_size"},
		"zero page_size":           {"page_size=0", "invalid_page_size"},
		"overlong search":          {"q=" + strings.Repeat("a", 201), "invalid_search"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/certificates?"+tc.query, nil)
			if _, perr := parseListParams(r); perr == nil || perr.code != tc.wantCode {
				t.Errorf("perr = %+v, want code %s", perr, tc.wantCode)
			}
		})
	}
}

func TestParseListParamsIssuerFilters(t *testing.T) {
	r := httptest.NewRequest("GET",
		"/api/v1/certificates?issuer_org=Acme%2C+Inc.&issued_by=6f2f9a1e-1b7c-4a44-9d2f-0a1b2c3d4e5f", nil)
	in, perr := parseListParams(r)
	if perr != nil {
		t.Fatalf("perr = %+v, want nil", perr)
	}
	if in.IssuerOrg == nil || *in.IssuerOrg != "Acme, Inc." {
		t.Errorf("issuer_org = %v", in.IssuerOrg)
	}
	if in.IssuedBy == nil || *in.IssuedBy != "6f2f9a1e-1b7c-4a44-9d2f-0a1b2c3d4e5f" {
		t.Errorf("issued_by = %v", in.IssuedBy)
	}

	// UUID가 아닌 issued_by는 DB ::uuid 캐스트 500이 아니라 400이어야 한다.
	r = httptest.NewRequest("GET", "/api/v1/certificates?issued_by=not-a-uuid", nil)
	if _, perr := parseListParams(r); perr == nil || perr.code != "invalid_issued_by" {
		t.Errorf("invalid issued_by perr = %+v, want invalid_issued_by", perr)
	}

	// 파라미터가 존재하며 빈 값이면 "조직 미상"(issuer_organization IS NULL)
	// 필터다 — 분포의 "조직 미상" 행 클릭 경로(프론트가 issuer_org=로 보낸다).
	r = httptest.NewRequest("GET", "/api/v1/certificates?issuer_org=", nil)
	in, perr = parseListParams(r)
	if perr != nil {
		t.Fatalf("perr = %+v, want nil", perr)
	}
	if in.IssuerOrg == nil || *in.IssuerOrg != "" {
		t.Errorf("issuer_org(empty) = %v, want pointer to \"\"", in.IssuerOrg)
	}

	// 파라미터 부재는 nil(필터 없음) — 빈 값과 구분된다.
	r = httptest.NewRequest("GET", "/api/v1/certificates", nil)
	in, _ = parseListParams(r)
	if in.IssuerOrg != nil {
		t.Errorf("issuer_org(absent) = %v, want nil", in.IssuerOrg)
	}
}
