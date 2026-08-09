# 포트 자동 탐지 스캔 설계

상태: **설계 확정 · 구현 미착수**
근거 명세: `docs/00-spec.md` 7.2 인증서 발견, `docs/01-plan.md` Phase 1 인벤토리.

이 문서는 구현 지시서가 아니라 **설계 확정본**이다. 착수 시점이 오면 "10. 병렬 구현 단위"부터 읽는다.

---

## 1. 목적

사용자가 포트 번호를 직접 입력하지 않아도 인증서를 찾아낸다. 현재는 스캔 생성 시 포트를 지정하지 않으면 `[443]` 하나만 훑기 때문에, 비표준 포트에 붙은 인증서(8443 관리 콘솔, 6443 kube-apiserver, 5671 AMQP 등)가 인벤토리에서 통째로 누락된다.

해법은 두 단계다.

| 단계 | 이름 | 성격 | 소요 |
| --- | --- | --- | --- |
| 1 | **프리셋 즉시 스캔** | 사용자가 스캔 버튼을 누르면 곧바로 도는 기존 `scan_jobs` | 초~분 |
| 2 | **전 포트 백그라운드 스윕** | 1~65535를 청크로 쪼개 낮은 속도로 며칠에 걸쳐 도는 신규 `scan_sweeps` | 시간~일 |

1단계는 "지금 당장 대부분을 찾는다", 2단계는 "언젠가 전부를 찾는다"를 담당한다. 두 단계는 같은 실행 엔진(`scan_jobs`)을 공유하고, 2단계는 그 위에 발주자(orchestrator)를 하나 얹은 것뿐이다.

### 1.1 비목표

- 포트 스캐너(nmap 대체)가 아니다. 열린 포트 목록이 아니라 **TLS 인증서**를 얻는 것이 목적이다.
- 서비스 핑거프린팅·배너 그래빙이 아니다.
- STARTTLS 프로토콜 협상은 이번 범위가 아니다(3.2 참조).

---

## 2. 포트 문법

### 2.1 파서

신규 파일 `backend/internal/discovery/ports.go`.

```go
func ParsePortSpec(spec string) ([]int, error)
```

- 입력은 콤마로 구분된 토큰 목록이다. 토큰은 셋 중 하나다.
  - 단일 포트 — `443`
  - 범위 — `8000-9000` (시작 ≤ 끝)
  - 프리셋명 — `web` | `common` | `all`
- 토큰 앞뒤 공백은 허용하고 무시한다. 빈 토큰(`443,,8443`)은 에러다.
- 결과는 **중복 제거 후 오름차순** 정렬한다. `common,443,400-450`처럼 겹쳐 써도 안전하다.
- 1 미만 또는 65535 초과는 거부한다. 숫자가 아닌 토큰이면서 프리셋명도 아니면 거부한다.
- 에러 메시지는 어느 토큰이 문제인지 지목한다(`invalid port token "8000-"`).

### 2.2 기존 시그니처 불변

```go
func Expand(targets []Target, ports []int) []Endpoint   // 그대로 둔다
```

`ParsePortSpec`은 `Expand` **앞단**에 붙는 순수 함수다. `Expand`·`ScanEndpoint`의 시그니처를 건드리지 않으므로 센서 측 코드 영향은 **0**이다. 포트 범위 문법은 서버에서 정수 목록으로 전개된 뒤 리스에 실리므로 센서까지 내려가지 않는다.

---

## 3. 프리셋

### 3.1 정의

| 프리셋 | 개수 | 포트 |
| --- | --- | --- |
| `web` | 6 | 443, 4443, 8443, 9443, 10443, 8080 |
| `common` | 26 | `web` + 8000, 8888, 9090 / 636, 3269 / 465, 993, 995 / 989, 990 / 5061, 5671, 8883 / 2376, 2379, 6443, 10250 / 5986, 7002, 8834 |
| `all` | 65535 | 1-65535 (**스윕 전용**) |

`common`의 묶음별 의도:

| 묶음 | 포트 | 대상 |
| --- | --- | --- |
| 웹·관리 콘솔 | 8000, 8888, 9090 | 대체 HTTPS, 관측 UI |
| 디렉터리 | 636, 3269 | LDAPS, Global Catalog SSL |
| 메일(implicit) | 465, 993, 995 | SMTPS, IMAPS, POP3S |
| 파일 전송 | 989, 990 | FTPS data/control |
| 메시징·미디어 | 5061, 5671, 8883 | SIP-TLS, AMQPS, MQTTS |
| 컨테이너·오케스트레이션 | 2376, 2379, 6443, 10250 | Docker TLS, etcd, kube-apiserver, kubelet |
| 레거시 관리 | 5986, 7002, 8834 | WinRM HTTPS, WebLogic SSL, Nessus |

