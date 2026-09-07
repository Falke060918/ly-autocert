# 설계 05 — 인증서 자동발급·갱신·배포 (ATC-20260907-001)

- Work Order: `ATC-20260907-001` — 정본 스펙: `docs/specs/ATC-20260907-001-auto-issuance.md`
- 작성: 2026-09-07 (planner) / 근거 조사: `.claude/tmp/autoissue/{api-docs,codebase,sensor-deploy}/summary.md`(로컬 전용) / 관련 명세: `docs/00-spec.md` 7.5~7.8장·15장 Phase 2
- 경로 주석: 문서 번호는 05다 — `04-port-autoscan.md`와의 번호 충돌을 재번호로 해소했다(2026-09-07, 오케스트레이터 결정).

**내가 이해한 설계 범위**: 내부 ACME 게이트웨이 골격 + TLS Manager 커넥터로 발급·자동 갱신을 세우고, 신설 배포 에이전트가 nginx/Apache에 인증서를 교체·reload까지 하는 전 경로(크리덴셜 관리·승인 정책·콘솔 UI·마이그레이션 0023~·감사·권한 포함)를 설계한다 — 코드는 쓰지 않는다.

**갈림길**: 게이트웨이 범위 — RFC 8555 ACME 서버를 전면 구현할지, 발급 파이프라인의 내부 추상층(RA 코어)까지만 세울지. 산출물이 실제로 달라진다 — **RA 코어로 간다**(§2.1에 근거·대안 비교). 되돌리기 쉬운 방향이라 선언 후 진행: ACME 프런트는 같은 주문 파이프라인 위의 확장점으로 보존된다.

---

## 1. 접근 요약

1. 발급의 심장은 `internal/issuance` RA 코어다 — 주문 상태 머신 + 승인 정책 + 키/CSR 생성 + upstream 커넥터 인터페이스. TLS Manager가 첫 커넥터이고, RFC 8555 ACME 서버 프런트는 확장점으로만 남긴다.
2. 비동기는 전부 기존 패턴 복제다 — 주문 워커는 알림 딜리버리 큐(리스+백오프), 갱신 스케줄러는 재스캔 스케줄러(tick+advisory lock), 배포 채널은 센서 pull 리스의 미러(신설 `agents`·`deploy_jobs`)로 만든다. 새 발명 없음.
3. 개인키는 서버가 생성해 `sensorca.Seal/Open`(AES-256-GCM)으로 봉인 저장하고, 배포 시에만 mTLS material 엔드포인트로 에이전트에 내려간다. 배포는 허용 목록 enum 기반 7단계 상태 머신(백업→원자 교체→config test→reload→검증→실패 시 롤백)이다.

---

## 2. 아키텍처

### 2.1 게이트웨이 범위 결정 — 이번에 어디까지 서는가

**결정: 발급 파이프라인의 내부 추상층(RA 코어)까지 세운다. RFC 8555 ACME 서버 프런트(certbot 대면 HTTP 엔드포인트)는 이번에 구현하지 않고 확장점으로 남긴다.**

00-spec P2 골격(1399~1431행)은 "내부 ACME-compatible RA/게이트웨이"를 말하는데, 그 실체는 두 층이다 — ① 요청을 받아 정책·승인·감사를 거쳐 upstream CA에 주문하는 **파이프라인(RA 코어)**, ② certbot류 표준 클라이언트가 붙는 **ACME 프로토콜 프런트**. 이번 작업의 사용자 성공 기준(스펙 §성공 기준)은 콘솔 발급 + 우리 에이전트 배포이고, certbot 호환은 등장하지 않는다. 따라서 ①을 세우고 ②는 자리만 남긴다.

대안 비교:

| 대안 | 장점 | 단점 | 비용 |
| --- | --- | --- | --- |
| (a) RFC 8555 ACME 서버 전면 구현 | P2 완료 조건("certbot이 발급받는다")까지 한 번에 | 이번 스펙의 성공 기준에 없는 표면(디렉토리·논스·계정·챌린지 서버)을 먼저 만들게 됨. 배포가 우리 에이전트인 이 범위에선 사용처 0 | 대(파이프라인의 2배 이상) |
| **(b) RA 코어 + 커넥터 (권고)** | 스펙 성공 기준 전부를 최소 표면으로 충족. 주문 파이프라인·상태 머신·감사가 곧 ②의 하부가 됨(newOrder→주문 생성, finalize→CSR 제출, 챌린지→검증 상태로 1:1 대응) | P2 완료 조건 중 certbot 항목은 미충족으로 남음(후속 작업) | 중 |
| (c) 게이트웨이 없이 핸들러에서 TLS Manager 직호출 | 최속 | 두 번째 CA(AD CS·Vault PKI)가 붙는 순간 전부 재작업. 00-spec P2 골격·STATE 확정 결정("중앙 게이트웨이 + 표준 클라이언트 골격")과 정면 충돌 | 소(당장) / 대(후불) |

(b)가 이기는 이유 — 스펙이 요구하는 전부를 담으면서, 00-spec의 골격 결정과 어긋나지 않고, ②를 나중에 붙일 때 파이프라인을 다시 짜지 않는 유일한 지점이기 때문이다. 상태 어휘·주문 모델을 프로토콜 중립으로 잡아 ACME 대응을 보존한다(§4.1).

### 2.2 구성요소 지도

```
[콘솔 UI]──REST──[api 핸들러]──┐
                              │(주문 생성·승인·조회)
                        [internal/issuance  RA 코어]
                          ├ 상태 머신 + 주문 워커(리스·폴링·백오프)
                          ├ 갱신 스케줄러(창 판정)
                          ├ 키/CSR 생성 + 봉인(sensorca.Seal/Open 재사용)
                          └ upstream Connector 인터페이스
                                └ tlsmanager 커넥터 ──HTTPS──> 외부 TLS Manager
                                └ (확장점: ACME·AD CS·Vault PKI 커넥터 / ACME 서버 프런트)
[8443 게이트웨이(기존 리스너 공유)]
  ├ /sensor/v1/*  기존 sensorgw (불변)
  └ /agent/v1/*   신설 agentgw — enroll·lease·heartbeat·material·complete
                        └──mTLS──> cmd/agent (신설 바이너리, nginx/Apache 배포 상태 머신)
```

### 2.3 upstream 커넥터 인터페이스 (계약 스케치)

