// main_test.go — 기동 배선 회귀 테스트.
// 핵심 규약: 센서 게이트웨이 설정이 잘못돼도 코어 API(8080)는 뜬다.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
)

// TestStartSensorGatewayFailureKeepsCoreRunning은 게이트웨이 구성 실패가
// 프로세스를 죽이지 않고 게이트웨이만 비활성화하는지 본다.
// 주입하는 오류는 SENSOR_ADVERTISED_HOSTS가 빈 실제 실패(sensorca SAN 필수)다 —
// 이 조합에서 백엔드가 exit(1) 하던 회귀를 막는다.
func TestStartSensorGatewayFailureKeepsCoreRunning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	srv, stop := startSensorGateway(logger, func() (*http.Server, func(), error) {
		return nil, nil, sensorca.ErrNoSAN
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

// TestSensorGatewayFailureHint는 실패 원인별로 지목 변수가 갈리는지 본다.
// 원인과 무관하게 SENSOR_ADVERTISED_HOSTS만 지목하던 오진 유도 문구의 회귀 방지다.
func TestSensorGatewayFailureHint(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantName string // 문구가 반드시 지목해야 하는 변수
		notName  string // 문구에 나오면 오진을 부르는 변수
	}{
		{
			name:     "missing SAN points at the advertised hosts",
			err:      fmt.Errorf("wrapped: %w", sensorca.ErrNoSAN),
			wantName: "SENSOR_ADVERTISED_HOSTS",
			notName:  "SENSOR_CA_ENC_KEY",
		},
		{
			name:     "unseal failure points at the encryption key",
			err:      fmt.Errorf("sensorgw: unseal sensor ca key: %w", sensorca.ErrOpen),
			wantName: "SENSOR_CA_ENC_KEY",
			notName:  "SENSOR_ADVERTISED_HOSTS",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sensorGatewayFailureHint(tc.err)
			if !strings.Contains(got, tc.wantName) {
				t.Errorf("hint = %q, want it to name %s", got, tc.wantName)
			}
			if strings.Contains(got, tc.notName) {
				t.Errorf("hint = %q, must not name the unrelated %s", got, tc.notName)
			}
		})
	}

	// 분류되지 않은 원인은 일반 문구 + 원인 오류를 그대로 싣는다.
	other := errors.New("dial tcp: connection refused")
	got := sensorGatewayFailureHint(other)
	for _, name := range []string{"SENSOR_CA_ENC_KEY", "SENSOR_ADVERTISED_HOSTS", "SENSOR_LISTEN_ADDR"} {
		if !strings.Contains(got, name) {
			t.Errorf("generic hint = %q, want it to name %s", got, name)
		}
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	startSensorGateway(logger, func() (*http.Server, func(), error) { return nil, nil, other })
	if !strings.Contains(buf.String(), other.Error()) {
		t.Errorf("warning must carry the underlying cause, got: %s", buf.String())
	}
}

// TestLogSensorGatewayDisabled는 게이트웨이 미구성 사유가 서로 모순되지 않는지 본다.
// 키 형식 오류일 때 "not set" INFO가 함께 나오면 값이 있는데도 없다고 읽힌다.
func TestLogSensorGatewayDisabled(t *testing.T) {
	var buf bytes.Buffer
	logSensorGatewayDisabled(slog.New(slog.NewTextHandler(&buf, nil)),
		errors.New("config: SENSOR_CA_ENC_KEY must be 64 hex chars (32 bytes)"))
	out := buf.String()
	if strings.Contains(out, "not set") {
		t.Errorf("invalid key must not also log 'not set', got: %s", out)
	}
	if !strings.Contains(out, "SENSOR_CA_ENC_KEY") {
		t.Errorf("warning must name SENSOR_CA_ENC_KEY, got: %s", out)
	}

	buf.Reset()
	logSensorGatewayDisabled(slog.New(slog.NewTextHandler(&buf, nil)), nil)
	if out := buf.String(); !strings.Contains(out, "not set") {
		t.Errorf("unset key must log the 'not set' notice, got: %s", out)
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
