# 인증서 만료 알림 설계 — 4채널

상태: **설계 확정 · 구현 미착수**
Phase 배정: **원래 Phase 4 항목이나 사용자 지시로 앞당긴다 — 자동 발급(Phase 2)을 뒤로 미루고 이것을 먼저 한다. 범위 이탈은 사용자 승인분이다.**
근거 명세: `docs/00-spec.md` 7.13 알림, 7.11 역할별 권한.

이 문서는 구현 지시서가 아니라 **설계 확정본**이다. 착수 시점이 오면 "12. 병렬 구현 단위"부터 읽는다.

---

## 1. 목적과 범위

만료가 임박한 인증서를 4개 채널로 알린다.

| 채널 | 대상 | 성격 |
| --- | --- | --- |
| `email` | 담당자 메일 | SMTP 직접 발송 |
| `webhook` | 사내 시스템 | HTTP POST JSON + HMAC 서명 |
| `inbox` | 제품 UI 알림함 | DB 행, 외부 의존 0 |
| `syslog` | SIEM·중앙 로그 | RFC5424 |

### 1.1 이번 범위에 포함

- 만료 예정(임계값 기반)·만료 이벤트 탐지
- 정책(policy) CRUD와 채널 설정
- 4채널 어댑터 + 아웃박스 재시도
- `users.email` 컬럼 추가
- 알림함 UI

### 1.2 이번 범위에서 제외

`00-spec.md` 7.13의 나머지 이벤트 — **갱신 완료 / 배포 완료 / 정책 위반 / 에이전트 연결 끊김** — 은 그 기능 자체가 아직 없다. `event_type`을 자유 문자열 + 정책의 `event_types text[]`로 두어 **확장점만 남기고 구현하지 않는다.**

---

## 2. 구조

```
 만료 판정 (스케줄러)
        │  pg_try_advisory_xact_lock — 인스턴스 중복 실행 차단
        ▼
 notification_events            ← 멱등키(dedup_key)로 중복 이벤트 차단
        │
        ▼
 notification_deliveries        ← 아웃박스. 채널×수신자당 1행
        │  FOR UPDATE SKIP LOCKED — 디스패처 중복 발송 차단
        ▼
 채널 어댑터 4종 (email / webhook / inbox / syslog)
```

중복은 두 층에서 각각 다른 수단으로 막는다.

| 층 | 위험 | 수단 |
| --- | --- | --- |
| 탐지 | 여러 서버 인스턴스가 같은 tick에 같은 인증서를 잡는다 | `pg_try_advisory_xact_lock(고정키)` + `dedup_key` UNIQUE |
| 발송 | 여러 디스패처가 같은 딜리버리를 집는다 | `FOR UPDATE SKIP LOCKED` 리스 + `lease_expires_at` |

---

## 3. 마이그레이션 `0012_notifications`

번호 주의: **0012는 이 기능이 선점한다.** 포트 자동 탐지(`docs/design/04-port-autoscan.md`)는 0013이다. 기존 규칙대로 번호는 max+1이며 빈 번호(0003·0005)를 메우지 않는다.

### 3.1 `users` 변경

```sql
ALTER TABLE users ADD COLUMN email text;
ALTER TABLE users ADD CONSTRAINT users_email_shape
  CHECK (email IS NULL OR position('@' in email) > 1);
```

**현재 `users`에는 email 컬럼이 아예 없다.** 메일 채널의 수신자를 해석할 방법이 없으므로 이번 범위에 포함한다. nullable이며(기존 계정 마이그레이션 부담 없음), CHECK는 형식 검증이 아니라 명백한 오입력만 막는 최소 가드다(RFC5322 정규식은 쓰지 않는다 — 오탐이 더 비싸다).

### 3.2 `notification_policies`

| 컬럼 | 타입 | 비고 |
| --- | --- | --- |
| `id` | uuid PK | |
| `name` | text **UNIQUE** | |
| `enabled` | bool | |
| `event_types` | text[] | 확장점(1.2) |
| `thresholds` | int[] | default `{90,60,30,7,1}` |
| `ca_thresholds` | int[] **nullable** | 0021. 발급 기관(CA) 전용 임계값. NULL = 미설정(CA도 `thresholds`) / `'{}'` = CA는 알리지 않음 / 값 = CA 전용. 7.3.2 |
| `channels` | text[] | CHECK: 원소가 `{email,webhook,inbox,syslog}` 부분집합 |
| `recipients` | jsonb | 5.1 참조 |
| `filter_environments` | text[] | 비면 전체 |
| `filter_department_ids` | uuid[] | 비면 전체 |
| `created_by` | uuid | |
| `created_at` / `updated_at` | timestamptz | |

### 3.3 `notification_events`

