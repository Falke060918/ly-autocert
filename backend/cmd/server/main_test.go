// main_test.go — 기동 배선 회귀 테스트.
// 핵심 규약: 센서 게이트웨이 설정이 잘못돼도 코어 API(8080)는 뜬다.
package main

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// TestStartSensorGatewayFailureKeepsCoreRunning은 게이트웨이 구성 실패가
// 프로세스를 죽이지 않고 게이트웨이만 비활성화하는지 본다.
// 주입하는 오류는 SENSOR_ADVERTISED_HOSTS가 빈 실제 실패(sensorca SAN 필수)다 —
// 이 조합에서 백엔드가 exit(1) 하던 회귀를 막는다.
func TestStartSensorGatewayFailureKeepsCoreRunning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	srv, stop := startSensorGateway(logger, func() (*http.Server, func(), error) {
		return nil, nil, errors.New("sensorca: at least one SAN (dns name or ip) is required")
	})
	if srv != nil {
		t.Fatalf("gateway server = %v, want nil on startup failure", srv)
	}
	if stop == nil {
		t.Fatal("stop function is nil — the deferred call in run() would panic")
	}
	stop() // no-op이어야 한다(패닉 금지).

	if !strings.Contains(buf.String(), "SENSOR_ADVERTISED_HOSTS") {
		t.Errorf("warning must name SENSOR_ADVERTISED_HOSTS, got: %s", buf.String())
	}
}

// TestStartSensorGatewaySuccess는 정상 구성 시 서버와 정지 함수를 그대로 넘기는지 본다(대조군).
func TestStartSensorGatewaySuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	want := &http.Server{Addr: ":8443"}
	stopped := false

	got, stop := startSensorGateway(logger, func() (*http.Server, func(), error) {
		return want, func() { stopped = true }, nil
	})
	if got != want {
		t.Fatalf("gateway server = %v, want %v", got, want)
	}
	stop()
	if !stopped {
		t.Error("reaper stop function was not passed through")
	}
}
