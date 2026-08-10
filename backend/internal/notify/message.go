package notify

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Severity는 알림 심각도다. Syslog severity와 웹훅 페이로드에 그대로 쓰인다.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityError    = "error"
	SeverityCritical = "critical"
)

// 이벤트 종류 식별자 (docs/00-spec.md 7.13). 웹훅의 event_id, Syslog의 MSGID로 나간다.
const (
	EventCertExpiring = "certificate.expiring"
	EventCertExpired  = "certificate.expired"
)

// Message는 채널에 전달되는 알림 한 건이다.
// 채널마다 렌더 형태가 다르므로(메일=텍스트, 웹훅=JSON, Syslog=구조화 필드)
// 텍스트 하나로 강제하지 않고 필드를 나눠 담는다.
type Message struct {
	// Title은 제목 한 줄이다. 메일 Subject, 웹훅 title, Syslog MSG에 쓰인다.
	Title string
	// TextBody는 사람이 읽는 본문이다 (주로 메일·인앱).
	TextBody string
	// Data는 기계가 읽는 필드다 (웹훅 JSON, Syslog 구조화 데이터).
	Data map[string]any
	// Severity는 SeverityInfo|Warning|Error|Critical 중 하나다. 비면 info로 본다.
	Severity string
	// EventID는 이벤트 종류 식별자다 (예: "certificate.expiring").
	EventID string
	// Recipient는 email/inbox 전용 수신 대상이다 (메일 주소 또는 사용자 ID).
	// webhook/syslog는 빈 문자열이며, 채널이 무시한다.
	// 수신자 주소는 로그·에러 메시지에 남기지 않는다.
	Recipient string
}

// CertExpiryFacts는 만료 임박 알림에 필요한 사실만 담은 고정 필드 구조체다.
// domain.Certificate를 받지 않는 이유는 원문 인증서 바이트(PEM/DER)가 알림 본문·
// 웹훅 페이로드·Syslog로 흘러갈 타입 경로 자체를 없애기 위해서다 (보안 하드 규칙 3).
// 필드를 늘릴 때도 스칼라 값만 추가하고, 인증서/키 바이트나 그것을 품은 구조체를
// 절대 넣지 않는다.
type CertExpiryFacts struct {
	CertificateID string
	CommonName    string
	NotAfter      time.Time
	DaysRemaining int
	Environment   string
	OwnerDisplay  string
	LocationCount int
	// DeepLink는 상세 화면 URL이다. NOTIFY_BASE_URL이 비면 빈 문자열이고 본문에서 생략된다.
	DeepLink string
}

// BuildCertExpiryMessage는 만료 임박/만료 알림 메시지를 만든다.
func BuildCertExpiryMessage(f CertExpiryFacts) Message {
	cn := scrubSensitive(f.CommonName)
	env := scrubSensitive(f.Environment)
	owner := scrubSensitive(f.OwnerDisplay)
	link := scrubSensitive(f.DeepLink)
	id := scrubSensitive(f.CertificateID)

	// 만료 여부는 DaysRemaining 부호가 아니라 NotAfter와 현재 시각으로 판정한다.
	// DaysRemaining은 탐지 쿼리의 floor(잔여 초/86400)이라 잔여 12시간이면 0이 되는데,
	// 부호로 판정하면 아직 유효한 인증서에 "만료되었습니다"가 나간다.
	// 경계는 탐지 쿼리(not_after >= now() = 임박 / not_after < now() = 만료)와 같다.
	now := time.Now()
	expired := now.After(f.NotAfter)
	title := fmt.Sprintf("[인증서 만료 D-%d] %s", f.DaysRemaining, cn)
	event := EventCertExpiring
	if expired {
		title = fmt.Sprintf("[인증서 만료] %s", cn)
		event = EventCertExpired
	}

	notAfter := f.NotAfter.UTC().Format(time.RFC3339)
	var b strings.Builder
	if expired {
		b.WriteString("인증서가 만료되었습니다.\n\n")
	} else {
		b.WriteString("인증서 만료가 임박했습니다.\n\n")
	}
	fmt.Fprintf(&b, "- 공통 이름(CN): %s\n", cn)
	fmt.Fprintf(&b, "- 인증서 ID: %s\n", id)
	fmt.Fprintf(&b, "- 만료 일시(UTC): %s\n", notAfter)
	if expired {
		// 경과 일수도 -DaysRemaining이 아니다 — 6시간 경과면 floor(-0.25) = -1이라 "1일"이 된다.
		elapsed := now.Sub(f.NotAfter)
		if d := int(elapsed / (24 * time.Hour)); d >= 1 {
			fmt.Fprintf(&b, "- 만료 경과: %d일\n", d)
		} else {
			fmt.Fprintf(&b, "- 만료 경과: %d시간\n", int(elapsed/time.Hour))
		}
	} else {
		fmt.Fprintf(&b, "- 남은 일수: %d일\n", f.DaysRemaining)
	}
	if env != "" {
		fmt.Fprintf(&b, "- 환경: %s\n", env)
	}
	if owner != "" {
		fmt.Fprintf(&b, "- 담당자: %s\n", owner)
	}
	fmt.Fprintf(&b, "- 설치 위치 수: %d\n", f.LocationCount)
	if link != "" {
		fmt.Fprintf(&b, "- 상세 보기: %s\n", link)
	}

	data := map[string]any{
		"event":          event,
		"certificate_id": id,
		"common_name":    cn,
		"not_after":      notAfter,
		"days_remaining": f.DaysRemaining,
		"location_count": f.LocationCount,
	}
	if env != "" {
		data["environment"] = env
	}
	if owner != "" {
		data["owner"] = owner
	}
	if link != "" {
		data["deep_link"] = link
	}

	return Message{
		Title:    title,
		TextBody: b.String(),
		Data:     data,
		Severity: expirySeverity(f.DaysRemaining),
		EventID:  event,
	}
}

func expirySeverity(days int) string {
	switch {
	case days <= 0:
		return SeverityCritical
	case days <= 7:
		return SeverityError
	case days <= 30:
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

// pemBlockRe / pemHeaderRe는 어떤 경로로든 흘러든 PEM 블록을 잡아낸다.
// 빌더가 고정 필드만 받으므로 정상 경로에서는 걸릴 일이 없지만,
// 상위 계층이 CN 같은 문자열에 원문을 밀어 넣는 사고를 마지막으로 막는다.
var (
	pemBlockRe  = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?-----END [A-Z0-9 ]+-----`)
	pemHeaderRe = regexp.MustCompile(`-----(BEGIN|END) [A-Z0-9 ]+-----`)
)

const redactedMark = "[redacted]"

// scrubSensitive는 문자열에서 PEM 블록을 제거한다 (보안 하드 규칙 3).
func scrubSensitive(s string) string {
	if !strings.Contains(s, "-----") {
		return s
	}
	s = pemBlockRe.ReplaceAllString(s, redactedMark)
	return pemHeaderRe.ReplaceAllString(s, redactedMark)
}
