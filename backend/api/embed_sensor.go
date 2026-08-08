// embed_sensor.go — 센서 게이트웨이(8443, /sensor/v1/*) OpenAPI 3 명세 임베드.
// 관리자 API 명세(openapi.yaml)와 별개 파일이다 — 인증 방식(mTLS)과 리스너가 다르다.
// 명세와 실제 라우트의 정합은 internal/sensorgw의 정합 테스트가 검증한다.
package apispec

import _ "embed"

//go:embed sensor-openapi.yaml
var SensorOpenAPIYAML []byte