`internal/issuance/upstream` 패키지. 커넥터는 상태를 갖지 않고 `ca_connections` 행(복호된 크리덴셜 포함)을 받아 구성된다.

```
TestConnection(ctx) error                          // 로그인 시도만 — 연결 테스트 버튼의 실체
ListProducts(ctx) ([]Product, error)               // product_id 선택 UI용
PlaceOrder(ctx, OrderSpec) (Placed, error)         // CSR 필수 동봉 + domain_validations 동봉(생성 즉시 검증 트리거)
FetchOrder(ctx, ref) (Snapshot, error)             // 폴링 지점 — 정규화 상태 + 검증 자료 + 발급물
Renew(ctx, ref, OrderSpec) (Placed, error)         // 새 CSR 동봉(갱신은 새 키 — §5)
Cancel(ctx, ref) error                             // 사용자 취소 시 upstream 취소
```

- `Snapshot`은 upstream 상태를 내부 정규화 enum으로 번역해 돌려준다. TLS Manager 매핑: `issued→issued / pending→pending / cancelled·refunded→cancelled / expired→expired / error→error / revoked→revoked`. 도메인 검증 자료(`dns_txt_record`·`http_file_url`·`validation_key`·검증 상태)와 발급물(`x509_base64`·`ca_chain`)을 함께 싣는다.
- 토큰 관리(`POST /v1/auth/login`·`refresh-token`)는 커넥터 내부 구현이다 — 연결별 인메모리 캐시 + 만료 전 refresh + 401 시 재로그인 1회. **토큰은 DB에 저장하지 않는다**(봉인 저장 대상은 secret뿐).
- HTTP 클라이언트는 `internal/notify/channels.go` 웹훅 선례를 따른다 — MinVersion TLS 1.2, 타임아웃, 시스템 루트로 서버 검증(InsecureSkipVerify 금지). 프록시는 Go 표준 `HTTP(S)_PROXY` 환경변수를 그대로 존중한다. 오류 문자열에 요청 본문·크리덴셜·키를 싣지 않는다(변수 이름·HTTP 상태·오류 종류만 — notify 규약 재사용).
- Revoke·EAB·multi-period(extend)는 이번 인터페이스에 넣지 않는다 — 필요해질 때 메서드 추가(확장점 언급만).

### 2.4 나머지 구조 대안 검토

**배포 작업 채널** — 결정: 신설 `agents` + `deploy_jobs` 테이블에 센서 pull 리스 패턴을 복제한다.

| 대안 | 장점 | 단점 | 비용 |
| --- | --- | --- | --- |
| (a) `scan_jobs`에 job_type='deploy' 추가 + sensors 테이블 공용 | 테이블 재사용 | 4곳 폐쇄 enum 개봉(0010 CHECK·domain·wire·runner) + `sensor_id` FK가 sensors를 가리켜 에이전트 신원을 센서에 욱여넣게 됨. 00-spec 7.8이 센서/에이전트를 계층으로 명시 분리. 돌아가는 스캔 경로에 회귀 위험 | 중 + 회귀 위험 |
| **(b) `agents`·`deploy_jobs` 신설 + 같은 리스 패턴 복제 (권고)** | 스캔 경로 무접촉(회귀 0). 스펙 7.8 계층 구분과 일치. 패턴(FOR UPDATE SKIP LOCKED·TTL·하트비트·reaper)은 검증된 복사 | 코드 중복 소량(리스 SQL 골격) | 중 |
| (c) 서버가 SSH로 푸시 | 에이전트 설치 불요 | 00-spec 7.8 원칙 위반("SSH 직접 접속을 기본값으로 쓰지 않음", outbound-only) + 서버가 대상 서버 자격증명을 보관하게 됨 | 탈락 |

**키 보관 위치** — 사용자 확정: 서버가 키를 생성하고 CSR만 외부 제출(스펙 원문). 중앙 보관이라 같은 인증서를 여러 대상에 재배포·재시도할 수 있다. 검토했던 대안(에이전트가 로컬 키 생성·CSR만 상행 — 키가 호스트를 안 떠나는 장점)은 다중 대상 배포·재배포가 안 되고 사용자 결정과 어긋나 채택하지 않는다. 기록만 남긴다.

**도메인 검증 자동화 수준** — 결정: 이번에는 **콘솔 안내 + 운영자 수작업 + 자동 폴링**이다(§4.4). 에이전트가 http_file을 대상 서버 웹루트에 자동 배치하는 방식과 DNS 제공자 API 커넥터는 확장점으로만 남긴다 — 전자는 배포 에이전트 완성(D) 후에야 가능하고, 후자는 폐쇄망 고객의 DNS가 제각각이라 이번 범위의 최소 설계가 아니다.

### 2.5 비가역 결정

1. **`ISSUANCE_ENC_KEY` 도입(hex 64, AES-256-GCM 봉인 키)** — 이 키를 잃으면 봉인된 크리덴셜·개인키 전부 복호 불가다. 운영 문서에 키 백업 지침을 반드시 싣는다. `SENSOR_CA_ENC_KEY`와 분리하는 이유: 용도(센서 CA vs 고객 발급 키)와 회전 반경이 다르다. 재봉인(키 회전) 도구는 확장점.
2. **외부 부작용** — 주문·발급·취소는 외부 CA에 기록이 남는다. 자동 테스트는 httptest 가짜 TLS Manager만 쓰고, 실서버 E2E는 데모 계정으로 사용자 입회 하에 한다.
3. **agents/sensors 분리 테이블** — 나중에 합치려면 큰 마이그레이션이다. 7.8 계층 구분이 명세 정본이라 분리로 확정한다.
4. 주문 상태 enum 어휘(§4.1)는 openapi·frontend union·DB CHECK 세 곳에 박힌다 — 첫 릴리스 전에 확정하고 이후 변경은 세 곳 동시 커밋(GOTCHAS 규약).

---

## 3. 데이터 모델 (마이그레이션 0023~0026 — 번호 갭 금지, max+1)

**0023 — `ca_connections` + `issuance_settings`** (단위 A)

