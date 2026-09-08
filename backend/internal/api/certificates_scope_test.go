package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCertificateScopeRejectsUnknown(t *testing.T) {
	_, err := parseListParams(httptest.NewRequest("GET", "/api/v1/certificates?scope=intended", nil))
	if err == nil || err.code != "invalid_scope" {
		t.Fatalf("invalid certificate scope accepted: %v", err)
	}
}

func TestCertificateScopeAggregateValidation(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{"summary": handleCertificateSummary(nil), "issuers": handleCertificateIssuers(nil), "risks": handleCertificateRisks(nil)} {
		for _, query := range []string{"scope=", "scope=unknown", "scope=server&scope=inventory"} {
			rr := httptest.NewRecorder()
			handler(rr, httptest.NewRequest("GET", "/api/v1/certificates/"+name+"?"+query, nil))
			if rr.Code != 400 {
				t.Fatalf("%s %s: %d", name, query, rr.Code)
			}
		}
	}
	for _, value := range []string{"", "?scope=server", "?scope=inventory"} {
		if _, err := parseListParams(httptest.NewRequest("GET", "/api/v1/certificates"+value, nil)); err != nil {
			t.Fatal(err)
		}
	}
}