`all`은 스윕 전용이다. 단발 스캔에서 `all`을 지정하면 400으로 거부한다 — 대상 1개라도 6.5만 엔드포인트라 즉시 스캔의 응답성 전제를 깬다.

### 3.2 STARTTLS 포트를 제외하는 이유 (중요)

현재 `ScanEndpoint`는 TCP 연결 직후 곧바로 ClientHello를 보내는 **implicit TLS 전용**이다. 따라서 아래 포트들은 원리상 인증서를 얻지 못한다. 평문 배너가 먼저 오고 프로토콜 명령(`STARTTLS`, `AUTH TLS`, `STARTTLS` extended query 등)을 주고받아야 TLS로 승격되기 때문이다.

| 포트 | 프로토콜 | 승격 방식 |
| --- | --- | --- |
| 25, 587 | SMTP | `EHLO` → `STARTTLS` |
| 110 | POP3 | `STLS` |
| 143 | IMAP | `STARTTLS` |
| 389 | LDAP | StartTLS 확장 요청 |
| 5432 | PostgreSQL | SSLRequest 메시지 |
| 3306 | MySQL | 핸드셰이크 후 SSL 요청 플래그 |
| 1433 | MSSQL | TDS PRELOGIN |
| 21 | FTP | `AUTH TLS` |
| 3389 | RDP | X.224 협상 |

이 포트들을 프리셋에 넣으면 **전부 실패로 기록되어 노이즈만 만든다.** 그러므로 프리셋에서 제외하고, 이 문서와 UI 툴팁에 "STARTTLS 포트는 현재 지원하지 않는다"를 명시한다.

STARTTLS는 별도 확장점으로 남긴다: `ScanEndpoint` 내부에 포트별 프리앰블 핸들러 테이블을 두는 방식이 자연스럽다. 이번 범위 밖이다.

### 3.3 기본값 변경

포트를 지정하지 않은 스캔의 기본값을 `[443]` → **`common`(26개)** 으로 바꾼다. 엔드포인트 수가 26배가 되지만, 단발 스캔 기본 레이트(50pps)에서 /24 한 개(254 × 26 = 6604 엔드포인트)가 약 2분 20초다. 수용 가능하다.

---

## 4. 2단계 모델

### 4.1 리소스 관계

```
scan_sweeps (부모, 신규)
   └─ 청크 1개 = 자식 scan_jobs 1건 (기존 테이블 재사용)
```

부모는 청크를 **한 번에 하나씩** 자식 Job으로 발주한다. 선발주(pre-dispatch)하지 않는 이유는 5.3에 있다.

### 4.2 상태

**기존 `scan_jobs` 상태 enum(`pending` / `leased` / `running` / `completed` / `failed`)은 변경하지 않는다.** 프론트 폴링 타깃 필터와 센서 리스 로직이 이 값 집합에 묶여 있고, 값을 하나만 추가해도 UI가 얼어붙는 사고(GOTCHAS "Contract drift")를 이미 겪었다.

스윕은 **자기 상태 enum을 따로 갖는다.**

```
queued ──▶ running ⇄ paused
             │
             ├─▶ completed
             ├─▶ failed
             └─▶ canceled
```

| 상태 | 의미 |
| --- | --- |
| `queued` | 생성 직후. 아직 첫 청크를 발주하지 않음 |
| `running` | 청크를 순차 발주 중 |
| `paused` | 신규 청크 발주만 중단. 이미 나간 청크는 완주 |
| `completed` | 마지막 청크(`next_seq > total_chunks`)까지 종료 |
| `failed` | 청크가 재시도 상한(2회)을 넘겨 진행 불가 |
| `canceled` | 사용자가 취소. 진행 중 청크는 완주하고 신규 발주는 없음 |

### 4.3 웨이브 순서

청크 `sweep_seq`는 아래 순서로 부여한다. 정렬 기준이 곧 우선순위다.

1. **`common` 26포트** — 값어치 높은 포트를 몇 분 안에 훑는다.
2. **1-1023 (well-known)** — `common`과 겹치는 포트는 제외.
3. **1024-49151 (registered)**
4. **49152-65535 (ephemeral)** — 실효성이 가장 낮아 마지막.