- `ca_connections`: `id uuid PK` / `name` / `type` CHECK('tlsmanager') — 커넥터 확장 자리 / `base_url` / `app_id` / `secret_sealed bytea` + `secret_nonce bytea`(Seal 산출) / 기본 subscriber 정보(회사·담당자·이메일 — TLS Manager 주문 전 가입자 필요) / `upstream_subscriber_id`(첫 주문 시 지연 기입) / `active bool` / `created_at`·`updated_at`. secret 평문 컬럼 없음.
- `issuance_settings`: 단일 행(시드) — `approval_required_new bool DEFAULT true` / `auto_renew bool DEFAULT true`. 갱신 창 수치는 설정이 아니다(CA 제약 고정 — §4.3).

**0024 — `agents`** (단위 B)

- `sensors`(0009) 미러: `id` / `name` / `status` CHECK(pending·active·disabled) / `enroll_token_hash`(SHA-256 hex, 24h 만료) / `cert_fingerprint`(mTLS 대조) / `last_seen_at` / 플랫폼 정보(os·hostname — 하트비트 갱신). 토큰 평문은 발급 응답에만.

**0025 — `issuance_orders` + `issuance_order_events` + `issuance_private_keys`** (단위 C)

- `issuance_orders`: `id uuid PK` / `ca_connection_id FK` / `origin` CHECK('manual','renewal') — scan_jobs.origin 선례 / `renewal_of_certificate_id FK NULL` / `status` CHECK(§4.1 어휘) / `upstream_order_code` / `upstream_status` / 요청 스펙(`dns_names text[]`·`key_algorithm` CHECK(rsa2048·rsa3072·rsa4096·ecdsa_p256·ecdsa_p384)·`validity_days`·`product_id`·`signing_ca_type`) / `domain_validation jsonb`(폴링 스냅샷 — 표시 전용) / `certificate_id FK NULL`(발급 결과 인벤토리 연결) / `requested_by FK NULL`·`approved_by FK NULL`·`approved_at` / 워커 제어(`next_action_at`·`leased_until`·`attempt_count`·`last_error`) / `created_at`·`finished_at`.
- 부분 유니크 `uq_issuance_orders_active_renewal ON (renewal_of_certificate_id) WHERE finished_at IS NULL` — 같은 인증서 활성 갱신 주문 1개 보장. **ON CONFLICT 아비터로 쓰지 않는다**(부분 인덱스 42P10 — GOTCHAS). 선판정 + 23505 흡수(scan dedup 패턴).
- `issuance_order_events`: `order_id FK` / `at` / `kind`(created·approved·order_placed·validation_pending·validated·issued·inventory_registered·deploy_started·deploy_finished·failed·retried·cancelled) / `detail jsonb`. 상세 화면 타임라인용 — 감사 로그는 업무 행위만 남기고(§8), 폴링 전이까지 감사에 넣으면 해시 체인 advisory lock 폭만 넓힌다.
- `issuance_private_keys`: `id` / `order_id FK UNIQUE` / `certificate_id FK NULL`(발급 후 기입) / `key_algorithm` / `key_sealed bytea`·`key_nonce bytea` / `csr_pem text`(비밀 아님) / `created_at`. 평문 키 컬럼 없음. 읽기 경로는 딱 2개 — 에이전트 material 릴리스(§7.4), 갱신 시 새 키 생성이라 재읽기 없음.

**0026 — `deployment_targets` + `deploy_jobs`** (단위 D)

- `deployment_targets`(배포 바인딩 — 갱신 자동 재배포의 근거): `id` / `agent_id FK` / `kind` CHECK('nginx','apache') / `service_unit` CHECK('nginx','httpd','apache2') / `cert_path`·`key_path`·`chain_path NULL`(절대 경로 — 운영자 입력) / `verify_host`(기본 127.0.0.1)·`verify_port`(기본 443)·`sni_name NULL` / `current_certificate_id FK NULL` / `active bool` / `created_by`.
- `deploy_jobs`: `id` / `deployment_target_id FK` / `agent_id FK` / `order_id FK NULL` / `certificate_id FK` / `status` CHECK(pending·leased·running·completed·failed·rolled_back) / `leased_until`·하트비트 / `steps jsonb`(단계별 결과 — 에이전트 complete 보고) / `error text` / 시각 컬럼. 리스 상태 머신은 `sensorjobs.go` 미러(FOR UPDATE SKIP LOCKED·TTL 90s·하트비트 연장·만료 reaper→failed).

단위 E는 마이그레이션이 없다(0025의 부분 유니크가 갱신 dedup을 이미 보장).

---

## 4. 발급·갱신 상태 머신과 워커

### 4.1 주문 상태 (00-spec 7.6 어휘 준수 + 1개 확장)

```
requested → approval_pending → issuing → awaiting_validation ⇄ issuing → issued → deploying → completed
                                  │                                        │(배포 없음)└→ completed
실패·종결: cancelled / issue_failed / deploy_failed / rolled_back / manual_required
```

- `awaiting_validation`은 7.6 흐름에 없는 추가 상태다 — 운영자 조치(DNS TXT/HTTP 파일 배치)가 필요한 구간을 UI·워커가 구분해야 해서 넣는다. 7.6의 `VERIFYING`은 배포 잡 내부 단계(§7.3)로 흡수하고 주문 수준 상태로는 쓰지 않는다 — 안 쓰는 상태를 enum에 만들지 않는다.
- 생성 시 분기: `approval_required_new`(또는 origin='renewal'이면 auto 정책)에 따라 `approval_pending` 또는 곧장 `issuing` 큐로. 승인은 `POST /issuance/orders/{id}/approve`.
- `issued` 도달 시 같은 처리에서 — 인증서 파싱(`certparse.Describe` — 인증서만 전달, 개인키는 이 경로에 절대 넣지 않는다: `ErrPrivateKeyFound` 거부 규약 유지) → 기존 `upsertCertificateSQL` 재사용으로 **인벤토리 자동 등재**(00-spec P2 완료 조건) → `issuance_orders.certificate_id`·`issuance_private_keys.certificate_id` 기입. 위치(location)는 만들지 않는다 — 배포·재스캔이 network 위치를 자연히 붙인다.
- 배포 바인딩이 없으면 `issued → completed`로 닫는다(단위 C 시점의 종결). 단위 D가 `deploying`을 연다.
- 교체 판정은 손대지 않는다 — 갱신 후 옛 인증서의 `replaced` 전이는 기존 `ReconcileReplacedCertificates`(관측 기반)가 그대로 수행한다. 발급 쪽에서 중복 구현 금지.

### 4.2 주문 워커 — 알림 딜리버리 큐 패턴 이식

