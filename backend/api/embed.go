// Package apispec은 OpenAPI 3 명세 파일(openapi.yaml)을 바이너리에 임베드한다.
// 서버가 GET /api/v1/openapi.yaml 로 이 바이트를 그대로 제공하므로,
// 배포된 서버가 주는 명세 = 빌드에 포함된 명세다.
// 명세와 실제 라우트의 정합은 internal/api의 정합 테스트가 검증한다
// (경로·메서드 누락/초과 검출 — `make openapi-check` 또는 go test).
package apispec

import _ "embed"

//go:embed openapi.yaml
var OpenAPIYAML []byte