| 컬럼 | 타입 | 비고 |
| --- | --- | --- |
| `id` | uuid PK | |
| `dedup_key` | **text NOT NULL UNIQUE** | 4장 |
| `event_type` | text | |
| `certificate_id` | uuid **ON DELETE SET NULL** | 인증서가 지워져도 이벤트 이력은 남는다 |
| `threshold_days` | int NULL | |
| `policy_id` | uuid NULL | 최초로 이 이벤트를 발생시킨 정책(참고용) |
| `severity` | text | |
| `payload` | jsonb | **공개 필드 스냅샷만**(11.2) |
| `status` | text | `pending` \| `suppressed` |
| `created_at` | timestamptz | |

### 3.4 `notification_deliveries` (아웃박스)

| 컬럼 | 타입 | 비고 |
| --- | --- | --- |
| `id` | uuid PK | |
| `event_id` | uuid **ON DELETE CASCADE** | |
| `channel` | text | |
| `recipient` | **text NOT NULL DEFAULT ''** | 3.7 |
| `status` | text | `pending` · `sending` · `sent` · `failed` · `skipped` |
| `attempt_count` | int | |
| `next_attempt_at` | timestamptz | |
| `leased_at` / `lease_expires_at` | timestamptz | |
| `last_error` | text | **500자로 절단해 저장** |
| `sent_at` | timestamptz | |

제약·인덱스:

```sql
UNIQUE (event_id, channel, recipient)
CREATE INDEX ... ON notification_deliveries (next_attempt_at) WHERE status = 'pending';
```

### 3.5 `notification_inbox`

| 컬럼 | 타입 | 비고 |
| --- | --- | --- |
| `id` | uuid PK | |
| `user_id` | uuid ON DELETE CASCADE | |
| `event_id` | uuid ON DELETE CASCADE | |
| `title` / `body` | text | |
| `severity` | text | |
| `certificate_id` | uuid NULL | 딥링크용 |
| `read_at` / `archived_at` | timestamptz NULL | |
| `created_at` | timestamptz | |

```sql
UNIQUE (user_id, event_id)
CREATE INDEX ... ON notification_inbox (user_id, created_at DESC);
CREATE INDEX ... ON notification_inbox (user_id)
  WHERE read_at IS NULL AND archived_at IS NULL;   -- 안 읽음 배지 카운트
```

### 3.6 시드

기본 정책 1건을 심는다.

```
name          = '기본 만료 알림'
enabled       = true
thresholds    = {90,60,30,7,1}
ca_thresholds = {365,180,90,30,7}   -- 0021. CA는 교체가 오래 걸려 더 이르게 (7.3.2)
channels      = {inbox}
recipients    = [{"kind":"role","role":"system_administrator"}]
```

**inbox 단독**이 핵심이다. 설치 직후 SMTP·웹훅 설정이 하나도 없어도 동작하고, 외부로 아무것도 새지 않는다. zero-config 안전 기본값.

### 3.7 `recipient NOT NULL DEFAULT ''`가 load-bearing인 이유

웹훅·Syslog는 "수신자"라는 개념이 없어서 NULL로 두고 싶어진다. **그러면 중복 방지가 통째로 무력화된다.**

Postgres의 UNIQUE 제약은 NULL을 서로 다른 값으로 취급한다. `(event_id, 'webhook', NULL)`을 100번 INSERT하면 100행이 전부 들어간다. 즉 재시작·재시도 경로에서 같은 웹훅이 여러 번 발사된다.

빈 문자열 `''`은 값이므로 UNIQUE가 정상 작동한다. 이 컬럼을 나중에 nullable로 "정리"하지 말 것.

---

## 4. 멱등키

```
만료 예정 : certificate.expiring:<cert_id>:<threshold_days>
만료      : certificate.expired:<cert_id>
```

### 4.1 정책 id를 키에 넣지 않는다

두 정책이 같은 인증서·같은 임계값을 커버해도 **이벤트는 1건**이고, 수신자는 두 정책의 **합집합**이 된다. 정책을 겹쳐 만들었다는 이유로 담당자에게 같은 메일이 두 통 가지 않는다.

부수 효과 하나를 명시한다: **임계값을 나중에 늘려도 이미 지난 임계값은 재발송되지 않는다.** 예를 들어 D-30이 이미 나간 뒤 정책에 60을 추가하면 D-60은 발송되지 않는다. 근거는 대상 조건이 아니라 **7.3.1의 임계값 선택 규칙**이다 — 이미 지난 임계값은 애초에 이벤트로 만들어지지 않는다. 이것이 의도된 동작이다 — 과거를 소급 발송하면 정책 편집이 곧 스팸 폭발이 된다.

> 2026-08 정정: 이 절은 원래 "대상 조건 `not_after >= now()`와 임계 조건에서 걸러진다"고 적혀 있었으나 **사실이 아니었다.** 초기 구현은 `unnest(thresholds)`를 그대로 조인해 도달한 임계값 전부를 매칭했고, 정책에 임계값을 추가하면 그 자리에서 소급 발송됐다. 7.3.1이 그 결함의 수정본이다.

### 4.2 `not_after`를 키에서 뺀다