- 배선: `cmd/server/wiring.go`에 `startIssuanceWorker` — 기존 stop 규약(`stop func()` + main defer). tick 30초.
- 클레임: tx1에서 `pg_try_advisory_xact_lock(x'69737375')`(신규 키 — 기존 notify·rescan·sched·adminip·setup 키와 상이) + 처리 대상(`status IN (issuing, awaiting_validation) AND next_action_at <= now() AND (leased_until IS NULL OR 만료)`)을 `FOR UPDATE SKIP LOCKED`로 집어 `leased_until = now()+2m` 기록. **외부 HTTP는 tx 밖에서** 수행하고 결과를 tx2로 기록한다(딜리버리 디스패처와 같은 이유 — 긴 외부 호출을 DB tx에 가두지 않는다). 고아 리스는 pending 회수(딜리버리 규약).
- 단계당 1행위: `issuing`+order_code 없음 → PlaceOrder(멱등 — order_code가 이미 있으면 재주문 금지, FetchOrder로 폴백) / `awaiting_validation`·`issuing` → FetchOrder 폴링 / `issued` 스냅샷 → 발급물 저장·인벤토리 등재.
- 폴링 주기(next_action_at): awaiting_validation 2분, issuing(upstream 처리 중) 1분. 일시 오류 백오프 1m×2^(n-1)·상한 30m, 10회 초과 시 `issue_failed`(사유 저장 — sanitize). upstream 종결 상태(cancelled·expired·error·revoked)는 즉시 `issue_failed`(사유에 upstream 상태 명기).
- `retry`: `issue_failed`에서만 — order_code가 살아 있으면 폴링 재개, 죽었으면(만료·취소) 새 주문 발주. attempt 리셋. `cancel`: 미발급 상태에서만 — upstream Cancel 호출 후 `cancelled`.

### 4.3 갱신 스케줄러 — 재스캔 스케줄러 패턴 이식

- `startRenewalScheduler`, tick 1시간, `pg_try_advisory_xact_lock(x'72656e6577')`(신규 키).
- 갱신 창 판정(외부 API 제약을 로컬 공식으로 그대로): `renew_from = not_after - LEAST((not_after - not_before)/3, interval '30 days')`, 대상 조건 = `now() >= renew_from AND now() < not_after`. 90일 인증서는 D-30, 1년 인증서도 D-30(30일 상한이 지배), 60일 인증서는 D-20(1/3이 지배).
- 대상 집합: **우리가 발급한 인증서만** — `EXISTS(완료된 issuance_orders where certificate_id = c.id)`로 원 주문(order_code·연결·키 계보)을 아는 것만 자동 갱신한다. 외부 발급분(스캔 발견·수동 업로드)은 대상 아님 — 인증서 상세에 "자동 갱신 불가(외부 발급분)" 표기, 확장점만 남김. status revoked·replaced 제외.
- 생성: `auto_renew=true`일 때만 주문 자동 생성(origin='renewal', 승인 생략 — 스펙 가정 그대로). `auto_renew=false`면 생성 자체를 안 한다(꺼진 스위치가 승인 대기 주문을 몰래 쌓지 않는다). tick당 상한 20건. dedup은 0025 부분 유니크 + 선판정.
- 갱신 실행: 워커 공통 경로에서 PlaceOrder 대신 `Renew(원 주문 order_code, 새 CSR)` 호출. upstream이 "too early"류 4xx를 주면 실패가 아니라 next_action_at을 +24h(로컬 판정과 CA 판정의 오차 흡수). 갱신이 재검증을 요구하면 `awaiting_validation`으로 파킹되고 콘솔 주문 목록의 "조치 필요" 배지로 드러난다(§9) — 알림 채널 연동은 확장점.
- 갱신 완료(issued) 시: 새 인증서 인벤토리 등재(공통) + **단위 E에서** 옛 인증서에 걸린 활성 `deployment_targets`마다 deploy_jobs 자동 생성 → 전건 completed면 주문 `completed` + 바인딩 `current_certificate_id` 갱신. 하나라도 실패면 `deploy_failed`(성공한 바인딩은 유지 — 단계 기록으로 구분).

### 4.4 도메인 검증 — 누가 어떻게 수행하나

- 방식은 주문 생성 폼에서 도메인별 선택: `dns_txt_record` 또는 `txt_http_file`(TLS Manager DV 방식 중 자동화 가능한 2종만 노출 — 이메일류는 제외). 주문 생성 시 `domain_validations[]`를 동봉해 upstream이 검증 요청을 즉시 트리거하게 한다.
- 수행 주체는 **운영자**다: 주문 상세가 폴링으로 받아온 검증 자료(TXT 레코드 이름·값, HTTP 파일 URL·`validation_key`)를 복사 버튼과 함께 표시하고, 운영자가 DNS/웹서버에 배치하면 upstream 검증 통과를 폴링이 감지해 자동 진행한다. 검증 자료는 비밀이 아니다(공개 검증용 값).
- 확장점(이번 미구현): 배포 에이전트의 http_file 자동 배치, DNS 제공자 커넥터. 재검증 요구 조건(갱신 시 기존 검증 재사용 기간)은 CA 정책 의존이라 데모 서버 실측으로 확인한다 — 구현 단위 C의 검증 항목.

---

## 5. 키·CSR 생성과 봉인 저장

- 생성: `crypto/rand` + `crypto/rsa`·`crypto/ecdsa` 표준 라이브러리만(자체 구현 금지 규칙). 알고리즘 허용 목록은 00-spec 7.5의 5종(RSA 2048/3072/4096, ECDSA P-256/P-384) — DB CHECK와 UI 선택지가 같은 목록.
- CSR: `crypto/x509.CreateCertificateRequest`, CN + SAN(dns_names). base64로 PlaceOrder에 동봉(스펙상 PEM 헤더 허용).
- 봉인: `sensorca.Seal(key, plaintext)`/`Open` 직접 import(패키지 수준 범용 함수 — 확인 완료, 추출 리팩토링 불요). 봉인 키는 신설 `ISSUANCE_ENC_KEY`(hex 64) — config 패턴은 `SENSOR_CA_ENC_KEY`와 동일: 미설정이어도 기동 정상, 발급·연동 기능만 사유 있는 비활성(zero-config 무영향 규약).
- 무거운 연산(키 생성·봉인)은 tx 밖에서 하고 tx는 INSERT만(감사 advisory lock 폭 최소화 — bcrypt 선례).
- 갱신은 **새 키 생성이 기본**이다(재사용 옵션 없음 — 키 수명을 인증서 수명에 맞추는 관행, TLS Manager renew의 CSR 동봉으로 실현). 옛 키 행은 남는다(옛 인증서가 아직 서비스 중일 수 있다).
- 금지선 재확인: 평문 키는 DB·로그·오류 문자열·API 응답·테스트 픽스처 어디에도 없다. 테스트 키는 테스트가 매번 생성한다.