사용자가 스윕을 며칠 뒤 취소하더라도 "얻을 것은 이미 얻은" 상태가 되도록 하는 배치다.

---

## 5. 청킹

### 5.1 크기

- 청크 하나의 엔드포인트 수 = `IP 수 × 포트 수` ≤ `SCAN_SWEEP_CHUNK_ENDPOINTS`(기본 **4096**).
- 기존 자식 Job 단위 가드 `MaxEndpoints = 65536`은 **그대로 유지한다.** 청크 상한이 훨씬 작으므로 실질 충돌은 없고, 스윕을 거치지 않는 단발 스캔의 방어선으로 계속 필요하다.

### 5.2 재생 가능성 (멱등)

`sweep_seq` 하나만 있으면 그 청크의 엔드포인트 집합을 **결정론적으로 재계산**할 수 있어야 한다. 즉 (타깃 목록, 포트 스펙, 청크 크기, seq) → 엔드포인트 집합이 순수 함수다. 이 성질 덕분에

- 재시도 시 같은 집합을 다시 만들 수 있고,
- 청크 엔드포인트 목록을 DB에 물리 저장할 필요가 없으며,
- 중복 발주가 나도 결과가 같다.

### 5.3 지연 생성과 중복 발주 차단

청크에 해당하는 `scan_jobs` 행은 **발주 직전에 생성한다.** 스윕 생성 시점에 6만 건을 미리 만들지 않는다(대기열 오염, 취소 시 정리 비용, 리퍼와의 상호작용 때문).

동시에 도는 발주자가 둘이어도 같은 seq가 두 번 나가지 않도록 부분 유니크 인덱스를 건다.

```sql
CREATE UNIQUE INDEX uq_scan_jobs_sweep_seq
  ON scan_jobs (sweep_id, sweep_seq)
  WHERE sweep_id IS NOT NULL AND status <> 'failed';
```

`status <> 'failed'`를 조건에 넣은 이유: **실패한 청크는 같은 seq로 다시 발주해야 한다.** 실패 행을 유니크 대상에서 빼면 재시도가 자연스럽게 허용된다. 재시도 상한은 **2회**이며, 초과하면 스윕을 `failed`로 전이하고 마지막 오류를 스윕에 기록한다.

### 5.4 스윕 상한 (ETA 거부)

생성 시점에 아래를 계산한다.

```
total_endpoints = |타깃 IP| × |포트|
ETA(일)        = total_endpoints ÷ rate_pps ÷ 86400
```

`ETA > SCAN_SWEEP_MAX_DAYS`(기본 **30**)이면 **422**로 거부한다. 응답 메시지에 실제 소요 일수를 넣는다.

> `이 스윕은 약 187일이 걸립니다. 대상 범위를 줄이거나 포트 스펙을 좁혀 주세요. (제한: 30일)`

숫자를 보여주지 않고 거부하면 사용자는 얼마나 줄여야 하는지 알 수 없다.

---

## 6. 진행률과 중단·재개

### 6.1 진행률은 파생값이다

스윕 행에 진행 카운터를 두지 **않는다.** 자식 `scan_jobs`를 매번 집계해서 파생한다.

```
완료 청크 수 / total_chunks
완료 엔드포인트 = Σ(자식 job의 처리 엔드포인트 수)
발견 인증서   = Σ(자식 job의 발견 수)
```

이유는 이미 데인 곳이기 때문이다 — 하트비트와 관측 배치가 같은 카운터를 각각 증가시켜 `total_endpoints`를 넘겨버린 사고(GOTCHAS "Sensor job progress counters overshoot")가 있었다. 카운터를 하나 더 만들면 이중 계상의 3번째 작성자가 생긴다.

따라서 **스윕의 가변 필드는 `status`, `next_seq`, `total_chunks` 셋뿐이다.** 나머지는 전부 읽기 시점 집계다.

### 6.2 중단·재개의 의미

- `pause` = **신규 청크 발주만 멈춘다.** 이미 발주된 청크는 완주한다. 센서가 리스한 작업을 중간에 죽이지 않는다.
- `resume` = `next_seq`부터 발주 재개.
- `cancel` = 발주 영구 중단. 진행 중 청크는 완주(결과는 정상 반영).