`not_after`를 키에 넣으면 "인증서가 교체되면 새 알림"이 되어 보일 수 있지만, 이 제품에서 인증서는 **fingerprint와 1:1**이라 `not_after`가 바뀌면 그건 다른 인증서다(다른 `cert_id`). 키에 넣어봐야 항상 같은 값이고 키만 길어진다.

---

## 5. 채널

### 5.1 인터페이스

```go
type Notifier interface {
    Channel() string
    Enabled() (bool, string)          // false일 때 두 번째 값이 사유
    Send(ctx context.Context, d Delivery) error
}
```

오류는 두 종류로 분류한다.

```go
notify.Temporary  // 재시도한다
notify.Permanent  // 재시도하지 않고 failed로 종료
```

메시지 타입:

```go
type Message struct {
    Title    string
    TextBody string             // 메일 본문
    Data     map[string]any     // 웹훅·Syslog 구조화 필드
}
```

`TextBody`와 `Data`를 **함께** 보유하는 것이 중요하다. 모든 채널을 텍스트로 강제하면 웹훅이 파싱 불가능한 문자열 덩어리를 받게 되고, 반대로 구조화만 하면 메일 본문을 만들 수 없다.

### 5.2 `email.go`

- `net/smtp` + `crypto/tls` (표준 라이브러리).
- 기본 `STARTTLS`. `SMTP_TLS_MODE`로 `tls`(implicit) / `none` 선택.
- **`SMTP_CA_FILE`로 사내 사설 CA를 신뢰한다.** 폐쇄망에서 메일 서버가 사설 CA 인증서를 쓰는 것이 정상이므로 이 경로가 없으면 `INSECURE_SKIP_VERIFY`를 켜는 수밖에 없게 된다.
- 오류 분류: SMTP **5xx = Permanent**(메일박스 없음 등), **4xx·네트워크 오류 = Temporary**.

### 5.3 `webhook.go`

- `net/http` POST, `Content-Type: application/json`.
- 서명: `crypto/hmac` + `crypto/sha256` → 헤더 `X-AutoCert-Signature: sha256=<hex>`.
  표준 라이브러리 조합이므로 **"자체 암호 구현 금지"에 저촉되지 않는다.** 해시 함수를 만드는 것이 아니라 표준 HMAC을 호출하는 것이다.
- `WEBHOOK_AUTH_HEADER`로 정적 인증 헤더 추가 가능(`Authorization: Bearer ...` 등).
- 오류 분류: **2xx 성공** / **408·429·5xx = Temporary** / **그 외 4xx = Permanent**.

### 5.4 `inbox.go`

```sql
INSERT INTO notification_inbox (...) VALUES (...)
ON CONFLICT (user_id, event_id) DO NOTHING;
```

외부 의존이 없으므로 사실상 실패하지 않는다. 시드 정책이 이 채널만 쓰는 이유다.

### 5.5 `syslog.go`

**RFC5424를 직접 포맷하고 `net.Dial`로 보낸다.** TCP는 RFC6587 octet-counting 프레이밍(`<길이> <메시지>`)을 쓴다.

`log/syslog`를 쓰지 않는 이유:

1. 표준 라이브러리에서 **동결(frozen)** 되어 신규 기능이 들어가지 않는다.
2. **RFC3164(구식)만** 만든다. 구조화 데이터·정밀 타임스탬프가 없어 SIEM 연동에 불리하다.
3. **Windows를 지원하지 않는다.** 센서·서버 바이너리의 크로스 컴파일을 깨뜨린다.

Syslog 포맷팅은 문자열 조립이지 암호 구현이 아니므로 **하드 규칙 2("자체 암호 구현 금지") 위반이 아니다.** 이 판단 근거를 여기 남긴다.

주의: **UDP는 fire-and-forget이라 `sent` 상태가 수신 보장이 아니다.** 운영 매뉴얼에 명시한다. 보장이 필요하면 `SYSLOG_NETWORK=tcp`를 쓴다.

---

## 6. 재시도와 디스패처

### 6.1 백오프

```
next_attempt_at = now() + 1분 × 2^(attempt_count - 1)
상한 = 1시간
최대 시도 = 5회
```

→ **5회째 실패에서 종결**되므로 실제 대기는 **1m · 2m · 4m · 8m 네 번**이다(1회차 실패 후 1m, …, 4회차 실패 후 8m). 5회차 실패 시점에는 다음 대기를 잡지 않고 곧바로 `failed` + `last_error`(500자 절단)를 기록한다. 상한 1시간은 최대 시도 5회에서는 도달하지 않으며, 시도 횟수를 늘릴 때를 위한 가드다.

`attempt_count`는 "실패 횟수"가 아니라 **시도 횟수**다. 성공(`sent`)에서도 1 올린다 — 올리지 않으면 발송 이력의 `sent` 행이 전부 "시도 0"으로 보이는데 디스패처 로그는 같은 건을 `attempt: 1`로 남겨 두 기록이 어긋난다. 첫 시도에 성공한 건은 `attempt_count = 1`이다.

### 6.2 자연 throttle

디스패처는 **15초 주기**로 돌며 한 번에 **최대 20건**을 리스한다.

