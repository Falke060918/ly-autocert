package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testFacts() CertExpiryFacts {
	return CertExpiryFacts{
		CertificateID: "11111111-2222-3333-4444-555555555555",
		CommonName:    "app.example.test",
		NotAfter:      time.Date(2026, 9, 1, 3, 4, 5, 0, time.UTC),
		DaysRemaining: 23,
		Environment:   "prod",
		OwnerDisplay:  "인프라팀",
		LocationCount: 3,
		DeepLink:      "https://autocert.example.test/certificates/11111111-2222-3333-4444-555555555555",
	}
}

func TestBuildCertExpiryMessage(t *testing.T) {
	m := BuildCertExpiryMessage(testFacts())

	if !strings.Contains(m.Title, "D-23") || !strings.Contains(m.Title, "app.example.test") {
		t.Errorf("title mismatch: %q", m.Title)
	}
	if m.Severity != SeverityWarning {
		t.Errorf("severity = %q, want %q", m.Severity, SeverityWarning)
	}
	if m.EventID != EventCertExpiring {
		t.Errorf("event id = %q, want %q", m.EventID, EventCertExpiring)
	}
	if m.Recipient != "" {
		t.Error("builder must not decide the recipient")
	}
	for _, want := range []string{"app.example.test", "2026-09-01T03:04:05Z", "23일", "prod", "인프라팀", "https://autocert.example.test/"} {
		if !strings.Contains(m.TextBody, want) {
			t.Errorf("text body missing %q:\n%s", want, m.TextBody)
		}
	}
	if m.Data["days_remaining"] != 23 || m.Data["common_name"] != "app.example.test" {
		t.Errorf("data mismatch: %#v", m.Data)
	}
	if m.Data["not_after"] != "2026-09-01T03:04:05Z" {
		t.Errorf("not_after must be RFC3339 UTC: %#v", m.Data["not_after"])
	}
}

// TestBuildCertExpiryMessageMarksCA는 CA 만료가 리프와 구분되어 나가는지 본다.
// 사건 종류(EventID)는 그대로여야 한다 — 종류를 나누면 기존 정책·웹훅 수신 측이
// CA 만료를 통째로 놓친다.
func TestBuildCertExpiryMessageMarksCA(t *testing.T) {
	f := testFacts()
	f.IsCA = true
	m := BuildCertExpiryMessage(f)

	if !strings.Contains(m.Title, "발급 기관") {
		t.Errorf("CA title must say so: %q", m.Title)
	}
	if m.EventID != EventCertExpiring {
		t.Errorf("event id = %q, want %q (CA must not create a new event type)", m.EventID, EventCertExpiring)
	}
	if !strings.Contains(m.TextBody, "하위 인증서") {
		t.Errorf("CA body must explain the blast radius:\n%s", m.TextBody)
	}
	if m.Data["is_ca"] != true {
		t.Errorf("data is_ca = %#v, want true", m.Data["is_ca"])
	}

	// 리프는 CA 문구가 붙지 않고, is_ca 키는 그래도 실린다(수신 측이 키 존재로 판정하지 않게).
	leaf := BuildCertExpiryMessage(testFacts())
	if strings.Contains(leaf.Title, "발급 기관") || strings.Contains(leaf.TextBody, "하위 인증서") {
		t.Errorf("leaf message must not carry CA wording: %q\n%s", leaf.Title, leaf.TextBody)
	}
	if leaf.Data["is_ca"] != false {
		t.Errorf("leaf data is_ca = %#v, want false", leaf.Data["is_ca"])
	}
}

func TestBuildCertExpiryMessageOmitsEmptyOptionalFields(t *testing.T) {
	f := testFacts()
	f.Environment = ""
	f.OwnerDisplay = ""
	f.DeepLink = "" // NOTIFY_BASE_URL 미설정
	m := BuildCertExpiryMessage(f)

	for _, unwanted := range []string{"환경:", "담당자:", "상세 보기:"} {
		if strings.Contains(m.TextBody, unwanted) {
			t.Errorf("text body must omit %q when the value is empty:\n%s", unwanted, m.TextBody)
		}
	}
	for _, key := range []string{"environment", "owner", "deep_link"} {
		if _, ok := m.Data[key]; ok {
			t.Errorf("data must omit key %q when the value is empty", key)
		}
	}
}

func TestBuildCertExpiryMessageSeverity(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{
		{60, SeverityInfo},
		{30, SeverityWarning},
		{7, SeverityError},
		{0, SeverityCritical},
		{-5, SeverityCritical},
	}
	for _, tc := range cases {
		f := testFacts()
		f.DaysRemaining = tc.days
		m := BuildCertExpiryMessage(f)
		if m.Severity != tc.want {
			t.Errorf("days=%d severity = %q, want %q", tc.days, m.Severity, tc.want)
		}
	}
}

