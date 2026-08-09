// Package notify는 알림 채널 어댑터를 제공한다 (docs/00-spec.md 7.13).
// 저장에 의존하지 않는 순수 패키지다 — 수신자 결정·스케줄·재시도 상태·인앱 인박스는
// 상위 계층(store/dispatcher) 몫이며 여기서는 "한 건을 한 채널로 보낸다"만 다룬다.
// 표준 라이브러리만 사용한다 (외부 의존성 추가 금지).
//
// 보안:
//   - 페이로드는 고정 필드 구조체(CertExpiryFacts 등)로만 만든다. domain.Certificate처럼
//     원문 인증서 바이트를 품은 타입을 받지 않아 유출 경로 자체를 없앤다 (하드 규칙 3).
//   - Enabled()의 사유 문자열과 에러 메시지에 비밀번호·시크릿·수신자 주소를 담지 않는다.
package notify

import "context"

// 채널 식별자. 저장·설정·감사에서 같은 문자열을 쓴다.
const (
	ChannelEmail   = "email"
	ChannelWebhook = "webhook"
	ChannelInbox   = "inbox"
	ChannelSyslog  = "syslog"
)

// Notifier는 알림 채널 하나를 나타낸다.
type Notifier interface {
	// Channel은 채널 식별자를 반환한다 ("email" | "webhook" | "inbox" | "syslog").
	Channel() string
	// Enabled는 이 채널이 보낼 수 있는 상태인지와, 아니라면 그 사유를 반환한다.
	// 사유는 운영자가 어떤 환경변수를 채워야 하는지 알 수 있게 쓰되
	// 비밀번호·시크릿·토큰 값을 절대 담지 않는다.
	Enabled() (bool, string)
	// Send는 메시지 한 건을 보낸다. 재시도 가능 여부는 반환 에러를
	// IsTemporary로 판정한다.
	Send(ctx context.Context, m Message) error
}