```
20건 / 15초 = 분당 약 80건
```

이 수치가 사실상 발송 레이트 제한이므로 **별도 rate-limit 환경변수를 두지 않는다.** 조절이 필요하면 주기와 배치 크기를 조정한다. 설정 표면을 늘리지 않는 것이 목적이다.

---

## 7. 스케줄러

### 7.1 구동 규약

```go
func StartScheduler(ctx context.Context) (stop func())
```

`sensorgw.StartReaper`와 **동일한 규약**이다. `time.Ticker` 기반이며 `main.go` 배선도 같은 모양을 따른다. 새 패턴을 만들지 않는다.

### 7.2 단일 실행 보장

매 tick은 **단일 트랜잭션**으로 실행하고, 트랜잭션 시작 직후 `pg_try_advisory_xact_lock(고정키)`를 시도한다. 실패하면 **조용히 skip**한다(다른 인스턴스가 이미 돌고 있다는 뜻이므로 에러가 아니다). 트랜잭션 종료와 함께 락이 자동 해제되어 별도 해제 코드가 필요 없다.

### 7.3 탐지 쿼리 — 세트 기반 단일 문장

행을 하나씩 돌지 않는다. 정책 × `certificates` 조인으로 한 번에 만든다.

```sql
INSERT INTO notification_events (dedup_key, event_type, certificate_id, threshold_days, ...)
SELECT ...
FROM notification_policies p
CROSS JOIN certificates c
CROSS JOIN LATERAL (SELECT floor(extract(epoch FROM (c.not_after - now())) / 86400)::int
                      AS days_remaining) dr
CROSS JOIN LATERAL (SELECT min(th.days) AS days          -- 7.3.1: 도달한 것 중 최솟값 1개
                      FROM unnest(p.thresholds) AS th(days)
                     WHERE th.days > 0 AND th.days >= dr.days_remaining) t
WHERE p.enabled
  AND t.days IS NOT NULL
  AND c.not_after >= now()
  AND c.status NOT IN ('revoked', 'replaced')
ON CONFLICT (dedup_key) DO NOTHING
RETURNING id;
```

`RETURNING id`로 **새로 생긴 이벤트만** 받아 딜리버리를 만든다. 이미 있던 것은 반환되지 않으므로 중복 발송이 구조적으로 불가능하다.

### 7.3.1 임계값 선택 — 인증서·정책당 1개만 발화한다

**정책 thresholds 중 이미 도달한 것(`th.days >= days_remaining`)들의 최솟값 하나만** 이벤트가 된다. D-3 인증서 + thresholds `{90,60,30,7,1}` → 이벤트는 `threshold_days = 7` **1건**이다.

`unnest`를 그대로 조인해 "도달한 임계값 전부"를 매칭하면 두 경로로 소급 폭주가 난다. 둘 다 e2e에서 실측된 결함이다.

| 경로 | 증상 |
| --- | --- |
| 정책 편집 | thresholds `[7]`로 D-5를 발송한 뒤 `[7,30,60,90]`으로 고치면 다음 tick에 30·60·90이 새로 발화한다 |
| 신규 발견 | 스캔이 D-3짜리 인증서를 발견하면 90/60/30/7이 동시에 터져 제목이 전부 "D-3"인 알림이 채널×수신자만큼 나간다(인증서 1건에 메일 8통 실측, 200건이면 800통) |

- **인증서 단위 기준선은 `dedup_key`가 대신한다.** 임계값이 작아질 때마다 키가 바뀌므로 D-90 → D-60 → D-30 → D-7의 정상 케이던스는 그대로 유지되고, 한 번 지나간(= 더 큰) 임계값은 애초에 생성되지 않는다. `seed`(7.7)는 "프로세스 생애 최초 tick 1회"에만 걸리는 전역 스위치라 이 역할을 대신할 수 없다.
- **비교 기준은 `not_after <= now() + t.days`가 아니라 `days_remaining`이다.** 알림 제목·payload가 쓰는 D-day와 같은 값이라야 "D-7 알림인데 제목은 D-6"이 안 난다.
- **아직 어떤 임계값에도 도달하지 않은 인증서는 이벤트가 없다**(D-120 + 최대 임계값 90 → 0건).
- 정책이 여럿이면 정책마다 최솟값을 고르므로, 서로 다른 임계값을 가진 두 정책은 서로 다른 이벤트를 만들 수 있다. 이는 4.1의 "정책이 겹쳐도 1건"과 모순되지 않는다 — 같은 임계값을 고른 경우에만 겹치고, 그때는 `dedup_key`가 합쳐 준다.

### 7.3.2 발급 기관(CA) 전용 임계값 (0021)

CA 만료는 **그 CA가 발급한 인증서 전부가 함께 신뢰를 잃는** 사건이고, 대응이 "재발급 후 배포"가 아니라 "새 CA 구축 + 하위 전건 재발급 + 신뢰 저장소 갱신"이다. 리프와 같은 D-90에 처음 알리면 이미 늦는다.