---

## 6. 크리덴셜(app_id/secret) 관리

- 입력: 설정 화면 `settings/issuance`(§9)에서 base_url·app_id·secret·기본 subscriber 정보 입력. secret은 쓰기 전용 — 응답에는 "설정됨" 마스킹만, 수정 시 비우면 기존 유지.
- 저장: `Seal(ISSUANCE_ENC_KEY, secret)` → `secret_sealed`+`secret_nonce`. `ISSUANCE_ENC_KEY` 미설정이면 저장 요청이 사유 있는 4xx(화면에 경고 배너).
- 사용: 커넥터 구성 시점에만 복호해 메모리 보관, 로그인 토큰 교환 후 즉시 참조 해제. 복호 값은 로그·오류에 안 싣는다.
- 연결 테스트: `POST /ca-connections/{id}/test` → TestConnection(로그인 시도) → 성공/실패·사유 응답 + 감사. 크리덴셜 유효성의 유일한 판정 지점.
- `.env` 직접 편집은 에이전트가 못 한다(GOTCHAS) — `ISSUANCE_ENC_KEY` 추가는 INSTALL·OPERATIONS 문서에 적고 사용자 수작업 항목으로 보고한다.

---

## 7. 배포 에이전트

### 7.1 신원·등록·채널 (센서 미러)

- 신설 바이너리 `cmd/agent` — enroll/run 서브커맨드, slog JSON, 정적 크로스 빌드. **이번 지원은 Linux만**(nginx/Apache 우선이 곧 Linux 우선 — 스펙 확인본). Windows 크로스 빌드는 되지만 배포 대상 아님을 문서 명기. 상태 디렉토리 `/var/lib/autocert-agent`(0700/0600 — 자기 mTLS 신원만, 고객 개인키는 여기 저장 금지).
- 등록: 센서와 동일 — 1회용 토큰(해시만 저장·24h) → 로컬 키쌍+CSR → sensorca가 클라이언트 인증서 발급 → 이후 mTLS. enroll 레이트리밋(1rps·burst 5) 재사용. 설치 스크립트 `scripts/install-agent.sh`는 `--token-file -`+stdin 파이프 형(콘솔 명령 예시도 처음부터 token-file 형 — STATE 잔여 항목의 재발 방지).
- 채널: 8443 리스너 공유, 신설 `internal/agentgw`가 `/agent/v1/*`(enroll·renew·lease·start·heartbeat·complete·material) 담당. `requireAgent` 미들웨어는 클라이언트 인증서 fingerprint를 **agents 테이블만** 대조한다 — 센서 인증서로 에이전트 API를 못 부르고 역도 같다(테이블 분리가 곧 경계). 기존 `/sensor/v1/*` 코드 경로는 불변. DTO는 신설 `internal/agentwire`(stdlib 전용, sensorwire 미러) — job kind는 'deploy' 하나로 시작하는 폐쇄 enum, 에이전트는 모르는 kind를 실행 없이 거부.
- 에이전트 프로세스는 root 권한이 필요하다(인증서 파일 소유권·systemctl reload) — 설치 문서에 명기, systemd 유닛으로 설치.

### 7.2 배포 잡 페이로드 — 허용 목록만

리스 응답에 비밀 없음: `{job_id, kind:'deploy', target{kind, service_unit, cert_path, key_path, chain_path, verify_host, verify_port, sni_name}, expected_fingerprint, lease_expires_at}`. 자유 문자열 명령 필드는 어디에도 없다 — 실행할 명령은 에이전트 내부 고정 표가 kind·service_unit enum에서 결정한다:

| kind | config test | reload |
| --- | --- | --- |
| nginx | `nginx -t` | `systemctl reload nginx` |
| apache | `apachectl configtest`(httpd는 `httpd -t`) | `systemctl reload {httpd|apache2}` |

- "재기동"의 답: 인증서 적용은 **graceful reload**로 충분하고 무중단이다(nginx/Apache 모두 재기동 불요) — 이것이 기본이자 유일 동작. full restart는 허용 목록 플래그 확장점으로만 남긴다(트래픽 단절이라 기본 금지).
- 명령 실행부는 러너 인터페이스로 추상화해 테스트에서 가짜 실행기로 대체한다(실 systemctl 없이 상태 머신 검증).

### 7.3 배포 상태 머신 (00-spec 496~506 원자적 배포 7단계 + 롤백)

```
precheck → backup → write_temp → swap(rename) → config_test → reload → verify → cleanup → completed
                              (swap 이후 실패) → rollback(백업 복원 + reload) → rolled_back
                              (swap 이전 실패) → failed (변경 0)
```

- precheck: 경로 존재·쓰기 가능·service_unit 존재·**기준선 config test 통과**(이미 깨진 설정이면 손대기 전에 실패 — 우리 탓 아님을 단계 기록으로 구분) · 대상 파일의 현 지문 == 바인딩 `current_certificate_id` 지문 대조(다르면 경고 기록 후 진행) · 멱등 단락: 목적지 지문이 이미 `expected_fingerprint`면 즉시 completed.
- backup: 같은 디렉토리에 `<파일>.autocert-bak.<ts>`(권한 보존). write_temp: 같은 디렉토리 임시 파일(키 0600·인증서 0644, fsync) 후 rename(원자 교체 — 같은 파일시스템 보장을 위해 같은 디렉토리). chain_mode: nginx는 fullchain을 cert_path에, apache는 chain_path 별도(NULL이면 번들).
- verify: 로컬 TLS 핸드셰이크(`verify_host:verify_port`, SNI=sni_name) → 리프 SHA-256 == expected_fingerprint. 신뢰 검증이 아니라 지문 대조이므로 InsecureSkipVerify(수집 전용 규약과 동일 맥락).
- rollback: 백업 복원 → config test → reload → 옛 지문 재확인 → rolled_back 보고. cleanup: 성공 시 백업 삭제(스펙 "성공 시 백업 정리").
- 각 단계 결과(시각·성공 여부·표준 출력 요약 — 키 내용 금지)를 `steps jsonb`로 complete 보고. 하트비트 30s, 리스 만료는 reaper가 failed 처리.
- 배포 후 인벤토리 반영은 기존 재스캔이 자연 수행한다(만료 임박 가속 티어 6h/1h) — 발급 쪽에서 스캔을 억지로 발주하지 않는다(discovery 내부 무접촉).

