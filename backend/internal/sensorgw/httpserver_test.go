package sensorgw

import (
	"net"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
)

// TestNewHTTPServerRequiresAdvertisedHost는 SENSOR_ADVERTISED_HOSTS가 빈 설정
// (DNS·IP 모두 없음)에서 구성이 오류로 끝난다는 계약을 고정한다 — cmd/server는
// 이 오류를 코어 API 종료가 아니라 게이트웨이 비활성화로 처리해야 한다.
// DB가 필요 없다(핸들러를 호출하지 않으므로 store·recorder는 nil).
func TestNewHTTPServerRequiresAdvertisedHost(t *testing.T) {
	ca, err := sensorca.New("test sensor gateway ca")
	if err != nil {
		t.Fatalf("new ca: %v", err)
	}
	gw := newGateway(nil, nil, ca)

	if _, err := gw.NewHTTPServer(":0", nil, nil); err == nil {
		t.Fatal("NewHTTPServer with no advertised host = nil error, want failure")
	}
	if _, err := gw.NewHTTPServer(":0", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		t.Fatalf("NewHTTPServer with one advertised ip: %v", err)
	}
}