`unnest`의 입력만 갈라 해결한다 — `certificate_type = 'ca'`이고 정책에 `ca_thresholds`가 있으면 그 목록으로, 아니면 `thresholds`로 판정한다. 7.3.1의 "도달한 것 중 최솟값 1개" 규칙은 그대로다.

- **새 사건 종류를 만들지 않는다.** `certificate.ca_expiring` 같은 종류를 나누면 기존 정책·알림함·웹훅 수신 측이 그 종류를 모르는 채 CA 만료를 통째로 놓친다. 채널·수신자·필터도 그대로 공유한다. 구분은 `payload.is_ca`(웹훅 `data.is_ca`)와 제목의 "발급 기관(CA)" 표기로만 한다.
- **판정은 `certificate_type = 'ca'`다.** 0018의 `is_ca`(BasicConstraints 원본)는 그 이전 행이 NULL이라 소급 판정에 쓸 수 없다.
- **3상태를 구분한다.** NULL(미설정 — 0021 이전과 동일) / `'{}'`(CA는 만료 임박 알림 없음 — CA를 별도 절차로 관리하는 조직) / 값. DEFAULT를 배열로 두면 앞의 둘이 같은 값이 되어 되돌릴 수 없으므로 컬럼 기본값은 NULL이다. API PATCH도 같은 이유로 "키 없음(유지)"과 "명시적 `null`(해제)"을 구분한다.
- **만료 후 알림(`certificate.expired`)은 임계값을 쓰지 않으므로 CA도 리프와 똑같이 나간다.** `ca_thresholds = '{}'`가 끄는 것은 만료 **임박** 알림뿐이다.
- 업그레이드 시 백필은 0012 시드 정책 1행(`기본 만료 알림`, `created_by IS NULL`)에만 `{365,180,90,30,7}`을 넣는다. 사용자가 만든 정책까지 건드리면 아무도 요청하지 않은 알림이 나간다. 백필된 시드 정책에서도 CA당 이벤트는 **1건**이다(도달한 임계값 중 최솟값 하나).

### 7.4 대상 조건

- **`not_after`로 판정한다. 저장된 `status`로 판정하지 않는다.**
  이유: `status`를 만료로 갱신하는 주체가 현재 없어 stale이다. 만료 알림이 stale 컬럼에 의존하면 정작 만료된 인증서를 놓친다. `status`는 `revoked` / `replaced` **제외 필터**로만 쓴다(이 둘은 사용자 액션으로 갱신되므로 신뢰 가능).
- **CA 인증서도 포함한다.** CA 만료는 리프 전체를 한꺼번에 무너뜨리므로 오히려 더 중요하다.

### 7.5 재시작 회수

서버 기동 시 `status = 'sending' AND lease_expires_at < now()`인 딜리버리를 `pending`으로 되돌린다. 프로세스가 발송 도중 죽어도 고아가 남지 않는다.

### 7.6 보존

`NOTIFY_RETENTION_DAYS`(기본 180) 초과한 **종료 상태**(`sent` / `failed` / `skipped`) 행을 삭제한다. 진행 중 행은 나이와 무관하게 남긴다.

삭제 순서가 load-bearing이다: 이벤트를 먼저 지우면 FK CASCADE가 아직 읽지 않은 알림함 행까지 함께 지운다. 그래서 종료 딜리버리 → 보관된 알림함 → 남은 참조가 없는 이벤트 순으로 지운다.

**예외: 아직 재탐지되는 이벤트는 나이와 무관하게 남긴다.** `certificate.expired`의 탐지 조건 `c.not_after < now()`에는 상한이 없어서 인벤토리에 남아 있는 만료 인증서는 영원히 탐지 대상이다. 그 이벤트를 지우면 `dedup_key`가 풀려 다음 tick에 같은 인증서가 다시 발송된다 — **보존 주기마다 만료 알림이 되살아난다.** `seed`로 `suppressed` 적재된 이벤트는 딜리버리·알림함 참조가 아예 없어 위 두 참조 검사를 그냥 통과하므로 특히 위험하다(침묵 처리해 둔 건이 180일 뒤 진짜 발송이 된다).

`certificate.expiring`은 이 예외가 필요 없다 — 임계값을 넘긴 인증서는 보존 기간이 지나기 전에 만료되어 `not_after >= now()` 조건에서 빠지므로 재탐지되지 않는다. 남기는 행 수는 "인벤토리에 남아 있는 만료 인증서 수"로 묶여 있어 무한 증가하지 않는다.

### 7.7 첫 실행 — `NOTIFY_FIRST_RUN=seed`

기본값 `seed`에서는 **최초 tick이 이미 지난 임계값을 `suppressed`로만 적재하고 발송하지 않는다.**

이것이 없으면 설치 당일에 기존 인벤토리 전체의 D-90/60/30/7/1이 한꺼번에 터진다. 수백 통의 메일이 첫인상이 된다. `seed` 모드는 "현재 상태를 기준선으로 삼고, 이후 새로 임계값을 넘는 것부터 알린다"는 뜻이다.

