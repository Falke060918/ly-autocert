package api

import (
	"strings"
	"testing"
)

// TestEnrollCommandExampleUsesConfiguredPort는 등록 명령 예시가 **설정된** 게이트웨이
// 포트를 쓰는지 본다. 예시가 기본 포트로 굳어 있던 시절에는 SENSOR_LISTEN_ADDR를
// 바꾼 배치에서 운영자가 명령을 그대로 복사해 접속에 실패했다.
func TestEnrollCommandExampleUsesConfiguredPort(t *testing.T) {
	const (
		token       = "test-token"
		fingerprint = "test-fingerprint"
	)
	tests := []struct {
		name     string
		port     string
		wantHost string
	}{
		{name: "configured port", port: "9443", wantHost: "https://<서버 주소>:9443"},
		{name: "empty falls back to default", port: "", wantHost: "https://<서버 주소>:" + defaultSensorGatewayPort},
		{name: "whitespace falls back to default", port: "  ", wantHost: "https://<서버 주소>:" + defaultSensorGatewayPort},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := enrollCommandExample(tc.port, token, fingerprint)
			if !strings.Contains(got, "--server "+tc.wantHost+" ") {
				t.Errorf("enroll example = %q, want --server %s", got, tc.wantHost)
			}
			if !strings.Contains(got, token) || !strings.Contains(got, fingerprint) {
				t.Errorf("enroll example = %q, want it to carry token and fingerprint", got)
			}
		})
	}
}

// TestWithSensorGatewayPortOption은 옵션 주입이 라우트 등록 전에 반영되는지 본다
// (newServer는 옵션을 먼저 적용한 뒤 핸들러를 만든다). DB 없이 도는 검사다.
func TestWithSensorGatewayPortOption(t *testing.T) {
	if got := newServer(nil, nil, nil, nil).sensorGatewayPort; got != defaultSensorGatewayPort {
		t.Errorf("default sensorGatewayPort = %q, want %q", got, defaultSensorGatewayPort)
	}
	if got := newServer(nil, nil, nil, nil, WithSensorGatewayPort("9443")).sensorGatewayPort; got != "9443" {
		t.Errorf("sensorGatewayPort = %q, want 9443", got)
	}
	// 빈 값은 배선 없음과 같다 — 기본 포트를 지운 채로 남기지 않는다.
	if got := newServer(nil, nil, nil, nil, WithSensorGatewayPort("")).sensorGatewayPort; got != defaultSensorGatewayPort {
		t.Errorf("empty option overrode the default: %q", got)
	}
}