즉 pause/resume/cancel은 전부 **발주자에 대한 제어**이고 실행 중인 Job에는 관여하지 않는다. 이 단순화 덕분에 센서 프로토콜 변경이 필요 없다.

### 6.3 기존 리퍼와의 관계

`ReapOrphanedServerScanJobs`는 `scan_jobs`를 대상으로 돈다. 스윕은 `scan_jobs`가 아니므로 **회수 대상이 아니고 충돌하지 않는다.** 자식 청크 Job은 기존 규칙 그대로 회수된다(회수되면 `failed`가 되고, 5.3의 재시도 경로를 탄다).

### 6.4 센서 대기 표시

센서 지정 스윕에서 자식 Job이 오래 `pending`에 머무르면 스윕 행에 `stalled_since`를 기록한다. UI는 이것을 근거로 "센서 대기" 상태를 표시한다. 이미 `/discovery`에 도입한 대기 배지(경과 시간 + 앰버 경고)와 같은 표현을 쓴다.

---

## 7. 레이트 제한

### 7.1 구조

신규 파일 `backend/internal/discovery/limiter.go`. `golang.org/x/time/rate`를 쓴다(표준 준칙 라이브러리, 자체 구현 금지 규칙과 무관한 영역).

- dial **직전**에 `limiter.Wait(ctx)`를 호출한다. 핸드셰이크 뒤가 아니라 앞이다 — IPS가 보는 것은 SYN 비율이기 때문이다.
- **`ScanEndpoint`의 시그니처는 바꾸지 않는다.** 호출자(워커 루프)가 감싼다. 이 원칙 덕분에 센서·서버 양쪽 실행 경로에 같은 함수를 계속 공유할 수 있다.

### 7.2 호스트 라운드로빈 (핵심)

엔드포인트 실행 순서를 **호스트 기준 라운드로빈으로 섞는다.**

```
나쁨 : 10.0.0.1:1 10.0.0.1:2 10.0.0.1:3 ...   (한 호스트에 연속 타격 = 포트스캔 시그니처)
좋음 : 10.0.0.1:1 10.0.0.2:1 10.0.0.3:1 ...   (호스트당 초당 몇 건)
```

이것이 IPS 오탐 억제의 실질적 핵심이다. 전체 pps가 낮아도 한 호스트에 연속으로 꽂으면 포트스캔으로 탐지된다. 반대로 라운드로빈이면 호스트당 접속률이 pps ÷ 호스트 수로 떨어진다.

### 7.3 기본값

| 항목 | 단발 스캔 | 스윕 |
| --- | --- | --- |
| 레이트 | 50 pps | 10 pps |
| 동시성 | 20 | 8 |
| 청크 간 공백 | — | 5초 |

- 상한 클램프: **1000 pps**. 그 이상은 값을 받아도 1000으로 깎고 경고 로그를 남긴다.
- 스윕이 단발보다 느린 것은 의도다. 스윕은 며칠 도는 배경 작업이므로 눈에 띄지 않아야 한다.

---

## 8. 설정

### 8.1 신규 환경변수 5개

| 변수 | 기본값 | 의미 |
| --- | --- | --- |
| `SCAN_SWEEP_ENABLED` | `true` | 스윕 기능 자체의 on/off |
| `SCAN_SWEEP_RATE_PPS` | `10` | 스윕 기본 레이트 |
| `SCAN_SWEEP_CONCURRENCY` | `8` | 스윕 워커 동시성 |
| `SCAN_SWEEP_CHUNK_ENDPOINTS` | `4096` | 청크 최대 엔드포인트 수 |
| `SCAN_SWEEP_CHUNK_GAP` | `5s` | 청크 발주 간 공백 |

`SCAN_SWEEP_MAX_DAYS`(기본 30)는 5.4의 거부 임계값이며 같은 규칙을 따른다.

### 8.2 값 오류 처리

**값이 잘못되어도 코어 API를 기동 불가로 만들지 않는다.** 경고 로그를 남기고 기본값으로 폴백한다.

선례가 있다: 센서 게이트웨이 설정 실패가 8080까지 죽이던 문제를 이미 고쳐서, 지금은 게이트웨이만 비활성화된다. 같은 원칙을 적용한다. 스캔 튜닝 값 하나를 오타 냈다고 인벤토리 조회까지 막히는 것은 과한 대가다.

경고는 원인별로 변수명을 지목한다(`SCAN_SWEEP_RATE_PPS="fast" is not an integer; falling back to 10`).

---

## 9. 스키마와 API