`NOTIFY_FIRST_RUN=send`를 주면 기존 동작(전부 발송)을 선택할 수 있다.

---

## 8. `scan_jobs`를 재사용하지 않는 이유

재사용 제안이 나올 만한 자리이므로 근거를 남긴다.

1. `job_type` CHECK 제약이 `'scan'`으로 **폐쇄**되어 있다. 알림을 넣으려면 제약을 고쳐야 하고, 그건 스캐너 쪽 가정을 흔든다.
2. **컬럼 모양이 다르다.** `scan_jobs`는 타깃·엔드포인트·진행률 중심이고 알림은 채널·수신자·백오프 중심이다. 공유하면 절반이 항상 NULL이다.
3. **회수 의미론이 다르다.** 고아 스캔 Job은 `failed`로 회수하지만(재실행 비용이 큼), 고아 딜리버리는 `pending`으로 되돌려 재시도해야 한다.

재사용하는 것은 **테이블이 아니라 패턴**이다 — `FOR UPDATE SKIP LOCKED` 리스, 명시적 상태 머신, 리퍼, `main.go` 배선 규약. 이건 그대로 가져온다.

---

## 9. API

### 9.1 알림함 (전 역할)

| 메서드 | 경로 | 비고 |
| --- | --- | --- |
| GET | `/api/v1/notifications` | `status=unread\|all\|archived` |
| GET | `/api/v1/notifications/unread-count` | 배지용 |
| POST | `/api/v1/notifications/{id}/read` | |
| POST | `/api/v1/notifications/read-all` | |
| POST | `/api/v1/notifications/{id}/archive` | |

- **본인 것만 강제 필터한다.** 쿼리에 `user_id = <세션 사용자>`를 항상 넣는다.
- 소유자가 아니면 **404**(403이 아니다 — 존재 여부를 흘리지 않는다).
- 라우트 등록 시 **리터럴 세그먼트가 `{id}`보다 먼저 매칭되도록** 한다. `/read-all`이 `/{id}/read`에 잡히면 안 된다. `/certificates/summary` 선례와 동일한 처리.

### 9.2 정책·채널 (system_administrator + security_administrator)

| 메서드 | 경로 | 비고 |
| --- | --- | --- |
| GET / POST | `/api/v1/notification-policies` | |
| PATCH / DELETE | `/api/v1/notification-policies/{id}` | |
| POST | `/api/v1/notification-policies/{id}/test` | 테스트 발송 |
| GET | `/api/v1/notification-channels` | 9.3 |
| GET | `/api/v1/notification-deliveries` | + auditor 열람 가능 |

역할 근거: `00-spec.md` **7.11 "보안 관리자 = 정책 설정"**. 알림 정책은 보안 정책의 하위이므로 security_administrator에 부여한다.

감사 이벤트: `notification.policy.create` / `notification.policy.update` / `notification.policy.delete` / `notification.test`.
변경과 감사는 **단일 트랜잭션**이고, **감사 INSERT가 트랜잭션의 마지막 문장**이다(기존 규칙 — 감사 해시 체인의 advisory lock 보유 구간을 최소화).

### 9.3 `GET /notification-channels`는 설정값을 노출하지 않는다

응답은 **`{channel, enabled, reason}` 뿐이다.**

```json
[
  {"channel":"email",  "enabled":false, "reason":"SMTP_HOST is not set"},
  {"channel":"inbox",  "enabled":true,  "reason":""}
]
```

**호스트·계정·비밀번호·웹훅 URL을 응답에 절대 싣지 않는다.** 운영자는 "왜 안 되는지"만 알면 되고, 값은 서버 환경변수에서 확인한다. 설정 화면에 값을 되비추는 순간 그 값은 브라우저·프록시 로그·스크린샷으로 샌다.

---

## 10. 설정

### 10.1 실패 격리

**알림 설정 오류가 코어 API를 죽이지 않는다.** 센서 게이트웨이 선례와 동일한 원칙이다.

잘못된 `SMTP_PORT`는 **메일 채널만 비활성화**하고 나머지 3채널과 코어 API는 정상 동작한다. 원인은 `Config.NotifyChannelErrors`에 채널별로 담고, 기동 경고 로그와 `GET /notification-channels`의 `reason`에 같은 문자열을 쓴다(진단 경로 일원화).

### 10.2 신규 환경변수

**공통**

| 변수 | 기본값 | 의미 |
| --- | --- | --- |
| `NOTIFY_ENABLED` | `true` | 알림 기능 전체 on/off |
| `NOTIFY_SCAN_INTERVAL` | `1h` | 스케줄러 tick 주기 |
| `NOTIFY_FIRST_RUN` | `seed` | `seed` \| `send` (7.7) |
| `NOTIFY_RETENTION_DAYS` | `180` | 종료 딜리버리 보존 |
| `NOTIFY_BASE_URL` | — | 알림 본문 딥링크의 기준 URL |

**SMTP**