### 7.4 개인키 전달 (서버→에이전트 첫 비밀 하행)

- 리스에 키를 싣지 않는다. 에이전트가 리스 확보 후 `GET /agent/v1/jobs/{id}/material`(mTLS) 호출 → 서버가 봉인 키를 복호해 `{cert_pem, chain_pem, key_pem}` 응답. 조건: 그 잡이 **그 에이전트의 leased/running 상태일 때만**. 응답 캐시 금지·본문 로그 금지.
- 수령마다 감사 `issuance.key.release`(order·agent·job id만 — 자료 없음). 재시도는 재수령 허용(잡 상태 조건이 경계).
- 에이전트는 키를 메모리에서 바로 대상 경로 임시 파일(0600)로 쓴다 — 상태 디렉토리·로그·환경변수 경유 금지, 완료 후 버퍼 best-effort 소거. 인벤토리 업로드 경로(`certparse`)와는 완전 분리 — 그쪽의 `ErrPrivateKeyFound` 전면 거부는 그대로 산다.

---

## 8. API 계약·권한·감사

**엔드포인트(8080, openapi.yaml 반영 — `security:` 블록 아래 paths 추가 금지, 신규 enum은 openapi.yaml + `frontend/lib/api.ts` 같은 커밋)**

| 엔드포인트 | 역할(권한 검사 — 요청마다) |
| --- | --- |
| GET/POST `/ca-connections`, PATCH/DELETE `/{id}`, POST `/{id}/test`, GET `/{id}/products` | system_administrator·pki_administrator |
| GET/PATCH `/issuance/settings` | system_administrator·pki_administrator |
| POST `/issuance/orders`(생성), POST `/{id}/cancel`·`/retry` | system_administrator·pki_administrator |
| POST `/issuance/orders/{id}/approve` | system_administrator·pki_administrator·security_administrator(01-plan: 보안 관리자 승인 권한) |
| GET `/issuance/orders`(목록·필터), GET `/{id}`(상세+이벤트) | 전 인증 역할 조회 가능(viewer 포함 — certificates 조회와 같은 선) |
| POST `/agents/tokens`, GET `/agents`, PATCH `/agents/{id}`(비활성) | system_administrator (sensors API 미러) |
| GET/POST `/deployment/targets`, PATCH/DELETE `/{id}` | system_administrator |
| POST `/issuance/orders/{id}/deploy`(바인딩 선택·생성+잡 발주), GET `/deployment/jobs?order_id=` | system_administrator |

- 승인자=요청자 허용(역할 검사만 — 감사에 둘 다 기록). 4-eyes 강제는 정책 확장점.
- 8443 `/agent/v1/*`는 openapi 밖(센서 게이트웨이와 같은 취급 — `TestOpenAPISpecMatchesRoutes` 대상은 8080만).

**감사 이벤트(전건 변경 tx의 마지막 문장 — GOTCHAS 규약)**

`ca_connection.create/update/delete/test` · `issuance.settings.update` · `issuance.order.create/approve/cancel/retry`(actor=사용자) · `issuance.order.issued/failed`(actor NULL — 워커, origin으로 구분) · `issuance.key.release` · `agent.token.issue/enroll/renew/disable` · `deploy.target.create/update/delete` · `deploy.job.create`(actor=사용자 또는 NULL(갱신 자동)) · `deploy.job.finish`(completed/failed/rolled_back, actor NULL). 폴링 폴백·하트비트류는 감사 대상 아님(order_events가 담당 — advisory lock 폭 관리).

---

## 9. 콘솔 UI

- `settings/issuance/`(신설, 단위 A): CA 연동 카드(입력·마스킹·연결 테스트 버튼·결과 표시) + 정책 카드(승인 요구·자동 갱신 스위치) + `ISSUANCE_ENC_KEY` 미설정 경고 배너. settings 하위 내비에 항목 추가.
- `agents/`(신설, 단위 B): `sensors/page.tsx` 미러 — 토큰 발급 다이얼로그(1회 표시, 설치 명령 예시는 `--token-file` 형)·목록(last_seen·상태)·비활성. 전역 사이드바에 "에이전트" 항목.
- `issuance/`(신설, 단위 C): 주문 목록(상태 필터·"조치 필요" 배지 = awaiting_validation+approval_pending, `page_size` ≤200 준수) / 신규 발급 폼(연결·product(ListProducts 라이브)·도메인 목록·키 알고리즘 5종·유효기간·도메인별 검증 방식) / 상세 `issuance/[id]/`(상태 타임라인=order_events, 검증 자료 복사 UI, 승인/취소/재시도 버튼, 발급 결과→인증서 상세 링크). 전역 사이드바 "발급" 항목.
- 주문 상세 배포 섹션(단위 D): ISSUED에서 "배포" — 에이전트·기존 바인딩 선택 또는 새 바인딩 폼(kind·service_unit·경로·검증 포트) → 잡 진행·단계 로그·롤백 결과 표시.
- 인증서 상세 보강(단위 E): 자동 갱신 대상 여부·갱신 예정일(renew_from)·연결된 갱신 주문 링크. 외부 발급분은 "자동 갱신 불가" 표기.
- 상태 union·API 타입은 `frontend/lib/`에 도메인별 파일 신설(`issuance.ts`·`agents.ts`·`deploy.ts`) — 기존 `api.ts` 클라이언트 재사용. URL 쿼리는 화면별 네임스페이스(파라미터 누출 GOTCHA).

---

## 10. 설정 키·운영 문서·관측

