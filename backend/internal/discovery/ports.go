package discovery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// minPort/maxPort는 TCP 포트 번호의 유효 범위다.
const (
	minPort = 1
	maxPort = 65535
)

// DefaultPortSpec은 스캔 포트 사양의 기본값이다.
// 사용자가 포트를 지정하지 않으면 common 프리셋(26개)으로 스캔한다.
const DefaultPortSpec = "common"

// 프리셋 이름. ParsePortSpec의 토큰으로 그대로 쓴다.
const (
	PresetWeb    = "web"
	PresetCommon = "common"
	PresetAll    = "all"
)

// starttlsExcludedPorts는 "TLS를 쓰지만 프리셋에서 의도적으로 뺀" 포트들이다.
//
// 설계 근거 — 다시 넣지 말 것:
// ScanEndpoint는 TCP 연결 직후 곧바로 ClientHello를 보내는 **implicit TLS 전용** 스캐너다.
// 아래 포트들은 평문으로 세션을 시작한 뒤 STARTTLS/AUTH TLS 명령으로 TLS로 승격하므로,
// ClientHello를 그냥 던지면 핸드셰이크가 실패하고 인증서를 원리상 얻을 수 없다.
// 프리셋에 넣으면 "열려 있는데 항상 도달 불가"로 기록돼 인벤토리에 잡음만 남는다.
//   - 25/587 SMTP, 110 POP3, 143 IMAP, 21 FTP(AUTH TLS)
//   - 389 LDAP(StartTLS), 5432 PostgreSQL, 3306 MySQL, 1433 MSSQL
//   - 3389 RDP(TLS 협상 전 X.224 연결 요청 선행)
//
// 이 포트들을 지원하려면 프리셋 추가가 아니라 ScanEndpoint에 프로토콜별
// STARTTLS 승격 단계를 붙이는 별도 확장점이 필요하다.
// (implicit TLS 대응 포트는 프리셋에 이미 있다 — 465 SMTPS, 993 IMAPS, 995 POP3S,
// 989/990 FTPS, 636/3269 LDAPS.)
var starttlsExcludedPorts = []int{25, 587, 110, 143, 389, 5432, 3306, 1433, 21, 3389}

// presetWeb은 웹/관리 콘솔용 HTTPS 포트(6개)다.
var presetWeb = sortedUnique([]int{
	443,   // HTTPS
	4443,  // 대체 HTTPS
	8443,  // Tomcat/WAS HTTPS
	9443,  // WebSphere/관리 콘솔 HTTPS
	10443, // 어플라이언스 관리 HTTPS
	8080,  // HTTPS로 잘못 물린 대체 HTTP 포트(레거시에서 흔함)
})

// presetCommon은 web + 사내 인프라에서 흔한 implicit TLS 포트(총 26개)다. 기본값이다.
var presetCommon = sortedUnique(append([]int{
	8000,  // 대체 HTTP/HTTPS
	8888,  // 대체 관리 포트
	9090,  // 관리 콘솔/Prometheus 계열
	636,   // LDAPS
	3269,  // AD Global Catalog over SSL
	465,   // SMTPS (implicit)
	993,   // IMAPS (implicit)
	995,   // POP3S (implicit)
	989,   // FTPS 데이터 (implicit)
	990,   // FTPS 제어 (implicit)
	5061,  // SIP over TLS
	5671,  // AMQPS
	8883,  // MQTT over TLS
	2376,  // Docker daemon TLS
	2379,  // etcd client TLS
	6443,  // Kubernetes API server
	10250, // kubelet
	5986,  // WinRM over HTTPS
	7002,  // WebLogic SSL
	8834,  // Nessus/스캐너 콘솔
}, presetWeb...))

// PortPreset은 API가 노출하는 포트 프리셋 1개다 (GET /discovery/port-presets).
// all처럼 목록이 지나치게 긴 프리셋은 Ports를 비우고 Count로만 표기한다.
type PortPreset struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Count       int    `json:"count"`
	Ports       []int  `json:"ports,omitempty"`
}

// PortPresets는 사용 가능한 프리셋 목록을 반환한다. 반환 슬라이스는 복사본이라
// 호출자가 수정해도 내부 상태에 영향이 없다.
func PortPresets() []PortPreset {
	return []PortPreset{
		{
			Name:        PresetWeb,
			Description: "웹·관리 콘솔 HTTPS 포트",
			Count:       len(presetWeb),
			Ports:       copyInts(presetWeb),
		},
		{
			Name:        PresetCommon,
			Description: "web + 사내 인프라에서 흔한 TLS 포트 (기본값)",
			Count:       len(presetCommon),
			Ports:       copyInts(presetCommon),
		},
		{
			Name:        PresetAll,
			Description: "전체 포트 1-65535 (스캔 시간이 매우 깁니다)",
			Count:       maxPort - minPort + 1,
			// Ports 생략 — 6만 5천 개를 응답에 싣지 않는다.
		},
	}
}

