package api

import (
	"net/http"

	apispec "github.com/Falke060918/INHERIT_AutoCert/backend/api"
)

// handleOpenAPISpec은 빌드에 임베드된 OpenAPI 명세 원문을 그대로 반환한다.
// 명세-코드 정합은 openapi_conformance_test.go가 보장한다 (`make openapi-check`).
func handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(apispec.OpenAPIYAML) // 쓰기 실패는 클라이언트 연결 종료뿐 — 무시
}
