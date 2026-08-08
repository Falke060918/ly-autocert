package sensorgw

import (
	"sort"
	"strings"
	"testing"

	apispec "github.com/Falke060918/INHERIT_AutoCert/backend/api"
	"gopkg.in/yaml.v3"
)

// 명세-코드 정합 검증: 게이트웨이 라우트 등록 목록과 api/sensor-openapi.yaml의
// 경로·메서드가 정확히 1:1 이어야 한다 (internal/api의 openapi_conformance_test.go 미러).
// DB가 필요 없어 항상 실행된다.

// httpMethods는 OpenAPI Path Item에서 연산으로 취급하는 키다.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// specOperations는 임베드된 센서 명세에서 "METHOD /path" 집합을 뽑는다.
// 명세 파일이 깨져 있으면(YAML 문법 오류) 여기서 바로 실패한다.
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.SensorOpenAPIYAML, &doc); err != nil {
		t.Fatalf("api/sensor-openapi.yaml is not valid YAML: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("api/sensor-openapi.yaml has no paths")
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

func TestSensorOpenAPISpecMatchesRoutes(t *testing.T) {
	spec := specOperations(t)

	// 라우트 등록만 필요하므로 의존성 없이 만든다 (핸들러는 호출하지 않는다).
	gw := newGateway(nil, nil, nil)
	routes := make(map[string]bool, len(gw.routes))
	for _, pattern := range gw.routes {
		routes[pattern] = true
	}

	for _, op := range sortedKeys(routes) {
		if !spec[op] {
			t.Errorf("route %q is registered but missing from api/sensor-openapi.yaml", op)
		}
	}
	for _, op := range sortedKeys(spec) {
		if !routes[op] {
			t.Errorf("api/sensor-openapi.yaml documents %q but no such route is registered", op)
		}
	}
}