// ParsePortSpec은 콤마로 구분된 포트 사양을 실제 포트 목록으로 펼친다.
// 각 토큰은 단일 포트("443"), 범위("8000-9000"), 프리셋명("web"/"common"/"all") 중 하나다.
// 서로 다른 토큰이 겹치면 합집합을 취하고, 결과는 중복 제거 후 오름차순 정렬한다.
// 잘못된 입력은 ErrInvalidScanInput으로 감싸 반환한다(핸들러가 400 매핑).
//
// 빈 토큰("443,,8443", "443,")은 **에러다**(설계 04-port-autoscan 2.1).
// 조용히 건너뛰지 않는 이유: 이 값은 사용자가 손으로 치는 자유 텍스트이고
// 겹친 쉼표는 대개 토큰 하나를 지우다 남긴 흔적이다. 무시하면 사용자가
// 요청했다고 믿는 것보다 좁은 범위가 돌고, 그 결과가 "인증서가 없다"로
// 인벤토리에 남는다 — 이 기능이 막으려던 바로 그 누락이다.
// 검증은 서버 파서 한 곳뿐이므로(설계 9.3) 관대해질 자리가 아니다.
func ParsePortSpec(spec string) ([]int, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("%w: 포트 사양이 비었습니다 (예: %q, %q, %q)",
			ErrInvalidScanInput, DefaultPortSpec, "443,8443", "8000-9000")
	}

	seen := make(map[int]bool)
	for _, raw := range strings.Split(spec, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			return nil, fmt.Errorf("%w: 포트 사양 %q에 빈 토큰이 있습니다 (쉼표가 겹쳤거나 맨 앞·뒤에 남았습니다)",
				ErrInvalidScanInput, spec)
		}
		ports, err := parsePortToken(token)
		if err != nil {
			return nil, err
		}
		for _, p := range ports {
			seen[p] = true
		}
	}
	// 여기까지 왔으면 토큰이 최소 1개 있고 모두 포트를 1개 이상 만들었으므로
	// seen은 비어 있을 수 없다(빈 토큰은 위에서 이미 거부했다).

	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}

// parsePortToken은 토큰 1개(프리셋·범위·단일 포트)를 포트 목록으로 바꾼다.
func parsePortToken(token string) ([]int, error) {
	switch strings.ToLower(token) {
	case PresetWeb:
		return copyInts(presetWeb), nil
	case PresetCommon:
		return copyInts(presetCommon), nil
	case PresetAll:
		all := make([]int, 0, maxPort-minPort+1)
		for p := minPort; p <= maxPort; p++ {
			all = append(all, p)
		}
		return all, nil
	}

	if strings.Contains(token, "-") {
		parts := strings.Split(token, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: 포트 범위 %q의 형식이 잘못되었습니다 (예: \"8000-9000\")",
				ErrInvalidScanInput, token)
		}
		start, err := parseSinglePort(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, err
		}
		end, err := parseSinglePort(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		if start > end {
			return nil, fmt.Errorf("%w: 포트 범위 %q의 시작(%d)이 끝(%d)보다 큽니다",
				ErrInvalidScanInput, token, start, end)
		}
		ports := make([]int, 0, end-start+1)
		for p := start; p <= end; p++ {
			ports = append(ports, p)
		}
		return ports, nil
	}

	p, err := parseSinglePort(token)
	if err != nil {
		return nil, err
	}
	return []int{p}, nil
}

// parseSinglePort는 포트 번호 1개를 파싱하고 1-65535 범위를 강제한다.
func parseSinglePort(token string) (int, error) {
	if token == "" {
		return 0, fmt.Errorf("%w: 포트 번호가 비었습니다 (숫자, 범위(\"8000-9000\"), 프리셋(web/common/all) 중 하나를 쓰십시오)",
			ErrInvalidScanInput)
	}
	n, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("%w: 포트 %q를 숫자로 해석할 수 없습니다 (숫자, 범위(\"8000-9000\"), 프리셋(web/common/all) 중 하나를 쓰십시오)",
			ErrInvalidScanInput, token)
	}
	if n < minPort || n > maxPort {
		return 0, fmt.Errorf("%w: 포트 %q이 범위를 벗어났습니다 (%d-%d)", ErrInvalidScanInput, token, minPort, maxPort)
	}
	return n, nil
}

// sortedUnique는 포트 목록을 중복 제거 후 오름차순 정렬한 새 슬라이스로 만든다.
func sortedUnique(ports []int) []int {
	seen := make(map[int]bool, len(ports))
	out := make([]int, 0, len(ports))
	for _, p := range ports {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// copyInts는 내부 프리셋이 호출자에게 수정되지 않도록 복사본을 만든다.
func copyInts(src []int) []int {
	out := make([]int, len(src))
	copy(out, src)
	return out
}