// 만료 판정은 DaysRemaining 부호가 아니라 NotAfter와 현재 시각 비교다.
// DaysRemaining은 탐지 쿼리의 floor(잔여 초/86400)이므로 잔여 12시간이면 0이 되고,
// 부호로 판정하면 아직 유효한 인증서에 "만료되었습니다"가 나간다.
func TestBuildCertExpiryMessageStillValidWithinLastDay(t *testing.T) {
	f := testFacts()
	f.NotAfter = time.Now().Add(12 * time.Hour)
	f.DaysRemaining = 0 // floor(0.5) — 탐지 쿼리가 주는 값

	m := BuildCertExpiryMessage(f)

	if m.EventID != EventCertExpiring {
		t.Errorf("event id = %q, want %q", m.EventID, EventCertExpiring)
	}
	if !strings.Contains(m.Title, "D-0") || strings.Contains(m.Title, "[인증서 만료]") {
		t.Errorf("title must stay an expiring title: %q", m.Title)
	}
	if strings.Contains(m.TextBody, "만료되었습니다") || strings.Contains(m.TextBody, "만료 경과") {
		t.Errorf("a still-valid certificate must not read as expired:\n%s", m.TextBody)
	}
	if !strings.Contains(m.TextBody, "남은 일수: 0일") {
		t.Errorf("text body must keep the backend days_remaining:\n%s", m.TextBody)
	}
}

// 만료 경과 일수는 -DaysRemaining이 아니다 — 6시간 경과면 floor(-0.25) = -1이라 "1일"이 된다.
func TestBuildCertExpiryMessageElapsedMatchesRealElapsed(t *testing.T) {
	cases := []struct {
		name  string
		since time.Duration
		days  int
		want  string
	}{
		{"6시간 경과", 6 * time.Hour, -1, "만료 경과: 6시간"},
		{"36시간 경과", 36 * time.Hour, -2, "만료 경과: 1일"},
		{"10일 경과", 10 * 24 * time.Hour, -10, "만료 경과: 10일"},
	}
	for _, tc := range cases {
		f := testFacts()
		f.NotAfter = time.Now().Add(-tc.since)
		f.DaysRemaining = tc.days

		m := BuildCertExpiryMessage(f)

		if m.EventID != EventCertExpired {
			t.Errorf("%s: event id = %q, want %q", tc.name, m.EventID, EventCertExpired)
		}
		if strings.Contains(m.Title, "D-") {
			t.Errorf("%s: expired title must not carry a D-day: %q", tc.name, m.Title)
		}
		if !strings.Contains(m.TextBody, tc.want) {
			t.Errorf("%s: text body missing %q:\n%s", tc.name, tc.want, m.TextBody)
		}
	}
}

// 보안 하드 규칙 3 회귀 방지: 어떤 경로로도 인증서 원문이 알림에 실리지 않는다.
// CertExpiryFacts가 고정 스칼라 필드만 받는 것이 1차 방어이고, 아래는 상위 계층이
// 실수로 문자열 필드에 PEM을 밀어 넣은 경우까지 막는 2차 방어를 고정한다.
func TestBuildCertExpiryMessageNeverCarriesPEM(t *testing.T) {
	// 테스트 전용 더미 블록이다 — 실제 인증서·키가 아니다 (하드 규칙 4).
	const dummyPEM = "-----BEGIN CERTIFICATE-----\n" +
		"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWZnaGlqa2xtbm9wcXJzdHV2\n" +
		"-----END CERTIFICATE-----"

	f := testFacts()
	f.CommonName = "app.example.test\n" + dummyPEM
	f.OwnerDisplay = dummyPEM
	f.Environment = "-----BEGIN PRIVATE KEY-----"
	m := BuildCertExpiryMessage(f)

	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	rendered := string(encoded)
	for _, forbidden := range []string{
		"BEGIN CERTIFICATE", "END CERTIFICATE", "BEGIN PRIVATE KEY",
		"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWZnaGlqa2xtbm9wcXJzdHV2",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("rendered message must not contain %q:\n%s", forbidden, rendered)
		}
	}
	if !strings.Contains(m.Title, "app.example.test") {
		t.Errorf("scrubbing must keep the non-sensitive part: %q", m.Title)
	}
}

func TestScrubSensitiveLeavesPlainTextUntouched(t *testing.T) {
	in := "app.example.test — 정상 문자열"
	if got := scrubSensitive(in); got != in {
		t.Errorf("scrubSensitive(%q) = %q, want unchanged", in, got)
	}
}