| 변수 | 기본값 | 의미 |
| --- | --- | --- |
| `SMTP_HOST` | — | 비면 메일 채널 비활성 |
| `SMTP_PORT` | `587` | |
| `SMTP_FROM` | — | |
| `SMTP_USERNAME` | — | |
| `SMTP_PASSWORD` | — | |
| `SMTP_PASSWORD_FILE` | — | **파일이 있으면 파일이 우선**(Docker secret 경로) |
| `SMTP_TLS_MODE` | `starttls` | `starttls` \| `tls` \| `none` |
| `SMTP_CA_FILE` | — | 사내 사설 CA |
| `SMTP_TLS_INSECURE_SKIP_VERIFY` | `false` | 기동 시 경고(11.5) |

**웹훅**

| 변수 | 기본값 | 의미 |
| --- | --- | --- |
| `WEBHOOK_URL` | — | 비면 채널 비활성 |
| `WEBHOOK_HMAC_SECRET` | — | 비면 서명 헤더 생략 |
| `WEBHOOK_AUTH_HEADER` | — | 정적 인증 헤더 |
| `WEBHOOK_CA_FILE` | — | 사설 CA |

**Syslog**

| 변수 | 기본값 | 의미 |
| --- | --- | --- |
| `SYSLOG_ADDR` | — | 비면 채널 비활성 |
| `SYSLOG_NETWORK` | `udp` | `udp` \| `tcp` |
| `SYSLOG_FACILITY` | `local0` | |
| `SYSLOG_APP_NAME` | `autocert` | RFC5424 APP-NAME |

### 10.3 `.env` 취급

`.env`·`.env.example`은 **에이전트 권한 밖이라 읽지도 쓰지도 못한다.** 따라서:

- 신규 변수는 `docs/manual/INSTALL.md`에 **새 절**로 전부 기재하고, 사용자가 손으로 `.env`에 넣는다.
- `docker-compose.yml`에는 `${VAR:-기본값}` **패스스루만** 추가한다(값은 넣지 않는다).

---

## 11. 보안

### 11.1 자격증명은 DB에 저장하지 않는다

SMTP 비밀번호·웹훅 시크릿은 **환경변수 전용**이다. `notification_policies`에 저장하지 않는다. DB 덤프·백업·감사 조회 어디에도 자격증명이 나타나지 않는다는 뜻이다. 대가는 "변경하려면 컨테이너 재시작"이며, 이는 수용된 결정이다(12.1 ③).

### 11.2 페이로드 빌더가 `domain.Certificate`를 받지 않는다

빌더 시그니처를 **고정 필드 구조체**로 못박는다.

```go
type CertView struct {
    CertificateID string
    CommonName    string
    NotAfter      time.Time
    DaysRemaining int
    Environment   string
    OwnerDisplay  string   // 표시명만. 메일 주소 아님
    DeepLink      string
    LocationCount int
}
```

`domain.Certificate`를 넘기면 DER 바이트·PEM 원문·개인키 참조가 **타입 경로상 도달 가능**해지고, 언젠가 누군가 `%+v`를 찍는다. 아예 도달할 수 없게 만드는 것이 유일하게 신뢰할 만한 방어다. 하드 규칙 3("인증서·개인키 로그 출력 금지")의 구조적 이행이다.

### 11.3 렌더 결과 단위 테스트

모든 채널의 렌더 결과에 `BEGIN CERTIFICATE`·base64 블록이 **없음**을 단위 테스트로 고정한다. 템플릿을 나중에 고치다 인증서 본문을 넣으면 테스트가 깨진다.

### 11.4 로그 규칙

로그에 남기는 것은 `delivery_id`, `channel`, `event_id`, 오류 분류·메시지뿐이다.
**수신자 메일 주소는 로그에 남기지 않는다.** 개인정보이며, 로그는 인증서 인벤토리보다 훨씬 넓게 열람된다.

### 11.5 `SMTP_TLS_INSECURE_SKIP_VERIFY=true`

기동 시 경고 로그를 남긴다. 이 값이 켜져 있으면 메일 서버 사칭이 가능하고, 알림 본문에는 어느 시스템의 어떤 인증서가 언제 만료되는지가 들어 있다(정찰 가치가 있는 정보다). 정상 경로는 `SMTP_CA_FILE`이다.

---

## 12. 결정 사항 (사용자 승인분)