- 신규 env: `ISSUANCE_ENC_KEY`(hex 64 — 필수 아님, 미설정 시 발급 기능 비활성). `.env.example` 추가는 사용자 수작업(GOTCHAS), INSTALL.md·OPERATIONS.md에 키 생성·백업 지침 명기.
- 신규 문서: `docs/manual/AGENT.md`(SENSOR.md 미러 — 설치·등록·권한(root)·지원 대상 Linux nginx/Apache) / OPERATIONS.md에 발급·갱신·배포 운영 절(승인·검증 조치·롤백 대응). CHANGELOG는 단위마다 `## Unreleased`에.
- Prometheus 메트릭 최소셋은 이 작업 범위 밖(P2 완결 시 별도) — 이번에는 구조화 로그·감사·order_events로 관측한다.
- 테스트 규약: DB 테스트는 컨테이너+`-p 1` 전체 실행(GOTCHAS), 커넥터는 httptest 가짜 TLS Manager로만(외부 호출 금지), 에이전트 상태 머신은 가짜 실행기+임시 디렉토리, COALESCE/CASE 파라미터 캐스트 명시.

---

## 11. 위험·가정·필요물

**위험**
1. 데모 스펙≠운영 스펙 가능성(스펙 §가정 1) — 커넥터를 얇게 유지하고 매핑 표를 한 파일에 모아 차이 흡수 지점을 좁힌다.
2. 갱신 시 재검증 요구 조건 미상 — awaiting_validation 파킹 + 배지로 드러나게 설계했고, 실제 빈도는 단위 C 검증에서 데모 실측.
3. `ISSUANCE_ENC_KEY` 분실 = 봉인물 전손 — 운영 문서 백업 지침이 완화책, 재봉인 도구는 확장점.
4. 배포 대상 경로는 운영자 수기 입력 — 오입력은 precheck(존재·쓰기·기준선 config test)가 1차 방어. 에이전트의 설정 파일 자동 발견은 확장점.
5. 사이드바 등 공유 UI 파일 — Wave 경계로 순차 편집되게 단위를 배치했다(§12 파일 경계).

**가정(기본값으로 닫음 — 산출물이 갈리지 않는 항목)**
- 승인 역할에 security_administrator 포함, 승인자=요청자 허용(감사로 추적). 에이전트·배포는 system_administrator 전용.
- 주문 조회는 전 역할 허용. 검증 자료(TXT 값 등)는 비밀 아님.
- 갱신은 새 키 기본. reload가 기본이자 유일한 적용 동작(restart는 확장점).
- `[확인 필요]` 마커 0건 — 스펙 확인 라운드가 갈림길을 이미 닫았고, 남은 모호는 전부 기본값+기록으로 처리했다.

**필요물(구현·검증 단계)**: 실서버 E2E에 데모 계정 `app_id/secret` 필요 — 사용자 제공 대기 항목(자동 테스트는 가짜 서버로 충분).

**범위 이탈 없음**: "Phase 1만" 범위 통제 대비 확장은 스펙에 기록된 사용자 승인(2026-09-07 확인 라운드)으로 해소됐다.

---

## 12. 병렬 단위 표

Wave 구성 — 스키마·상태 머신이 수직으로 쌓이고, enum 동기화 규약(openapi+api.ts+backend 같은 커밋)이 백/프론트 분할을 금지해 각 단위는 풀스택 수직 슬라이스다. 병렬은 Wave 1의 2개뿐이고 이후는 순차다(의존이 실재해서다 — 억지 병렬로 파일 경계를 깨지 않는다).

| 하위 ID | 이름 | Wave | 의존 | 크기 | 마이그레이션 |
| --- | --- | --- | --- | --- | --- |
| ATC-20260907-001-A | CA 연동·발급 정책 설정 | 1 | 없음 | M | 0023 |
| ATC-20260907-001-B | 배포 에이전트 기반(신원·등록·관리) | 1 | 없음 | L | 0024 |
| ATC-20260907-001-C | 발급 파이프라인 코어 + 발급 UI | 2 | A | XL | 0025 |
| ATC-20260907-001-D | 배포 실행(바인딩·잡·에이전트 상태 머신) | 3 | B·C | L | 0026 |
| ATC-20260907-001-E | 자동 갱신 + 갱신 자동 재배포 | 4 | C·D | M~L | 없음 |

머지 순서 = A → B → C → D → E (Wave 1 내부는 A 먼저 — 마이그레이션 번호 0023/0024가 이 순서로 고정된다).

### A — CA 연동·발급 정책 설정
- 성공 기준: ① 설정 화면에서 base_url·app_id·secret·기본 subscriber를 저장하면 secret이 봉인 저장되고(평문 컬럼·로그·응답 0) 연결 테스트 버튼이 실제 로그인으로 성공/실패·사유를 보여준다. ② 정책 스위치 2개(신규 승인 요구 ON·자동 갱신 ON 기본)가 API로 영속되고 변경 전건 감사가 같은 tx 마지막 문장으로 남는다. ③ `ISSUANCE_ENC_KEY` 미설정 시 기동은 정상이고 저장 시도만 사유 있는 오류다.
- 하지 말 것: 주문·발급 로직 선구현 금지 / secret 반환·로그 금지 / `.env.example` 직접 수정 금지(문서+보고) / sensorca 코드 수정 금지(import만).
- 파일 경계: `migrations/0023`, `store/ca_connections.go`·`issuance_settings.go`, `api/handlers_caconnections.go`(+settings), `internal/issuance/upstream/`(인터페이스+tlsmanager의 로그인·TestConnection·ListProducts까지), `config.go`(ISSUANCE_ENC_KEY), `frontend/app/(app)/settings/issuance/`, settings 하위 내비, `frontend/lib/caconnections.ts`, openapi.yaml.

### B — 배포 에이전트 기반
- 성공 기준: ① `cmd/agent`가 센서와 같은 enroll(1회용 토큰·CSR·mTLS)로 등록되고 하트비트로 last_seen이 갱신된다. ② 콘솔 `/agents` 화면에서 토큰 발급(`--token-file` 형 설치 명령 예시)·목록·비활성이 되고 agent.token.issue/enroll/renew/disable 전건 감사가 남는다. ③ 기존 `/sensor/v1/*` 경로의 동작·테스트가 변하지 않는다.
- 하지 말 것: 배포 잡·deploy_jobs 선구현 금지(리스는 D) / sensors 테이블·sensorwire 수정 금지(미러 신설만) / Windows 배포 지원 주장 금지(문서에 Linux 한정 명기).
- 파일 경계: `migrations/0024`, `cmd/agent/`, `internal/agent/`(enroll·run 골격), `internal/agentwire/`, `internal/agentgw/`(enroll·renew·heartbeat — lease·material은 D), `store/agents.go`, `api/handlers_agents.go`, `scripts/install-agent.sh`, `frontend/app/(app)/agents/`, 전역 사이드바(에이전트 항목), `frontend/lib/agents.ts`, openapi.yaml, `docs/manual/AGENT.md`.