### 9.1 마이그레이션 `0013_port_autoscan`

번호 주의: **0012는 알림 기능이 선점한다**(`docs/design/05-notifications.md`). 이 기능은 **0013**이다. 기존 규칙대로 번호는 max+1이며 빈 번호(0003·0005)를 메우지 않는다.

포함 내용:

1. `scan_sweeps` 테이블 신설
   - `id`, `name`, `status`, `targets`(스캔과 동일 형식), `port_spec text`, `rate_pps int`
   - `executor_scope`(server / sensor:uuid), `total_endpoints bigint`, `total_chunks int`, `next_seq int`
   - `stalled_since timestamptz`, `last_error text`, `created_by`, `created_at`, `updated_at`
2. `scan_jobs`에 컬럼 4개 추가 — `sweep_id uuid NULL REFERENCES scan_sweeps(id)`, `sweep_seq int NULL`, `rate_pps int NULL`, `port_spec text NULL`
3. 부분 유니크 인덱스 `uq_scan_jobs_sweep_seq` (5.3)
4. 조회용 인덱스 `(sweep_id, sweep_seq)`

**기존 `ON CONFLICT` 아비터는 건드리지 않는다.** 0010에서 `uq_targets_network_ip`를 갈아끼웠을 때 아비터 불일치로 42P10을 맞은 전례가 있다(GOTCHAS). 이번 마이그레이션은 **추가만** 하고 기존 유니크 인덱스의 컬럼 구성을 바꾸지 않는다.

### 9.2 API

| 메서드 | 경로 | 권한 | 비고 |
| --- | --- | --- | --- |
| POST | `/discovery/scans` | 기존과 동일 | `port_spec` 옵션 **추가**(하위호환) |
| POST | `/discovery/sweeps` | 3역할 | 202 + `{sweep_id, total_endpoints, total_chunks, estimated_finish_at}` |
| GET | `/discovery/sweeps` | 전 역할 | 목록 |
| GET | `/discovery/sweeps/{id}` | 전 역할 | 진행률 파생 포함 |
| POST | `/discovery/sweeps/{id}/pause` | 3역할 | 감사 `discovery.sweep` |
| POST | `/discovery/sweeps/{id}/resume` | 3역할 | 감사 `discovery.sweep` |
| POST | `/discovery/sweeps/{id}/cancel` | 3역할 | 감사 `discovery.sweep` |
| GET | `/discovery/port-presets` | 전 역할 | 프리셋 이름·포트·설명 |

"3역할" = 스캔을 만들 수 있는 기존 역할 집합과 동일하게 맞춘다(별도 역할을 새로 만들지 않는다).

포트 결정 우선순위 (하위호환):

```
port_spec 지정 → 그것을 파싱
아니면 ports 지정 → 그것을 사용 (기존 호출자 그대로 동작)
아니면 → common
```

라우트 등록과 `backend/api/openapi.yaml` 수정은 **같은 커밋**이어야 한다. `TestOpenAPISpecMatchesRoutes`가 파싱 단계에서 실패하고, `security:` 블록 아래에 path를 붙이면 조용히 깨진다(GOTCHAS).

### 9.3 프론트엔드

- 포트 입력을 **자유 텍스트 `port_spec` 하나**로 바꾼다.
- 클라이언트의 `split(",") → parseInt` 전처리를 **삭제한다.** 이건 정리가 아니라 **버그 수정**이다: 현재 `8443-9443`을 입력하면 `parseInt("8443-9443")`가 `8443`을 반환해 범위가 조용히 잘려나간다. 사용자는 1001개 포트를 요청했다고 믿지만 1개만 스캔된다.
- 검증은 서버 400 응답으로 일원화한다. 클라이언트에 두 번째 파서를 두지 않는다(두 파서는 반드시 갈라진다).
- 프리셋 칩(`web` / `common` / `all`) — 누르면 텍스트 입력에 삽입.
- "전 포트 백그라운드 스윕" 토글 — 켜면 `/discovery/sweeps`로 발사.
- 스윕 카드: 진행률 바, ETA, 웨이브 표시, 일시정지/재개/취소 버튼(권한 게이트로 3역할에만 노출).

### 9.4 센서 배선