| # | 결정 | 근거 |
| --- | --- | --- |
| ① | **알림함 읽음·보관 API는 감사 이벤트를 남기지 않는다** | 본인 소유 행의 개인적 읽음 상태다. 인벤토리도 설정도 바뀌지 않는다. 감사 대상은 "변경 API"이지 "내 UI 상태"가 아니다 |
| ② | **발송 성공/실패도 감사에 기록하지 않는다** | 감사 체인이 advisory lock으로 **직렬화**되어 있어, 대량 발송이 로그인·업로드 같은 사용자 요청을 지연시킨다. 완전한 이력은 `notification_deliveries`가 이미 보유한다. 규제상 감사 로그가 필요하면 **일 1회 집계 이벤트 `notification.digest`** 가 탈출구다 |
| ③ | **자격증명은 환경변수 전용** | 변경 시 컨테이너 재시작이 필요하다는 대가를 수용 |
| ④ | **`users.email` 추가를 이번 범위에 포함** | 없으면 메일 채널이 성립하지 않는다 |
| ⑤ | **발송 시각창(quiet hours) 없이 즉시 발송** | 설정 표면을 줄인다. 필요해지면 정책에 필드 추가로 확장 |
| ⑥ | **첫 실행은 `seed`** | 설치 당일 폭주 방지(7.7) |
| ⑦ | **7.13의 나머지 이벤트는 미구현** | 갱신·배포·정책위반·에이전트끊김은 **기능 자체가 없다.** `event_type` 확장점만 남긴다 |

---

## 13. at-least-once 수용

**SMTP 서버가 메시지를 수락한 직후 프로세스가 죽으면 같은 메일이 한 통 더 갈 수 있다.**

"전송했다"와 "전송했음을 기록했다" 사이에는 원자성이 없고, 외부 SMTP 서버를 상대로 exactly-once는 **원리적으로 불가능**하다(2단계 커밋에 참여하지 않는다). 이 성질을 숨기지 않고 운영 매뉴얼에 명시한다.

완화:

- **알림함**: `ON CONFLICT (user_id, event_id) DO NOTHING` — 중복 불가.
- **웹훅**: 페이로드에 `event_id`가 있고 HMAC 서명이 붙으므로 **수신 측에서 멱등 처리 가능**하다. 매뉴얼에 "`event_id`로 중복 제거하라"를 적는다.
- **메일·Syslog**: 중복 가능. 수용한다.

---

## 14. 수신자 해석

### 14.1 적용 범위

수신자 개념은 **`email`·`inbox`에만** 적용된다. `webhook`·`syslog`는 **채널 단위 브로드캐스트**다(엔드포인트가 하나뿐이므로 수신자를 곱할 대상이 없다).

### 14.2 `recipients` jsonb 형태

```json
[
  {"kind":"owner"},
  {"kind":"role","role":"system_administrator"},
  {"kind":"user","user_id":"..."},
  {"kind":"static","email":"noc@example.com"}
]
```

### 14.3 폴백

`owner` 해석에 실패하면(담당자 미지정, 담당자에게 email 없음) → 정책의 **static 수신자로 폴백**한다.
그것도 없으면 딜리버리를 **`skipped`** 로 남기고 사유를 기록한다. 조용히 사라지지 않는다 — `GET /notification-deliveries`에서 "왜 안 왔는지"를 답할 수 있어야 한다.

---

## 15. 병렬 구현 단위

| 단위 | 범위 | 주요 파일 |
| --- | --- | --- |
| **U1** | 마이그레이션 0012 + 시드 | `migrations/0012_notifications.up/down.sql` |
| **U2** | 도메인 타입 + 멱등키 + 페이로드 빌더 | `internal/notify/types.go`, `dedup.go`, `payload.go` |
| **U3** | 저장소 | `internal/store/notifications.go` |
| **U4** | 채널 어댑터 4종 | `internal/notify/email.go`·`webhook.go`·`inbox.go`·`syslog.go` |
| **U5** | 스케줄러 + 디스패처 | `internal/notify/scheduler.go`·`dispatcher.go` |
| **U6** | 정책·채널 API | `api/handlers_notifications.go`, `api/server.go`, `api/openapi.yaml` |
| **U7** | 알림함 API | `api/handlers_inbox.go`, `api/server.go`, `api/openapi.yaml` |
| **U8** | 설정 + 배선 | `internal/config`, `cmd/server/main.go`, `docker-compose.yml` |
| **U9** | 프론트엔드(알림함 UI·배지·정책 화면) | `frontend/app/(app)/notifications/`, `frontend/lib/api.ts`, `app-sidebar.tsx` |
| **U10** | 문서 | `docs/manual/INSTALL.md`·`OPERATIONS.md`, `docs/MAP.md` |

### 15.1 머지 순서

```
U1 → (U2 ∥ U3) → (U4 ∥ U5) → U6 → U7 → U8 → U10 → U9
```

U9(프론트)를 마지막에 두는 이유는 U6·U7의 응답 타입이 확정된 뒤에야 `lib/api.ts` 타입을 쓸 수 있기 때문이다.

### 15.2 공유 파일 주의

| 파일 | 공유 단위 | 주의 |
| --- | --- | --- |
| `api/server.go`, `api/openapi.yaml` | U5 · U6 · U10 | **라우트와 명세는 반드시 같은 커밋.** 아니면 `make openapi-check` 실패. path는 `security:` 블록 **위/안**에 넣을 것 |
| `frontend/lib/api.ts`, `app-sidebar.tsx` | U7 · U8 · U10 | 사이드바 메뉴 추가와 API 타입 추가가 겹친다. 한 단위가 끝난 뒤 다음이 리베이스 |
