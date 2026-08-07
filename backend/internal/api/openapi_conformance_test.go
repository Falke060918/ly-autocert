package api

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	apispec "github.com/Falke060918/INHERIT_AutoCert/backend/api"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"gopkg.in/yaml.v3"
)

// 명세-코드 정합 검증: 라우트 등록 목록과 api/openapi.yaml의 경로·메서드가
// 정확히 1:1 이어야 한다. 라우트를 추가하고 명세를 안 고치면(또는 그 반대)
// 이 테스트가 실패한다. DB가 필요 없어 항상 실행된다 (`make openapi-check`).

// httpMethods는 OpenAPI Path Item에서 연산으로 취급하는 키다
// (summary/description/parameters 같은 비-연산 키와 구분).
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// specOperations는 임베드된 명세에서 "METHOD /path" 집합을 뽑는다.
// 서버가 실제로 제공하는 바이트(apispec.OpenAPIYAML)를 그대로 파싱하므로
// 명세 파일이 깨져 있으면(YAML 문법 오류) 여기서 바로 실패한다.
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.OpenAPIYAML, &doc); err != nil {
		t.Fatalf("api/openapi.yaml is not valid YAML: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("api/openapi.yaml has no paths")
	}
	ops := make(map[string]bool)
	for path, item := range doc.Paths {
		for key := range item {
			if httpMethods[key] {
				ops[strings.ToUpper(key)+" "+path] = true
			}
		}
	}
	return ops
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestOpenAPISpecMatchesRoutes(t *testing.T) {
	spec := specOperations(t)

	// 라우트 등록만 필요하므로 의존성 없이 만든다 (핸들러는 호출하지 않는다).
	srv := newServer(nil, nil, nil)
	routes := make(map[string]bool, len(srv.routes))
	for _, pattern := range srv.routes {
		routes[pattern] = true
	}

	for _, op := range sortedKeys(routes) {
		if !spec[op] {
			t.Errorf("route %q is registered but missing from api/openapi.yaml", op)
		}
	}
	for _, op := range sortedKeys(spec) {
		if !routes[op] {
			t.Errorf("api/openapi.yaml documents %q but no such route is registered", op)
		}
	}
}

// 서버가 명세를 실제로 제공하는지 통합 검증 (TEST_DATABASE_URL 필요 — 미설정 시 스킵).
// 폐쇄망 제품이므로 명세도 인증 뒤에 둔다: 미인증 401, 인증 후 임베드 원문 그대로.
func TestOpenAPISpecEndpoint(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if resp := env.do(t, http.MethodGet, "/api/v1/openapi.yaml", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated spec fetch = %d, want 401", resp.StatusCode)
	}

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "spec-viewer", pw, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	token, status := env.login(t, "spec-viewer", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	resp := env.do(t, http.MethodGet, "/api/v1/openapi.yaml", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spec fetch = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(apispec.OpenAPIYAML) {
		t.Errorf("served spec differs from embedded api/openapi.yaml (%d vs %d bytes)", len(body), len(apispec.OpenAPIYAML))
	}
}