### C — 발급 파이프라인 코어 + 발급 UI
- 성공 기준: ① 콘솔에서 발급 요청→(정책 ON이면 승인)→주문 생성→검증 안내 표시→운영자 조치 후 폴링→발급 인증서가 기존 upsert로 인벤토리에 자동 등재되고, 주문 상세 타임라인에서 전 과정이 보인다. ② 개인키는 서버 생성·봉인 저장으로 CSR만 외부 제출되고, 평문 키가 DB·로그·응답·테스트 어디에도 없다. ③ 일시 오류는 백오프 재시도, 영구 실패는 issue_failed+사유가 화면에 남고, 업무 전이 전건 감사(같은 tx 마지막 문장)·취소·재시도가 동작한다.
- 하지 말 것: 갱신 스케줄러·배포 연동 선구현 금지 / 실 TLS Manager 대상 자동 테스트 금지(가짜 서버만) / 상태 enum을 openapi·api.ts와 다른 커밋에 넣기 금지 / `awaiting_validation` 외 스펙 밖 상태 추가 금지.
- 파일 경계: `migrations/0025`, `internal/issuance/`(상태 머신·워커·키/CSR), `upstream/tlsmanager` 확장(PlaceOrder·FetchOrder·Cancel), `store/issuance_orders.go`·`issuance_keys.go`, `api/handlers_issuance.go`, `cmd/server/wiring.go`(워커 배선), `frontend/app/(app)/issuance/`(목록·신규·상세), 전역 사이드바(발급 항목), `frontend/lib/issuance.ts`, openapi.yaml, OPERATIONS.md 발급 절.

### D — 배포 실행
- 성공 기준: ① ISSUED 주문에서 에이전트·대상(nginx/apache 허용 목록)을 골라 배포하면 precheck→백업→원자 교체→config test→reload→지문 검증까지 자동 수행되고 단계 로그가 주문 상세에 보인다. ② 검증 실패 시 백업 복원+reload 롤백이 실행되어 rolled_back 상태·옛 지문 복귀가 기록된다. ③ 개인키는 리스가 아닌 mTLS material 엔드포인트로만 내려가고(해당 에이전트의 leased/running 잡에만) 수령 전건 `issuance.key.release` 감사가 남으며, 서버→에이전트 페이로드에 자유 문자열 명령이 없다.
- 하지 말 것: 임의 셸 실행 경로·자유 문자열 명령 필드 신설 금지 / restart 구현 금지(reload만, 플래그는 확장점 주석) / 갱신 자동 재배포 훅 선구현 금지(E) / 에이전트 상태 디렉토리에 고객 키 저장 금지.
- 파일 경계: `migrations/0026`, `store/deploy_jobs.go`·`deployment_targets.go`, `agentgw`(lease·start·complete·material·reaper), `agentwire` 확장, `internal/agent/`(배포 상태 머신·실행기 추상화), `api/handlers_deploy.go`, `issuance` 상태 머신의 deploying 전이, 주문 상세 배포 섹션, `frontend/lib/deploy.ts`, openapi.yaml, AGENT.md·OPERATIONS.md 배포 절.

### E — 자동 갱신 + 갱신 자동 재배포
- 성공 기준: ① 갱신 창(`not_after - LEAST(유효기간/3, 30일)` 도달) 진입한 우리 발급 인증서에 갱신 주문이 자동 생성되고(auto_renew ON일 때, 무승인·origin='renewal'), 같은 인증서에 활성 갱신 주문이 2개 생기지 않는다. ② 갱신 발급 완료 시 옛 인증서의 활성 배포 바인딩마다 배포 잡이 자동 생성되어 교체·reload·검증까지 무인 완주하고, 바인딩 `current_certificate_id`가 새 인증서로 넘어간다. ③ 인증서 상세에 갱신 예정일·갱신 주문 링크가 보이고 외부 발급분은 "자동 갱신 불가"로 표기된다.
- 하지 말 것: 옛 인증서 replaced 전이 직접 구현 금지(기존 관측 엔진 소관) / auto_renew OFF에서 주문 생성 금지 / 갱신 창 수치의 설정화 금지(CA 제약 고정) / 마이그레이션 추가 금지.
- 파일 경계: `internal/issuance/renewal.go`(스케줄러)+워커의 Renew 경로, `upstream/tlsmanager`의 Renew, `store/issuance_renewal.go`(창 판정 쿼리 — 신규 파일), `wiring.go` 배선 1줄 구역, 인증서 상세 UI 보강, `frontend/lib/issuance.ts` 확장, OPERATIONS.md 갱신 절.

---

## 13. 영향 파일 목록 (신설 N / 수정 M)

- N `backend/migrations/0023~0026_*.up/down.sql`
- N `backend/internal/issuance/`(상태 머신·워커·갱신·키), N `backend/internal/issuance/upstream/`(인터페이스·tlsmanager)
- N `backend/internal/agentgw/`, N `backend/internal/agentwire/`, N `backend/internal/agent/`, N `backend/cmd/agent/`
- N `backend/internal/store/`{ca_connections,issuance_settings,issuance_orders,issuance_keys,issuance_renewal,agents,deploy_jobs,deployment_targets}.go
- N `backend/internal/api/handlers_`{caconnections,issuance,agents,deploy}`.go` / M `backend/internal/api/`(라우팅·권한 표) / M `backend/api/openapi.yaml`
- M `backend/internal/config/config.go`(ISSUANCE_ENC_KEY) / M `backend/cmd/server/wiring.go`·`main.go`(워커·스케줄러·agentgw 배선)
- N `frontend/app/(app)/`{settings/issuance,agents,issuance,issuance/[id]}/ / M 전역 사이드바·settings 내비 / N `frontend/lib/`{caconnections,agents,issuance,deploy}.ts / M `frontend/lib/api.ts`
- N `scripts/install-agent.sh` / N `docs/manual/AGENT.md` / M `docs/manual/INSTALL.md`·`OPERATIONS.md` / M `CHANGELOG.md`