- `sensorwire.LeaseResponse`에 **`rate_pps int` 필드 1개만** 가산한다.
- 구버전 센서는 모르는 필드를 무시하므로 **전속력으로 돈다.** 이 스큐를 문서(`docs/manual/SENSOR.md`)에 명시한다 — "레이트 제한을 적용하려면 센서도 함께 업데이트해야 한다."
- 포트 범위 문법은 센서까지 내려가지 않는다. 서버가 정수 목록으로 전개한 뒤 리스에 싣는다(2.2).

---

## 10. 병렬 구현 단위

| 단위 | 범위 | 주요 파일 | 의존 |
| --- | --- | --- | --- |
| **U1** | 포트 문법 파서 + 프리셋 | `discovery/ports.go`, `ports_test.go` | 없음 |
| **U2** | 레이트 리미터 + 호스트 라운드로빈 | `discovery/limiter.go`, `limiter_test.go` | 없음 |
| **U3** | 마이그레이션 0013 | `migrations/0013_*.up/down.sql` | 없음 |
| **U4** | 스윕 저장소 | `store/sweeps.go` | U3 |
| **U5** | 스윕 엔진(청킹·발주·상태 전이) | `discovery/sweep.go`, `discovery/manager.go` | U1, U2, U4 |
| **U6** | API 핸들러 + openapi.yaml | `api/handlers_discovery.go`, `api/server.go`, `api/openapi.yaml` | U5 |
| **U7** | 센서 배선(`rate_pps`) | `sensorwire/`, `cmd/sensor/` | U2 |
| **U8** | 설정 + 배선 | `config`, `cmd/server/main.go`, `docker-compose.yml` | U5 |
| **U9** | 프론트엔드 | `frontend/app/(app)/discovery/`, `frontend/lib/api.ts` | U6 |
| **U10** | 문서 | `docs/manual/INSTALL.md`·`OPERATIONS.md`·`SENSOR.md`, `docs/MAP.md` | U6, U8 |

권장 머지 순서:

```
(U1 ∥ U2 ∥ U3) → U4 → U5 → U6 → (U7 ∥ U8) → U9 → U10
```

공유 파일 주의: `api/server.go`와 `api/openapi.yaml`은 U6·U10이 함께 만진다. 라우트와 명세는 **반드시 같은 커밋**이어야 `make openapi-check`를 통과한다.

---

## 11. 리스크와 기각한 대안

### 11.1 리스크

| 리스크 | 대응 |
| --- | --- |
| **/20 전수 스윕은 10pps로 비현실적** — 4094 IP × 65535 = 2.68억 엔드포인트 ≈ 310년 | 5.4의 ETA 계산으로 **422 거부**. 실제 소요 일수를 메시지에 넣어 얼마나 줄여야 할지 알린다 |
| **감사 볼륨 증가** — 청크당 감사 2건(발주·완료) | 청크가 분 단위로 나가므로 감사 삽입률은 분당 수 건. 감사 체인 advisory lock에 유의미한 부하가 아니다. 무시 가능 |
| IPS 오탐으로 스캐너 IP 차단 | 7.2 호스트 라운드로빈 + 낮은 기본 pps + 청크 간 공백. 그래도 발생하면 운영자가 `SCAN_SWEEP_RATE_PPS`를 낮춘다 |
| 스윕이 며칠간 살아 있는 동안 대상 네트워크 변경 | 청크는 seq→엔드포인트 순수 함수(5.2)이므로 스윕 생성 시점 정의를 끝까지 유지한다. 변경 반영은 새 스윕으로 |

### 11.2 기각한 대안

**(1) 한 Job을 2 phase로 나눈다** — `scan_jobs` 하나가 프리셋 단계와 전수 단계를 순차 실행.

기각 사유: `phase1_done` 같은 상태를 표현하려면 **job 상태 enum을 확장해야 하고**, 그 값이 프론트 유니온 타입에 없으면 폴링 타깃 필터에서 빠져 UI가 "실패"로 얼어붙는다. 이미 겪은 사고다(GOTCHAS "Contract drift"). 부모 리소스를 따로 두면 기존 enum을 한 글자도 건드리지 않는다.

**(2) 청크 행을 DB에 남기지 않는다** — 메모리에서 발주하고 결과만 인벤토리에 반영.

기각 사유: 서버 재시작 시 어디까지 했는지 알 수 없고, **어떤 범위를 언제 스캔했는지 감사할 수 없다.** 인증서 제품에서 "우리가 이 대역을 언제 훑었나"에 답하지 못하는 것은 치명적이다. 청크를 `scan_jobs` 행으로 남기면 기존 감사·회수·조회 인프라를 그대로 물려받는다.
