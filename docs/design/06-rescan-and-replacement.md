# 엔드포인트 주기 재스캔과 교체 감지 설계

상태: **설계 확정 · 구현 미착수**
Phase 배정: **Phase 1 범위 안.** 근거 명세: `docs/00-spec.md` 7.1 네트워크 TLS 스캔 — "스캔 주기"(246행), 수집 항목 "최초 발견 시간 / 최근 확인 시간"(266-267행), 인벤토리 항목 "최근 스캔 시간"(308행), 15장 Phase 1 목록 "네트워크 TLS 스캔"(1334행). `00-spec.md` 16장 제외 목록과 `docs/01-plan.md` "이번 범위에서 제외"(77-92행) 어디에도 없다.

이 문서는 구현 지시서가 아니라 **설계 확정본**이다. 착수 시점이 오면 "12. 병렬 구현 단위"부터 읽는다.

---

## 1. 목적과 범위

### 1.1 착수 근거는 스펙이 아니라 결함이다

스펙에 "스캔 주기"가 있긴 하지만, `docs/02-goals.md`의 Phase 1 완료 조건 **필수 항목은 아니다.** 그런데도 지금 해야 하는 이유는 따로 있다.

만료 알림(Phase 4 항목)을 지시로 앞당겨 출하한 결과, **교체 감지 부재가 "출하된 기능의 오탐"이 됐다.** 운영자가 인증서를 갱신해 서버에 새로 깔아도 제품은 그 사실을 모른다. 옛 인증서는 인벤토리에 `active`로 남아 있고, 만료일이 다가오면 D-30·D-7 알림이 계속 나간다. 이미 처리한 건에 대한 알림은 알림 전체의 신뢰를 깎는다 — 몇 번 겪으면 진짜 알림도 무시된다.

즉 이 기능은 "인벤토리를 최신으로 유지한다"이면서 동시에 **"이미 출하한 알림의 정확도 복구"** 다.

### 1.2 두 축

| 축 | 하는 일 | 산출 |
| --- | --- | --- |
| **주기 재스캔** | 이미 아는 엔드포인트를 주기적으로 다시 확인한다 | 신선도(`last_verified_at`)와 관측 지문 |
| **교체 감지** | 같은 엔드포인트의 인증서 지문이 바뀐 것을 확인하고 옛 인증서를 `replaced`로 전이 | 죽은 인증서의 알림·집계 제외 |

### 1.3 이번 범위에서 제외

**대역 재스캔(신규 엔드포인트 발견)은 다루지 않는다.** 이 기능은 **이미 아는 엔드포인트의 재확인**만 한다.

신규 발견은 이미 담당자가 있다 — 수동 스캔(`POST /discovery/scans`)과 전 포트 백그라운드 스윕(`docs/design/04-port-autoscan.md`)이다. 여기에 대역 재스캔을 얹으면 세 개의 발주자가 같은 대역을 각자의 주기로 때리게 되고, 레이트 예산이 서로를 모르는 채 겹친다. 신선도 유지와 신규 발견은 목적도 주기도 다르므로 분리한다.

"이 위치 다시 확인" 같은 사용자 트리거 단건 재확인도 이번 범위 밖이다(11.4).

---

## 2. 재스캔 대상 모델

### 2.1 신규 테이블 `endpoint_monitors`

재스캔의 단위는 **엔드포인트**이고, 키는 `(target_id, port)`다.

스코프(`server` / `sensor:<uuid>`)를 이 테이블에 중복 저장하지 않는다. `targets.executor_scope`(0010, 30행)가 이미 세그먼트를 가르고 있고, `targets`는 `uq_targets_network_scope_ip`로 스코프별 IP당 1행이므로 `target_id` 하나가 스코프를 함의한다. 같은 사실을 두 곳에 적으면 반드시 갈라진다.

| 컬럼 | 타입 | 의미 |
| --- | --- | --- |
| `id` | uuid PK | |
| `target_id` | uuid NOT NULL REFERENCES targets(id) ON DELETE CASCADE | |
| `port` | integer NOT NULL | |
| `first_seen_at` | timestamptz NOT NULL | 이 엔드포인트를 처음 안 시각 |
| `last_probe_at` | timestamptz NULL | **프로브를 시도한** 시각(성공 여부 무관) |
| `last_success_at` | timestamptz NULL | 마지막으로 인증서를 얻은 시각 |
| `current_fingerprint` | text NULL | 마지막으로 관측된 리프 지문 |
| `current_streak` | integer NOT NULL DEFAULT 0 | 현재 지문을 연속 관측한 횟수 |
| `previous_fingerprint` | text NULL | 직전 지문(교체 전이의 "옛 인증서") |
| `consecutive_misses` | integer NOT NULL DEFAULT 0 | 연속 미관측 횟수 |
| `enabled` | boolean NOT NULL DEFAULT true | |
| `interval_seconds` | integer NULL | 대상별 override(3.3) |
| `retired_at` | timestamptz NULL | 은퇴 시각. NULL이 아니면 발주 대상 제외 |

행은 `certificate_locations`의 `location_type = 'network_endpoint'` 위치에서 파생하며, **최초 행 생성은 위치 생성과 같은 트랜잭션**이다(`store/discovery.go`의 네트워크 위치 upsert 경로, 457-465행).

### 2.2 왜 `certificate_locations`를 그대로 쓰지 않는가

재사용 제안이 나올 자리이므로 근거를 남긴다. 이유는 둘이고, 둘 다 치명적이다.

**① 위치 테이블의 키는 엔드포인트가 아니다.**
`uq_certificate_locations_network_endpoint`(0006)는 `(certificate_id, target_id, port)`다. 인증서가 키에 들어 있으므로 **교체 직후에는 같은 엔드포인트에 2행이 존재한다**(옛 인증서 1행 + 새 인증서 1행). "이 엔드포인트를 언제 다시 볼 것인가"를 물으면 답이 두 개다. 재스캔의 단위는 인증서가 아니라 엔드포인트이므로 키가 맞지 않는다.

**② "프로브했는데 못 얻었다"를 기록할 자리가 없다.**
`last_verified_at`은 **인증서를 얻었을 때만** 갱신된다(`store/discovery.go:462`의 `DO UPDATE SET last_verified_at = now()` — 이 문장은 체인을 얻은 경로에서만 실행된다). 도달 실패는 위치 테이블에 아무 흔적도 남기지 않는다. 그래서 "연속 N회 못 봤다"를 셀 수 없고, 은퇴 판정(3.4)도 교체 판정의 부정 근거(8.2)도 만들 수 없다.

`endpoint_monitors`는 `last_probe_at`(시도)과 `last_success_at`(성공)을 분리해 이 구멍을 메운다.

### 2.3 `next_due_at`을 두지 않는다

다음 실행 예정 시각을 컬럼으로 들고 있지 않는다. **due는 매번 쿼리로 파생한다** — `last_probe_at + 유효 간격 <= now()`.

04 설계의 "진행률은 파생값이다"(6.1)와 같은 판단이다. 저장된 예정 시각은 티어가 바뀌는 순간(잔여 31일 → 30일) 갱신 주체가 필요해지고, 그 갱신을 한 곳이라도 빠뜨리면 인증서 하나가 조용히 24시간 주기에 남는다 — 하필 가속이 가장 필요한 시점에. 파생값에는 갱신 누락이라는 실패 모드 자체가 없다.

### 2.4 monitor는 폭증하지 않는다

전 포트 스윕이 65535 포트를 훑어도 monitor 행은 **인증서를 실제로 얻은 엔드포인트에만** 생긴다. 열려 있지만 TLS가 아닌 포트, 닫힌 포트는 위치를 만들지 않으므로 monitor도 만들지 않는다. 스윕 규모와 monitor 규모는 연동되지 않는다.

---

## 3. 주기 정책

### 3.1 전역 기본 + 만료 임박 가속 2티어

| 조건 | 간격 |
| --- | --- |
| 기본 | `RESCAN_INTERVAL` (기본 **24h**) |
| 인증서 잔여 **30일 이하** | **6h** |
| 인증서 잔여 **7일 이하** | **1h** |

가속의 근거: **갱신은 만료 직전에 일어난다.** 운영자가 D-3에 새 인증서를 깔았는데 재스캔이 24시간 주기면 최대 하루 동안 옛 인증서가 `active`로 남아 있고, 그 사이에 D-1 알림이 나간다. 오탐을 없애려는 기능이 오탐 창을 만드는 셈이다. 정작 자주 봐야 할 구간에서 자주 보는 것이 이 기능의 값어치 대부분이다.

반대로 잔여 300일짜리를 6시간마다 볼 이유는 없다. 그래서 가속은 임박 구간에만 건다.

### 3.2 티어 판정 기준

티어는 해당 엔드포인트에 붙어 있는 인증서의 `not_after`로 판정한다. **저장된 `status`가 아니라 `not_after`다** — 05 설계 7.4와 같은 이유로, `status`를 만료로 갱신하는 주체가 아직 없어 stale이다.

한 엔드포인트가 여러 인증서 위치를 가지면(교체 직후) **가장 임박한 것**을 기준으로 한다. 빨리 보는 쪽이 안전한 실패다.

### 3.3 대상별 override는 자리만 둔다

`interval_seconds` 컬럼을 두되 **이번 범위에 UI를 만들지 않는다.** 설정 표면을 늘리지 않으면서 확장점만 남긴다(05 설계 1.2와 같은 처리). NULL이면 3.1의 티어 규칙을 따른다.

### 3.4 은퇴

`consecutive_misses >= RESCAN_RETIRE_MISSES`(기본 **30**)이면 `retired_at`을 찍고 발주 대상에서 제외한다.

기본 간격 24h에서 30회는 약 한 달이다. 서버가 철거됐거나 방화벽 정책이 바뀐 엔드포인트를 영원히 두드리지 않되, 일시 장애·정기 점검으로 몇 주 빠진 것은 살려둔다. **행을 지우지 않는 이유**는 `first_seen_at`이 "우리가 이 엔드포인트를 언제부터 알았나"의 유일한 기록이기 때문이다. 은퇴는 조회 가능한 상태이지 삭제가 아니다.

은퇴 엔드포인트가 다시 관측되면(수동 스캔·스윕 경로로) `retired_at`을 NULL로 되돌리고 `consecutive_misses`를 0으로 리셋한다.

---

## 4. 발주 — 기존 `scan_jobs` 재사용

### 4.1 포트별 그룹 발주

재스캔 배치 1건 = `targets = [due 상태인 IP들]`, `ports = [단일 포트]`인 **`scan_jobs` 1행**이다.

이 형태가 강제되는 이유는 `scan_jobs`의 스키마다. `targets text[]` × `ports integer[]`는 **데카르트 곱**으로 전개된다(0006, 26-27행). 엔드포인트 쌍 `(10.0.0.1:443), (10.0.0.2:8443)`을 그대로 넣으면 `2 × 2 = 4`개가 나오고, 요청하지 않은 `10.0.0.1:8443`·`10.0.0.2:443`까지 스캔된다. 이건 낭비가 아니라 **범위 위반**이다 — 감사 로그에 "우리가 스캔한 범위"가 실제와 다르게 남는다.

포트를 1개로 고정하면 곱이 `|IP| × 1`이라 **정확히 due 쌍만** 나온다. 현실 데이터는 대부분 443이므로 job 수도 몇 개에 그친다.

배치 상한은 `RESCAN_BATCH_IPS`(기본 **1024**)이고, 초과분은 다음 tick으로 넘긴다.

### 4.2 기존 계약은 한 글자도 건드리지 않는다

- **job 상태 enum 불변** — `pending` / `leased` / `running` / `completed` / `failed`. 04 설계 4.2와 같은 판단이며, 값 하나만 늘려도 프론트 폴링 타깃 필터에서 빠져 UI가 "실패"로 얼어붙는다(GOTCHAS "Contract drift").
- **기존 유니크 인덱스 구성 불변, `ON CONFLICT` 아비터 불변** — 0010에서 `uq_targets_network_ip`를 갈아끼웠을 때 아비터 불일치로 42P10을 맞은 전례가 있다(GOTCHAS).
- `job_type` CHECK도 `'scan'` 그대로다. 재스캔은 **새 job 유형이 아니라 같은 스캔의 다른 발주자**다.

### 4.3 출처 구분 — `origin` 컬럼 1개

```sql
ALTER TABLE scan_jobs
  ADD COLUMN origin text NOT NULL DEFAULT 'manual'
    CHECK (origin IN ('manual', 'sweep', 'rescan'));
UPDATE scan_jobs SET origin = 'sweep' WHERE sweep_id IS NOT NULL;
```

"이 job은 왜 돌았나"에 답하지 못하면 감사와 운영 진단이 성립하지 않는다. 기존 행은 `sweep_id IS NOT NULL`로 정확히 백필된다.

**프론트 영향은 0이다.** 폴링은 `status`만 본다 — 추가된 컬럼은 응답 타입에 새 필드가 하나 늘 뿐이고 유니온 타입을 건드리지 않는다.

---

## 5. 센서 위임

### 5.1 그룹핑 키는 `(executor_scope, port)`

due 엔드포인트를 스코프별로 먼저 가르고, 그 안에서 포트별로 묶는다. `targets.executor_scope`가 `sensor:<uuid>` 형태면 그 job은

```
executor_type = 'sensor'
sensor_id     = <uuid>
status        = 'pending'
```

으로 만든다. 나머지는 센서가 기존 리스 경로로 pull해 간다.

**센서 프로토콜 변경은 0이다.** 센서 입장에서 재스캔 job은 다른 스캔 job과 구별되지 않는다 — 그게 목적이다.

### 5.2 비활성 센서 스코프는 건너뛴다

센서가 오프라인이면 그 스코프의 job은 `pending`으로 쌓이기만 한다. 그래서 **발주 시점에 건너뛴다.**

단, 이를 위해 monitor에 신규 상태 컬럼을 만들지 않는다. "센서가 죽어서 못 봤다"는 신선도 경고(11.1)가 이미 사용자에게 보여준다 — `last_verified_at`이 낡으면 화면이 앰버·레드로 알린다. 같은 사실을 두 번째 자리에 적으면 그 둘이 갈라진다.

---

## 6. 레이트 제한 — 추가 작업 없음

재스캔 job은 서버 실행 경로에서 기존 `Manager.run`을 그대로 탄다. 따라서 `InterleaveByHost`(호스트 라운드로빈)와 `RateLimiter.Wait`(dial 직전)이 **자동으로 적용된다**(`discovery/manager.go:126-141`). 04에서 만든 것을 그대로 물려받는다.

| 항목 | 단발 스캔 | 스윕 | **재스캔** |
| --- | --- | --- | --- |
| 레이트 | 50 pps | 10 pps | **20 pps** (`RESCAN_RATE_PPS`) |
| 동시성 | 20 | 8 | **8** |

재스캔이 단발과 스윕 사이인 것은 의도다. 배경 작업이지만 스윕처럼 며칠짜리는 아니고, 신선도에 시간 예산이 걸려 있다.

**포트별 그룹핑이 IPS 회피와 자연히 맞물린다.** 재스캔 job 하나는 "같은 포트, 여러 IP"이므로 라운드로빈 없이도 연속 프로브가 서로 다른 호스트로 간다. 04에서 구조적으로 방어한 것을 이 발주 형태가 한 번 더 보장한다.

---

## 7. 중복 발주와 고아 회수

### 7.1 tick 단일화

재스캔 tick은 **단일 트랜잭션**으로 실행하고, 시작 직후 `pg_try_advisory_xact_lock(고정키)`를 시도한다. 실패하면 조용히 skip한다. 05 설계 7.2와 동일한 규약이며, 알림 스케줄러와는 **다른 락 키**를 쓴다.

### 7.2 claim 방식 — 발주 시점에 `last_probe_at`을 미리 찍는다

job을 만드는 그 트랜잭션에서 해당 monitor들의 `last_probe_at = now()`를 함께 갱신한다. due 판정이 `last_probe_at` 기반(2.3)이므로 **같은 엔드포인트가 두 job에 들어갈 수 없다.**

대가를 명시한다: **job이 실패하면 그 엔드포인트는 다음 주기까지 다시 보지 않는다.** 이미 "봤다고" 표시했기 때문이다. 이것은 수용된 결정이다 — 반대 설계(완료 시점에 찍기)는 job이 도는 동안 같은 엔드포인트가 계속 due로 보여 중복 발주를 낳고, 그걸 막으려면 결국 in-flight 상태 컬럼을 하나 더 만들어야 한다. 24시간 뒤 재시도로 충분한 문제에 상태 머신을 하나 더 얹지 않는다.

### 7.3 고아 회수는 추가 코드 0

`ReapOrphanedServerScanJobs`(`store/discovery.go:266`)는 `executor_type = 'server'`인 `pending`/`running` job을 **전부** 잡는다. 재스캔 job도 `scan_jobs` 행이므로 그대로 회수된다. 새 리퍼를 만들지 않는다.

---

## 8. 교체 감지

### 8.1 관측 시점에 하는 일 — 카운팅뿐

교체 판정을 관측 경로에서 내리지 않는다. 관측 시점에는 monitor의 카운터만 갱신한다.

훅을 놓는 자리는 `applyEndpointObservation`(정의: `store/discovery.go:190`)이고, 호출자는 정확히 둘이다 — `store/discovery.go:173`(서버 직접 스캔)과 `store/sensorjobs.go:275`(센서 관측 배치). 두 경로가 이 함수 하나로 합류하므로 훅도 하나면 된다.

**`if !obs.Reachable { return }` 조기 반환(191-193행) 앞에** 훅을 놓아야 한다. 뒤에 놓으면 미관측이 기록되지 않아 `consecutive_misses`가 영원히 0이다.

| 관측 결과 | monitor 갱신 |
| --- | --- |
| 지문이 `current_fingerprint`와 같다 | `current_streak++`, `last_success_at = now()`, `consecutive_misses = 0` |
| 지문이 다르다 | `previous_fingerprint ← current_fingerprint`, `current_fingerprint ← 새 지문`, `current_streak = 1`, `consecutive_misses = 0` |
| 도달 실패 | `consecutive_misses++` **만** 한다 |

**도달 실패는 교체 근거가 아니다.** "안 보인다"와 "다른 것으로 바뀌었다"는 다른 사실이다. 방화벽 변경·서비스 재시작·센서 장애 어느 것으로도 도달 실패는 난다. 미관측만으로 인증서를 `replaced` 처리하면 네트워크 사고 한 번이 인벤토리 대량 오염이 된다.

### 8.2 전이 3중 게이트 — 조건 전부 AND

옛 인증서를 `replaced`로 전이하려면 **넷을 모두** 만족해야 한다.

**① `current_streak >= RESCAN_REPLACE_CONFIRM`(기본 2)**
LB 뒤에 서로 다른 인증서를 든 백엔드가 여러 대 있으면 관측 지문이 A → B → A → B로 번갈아 나온다. 1회 관측으로 전이하면 **A와 B가 서로를 무한히 `replaced`로 만든다** — 매 tick마다 감사 이벤트와 상태 전이가 발생하는 진동 상태다. 연속 2회를 요구하면 진짜 번갈이는 어느 쪽도 전이하지 못하고 멈춘다(16.1).

**② 옛 인증서의 모든 `network_endpoint` 위치가 `removed`**
다른 엔드포인트에서 아직 서비스 중인 인증서는 교체된 것이 아니다. 와일드카드 인증서가 서버 5대에 깔려 있고 그중 1대만 갱신했다면 나머지 4대는 여전히 옛 인증서를 쓴다.

**③ 옛 인증서가 `file`/`keystore`/`windows_store` 위치를 하나도 갖지 않는다**
8.3에서 따로 다룬다.

**④ 현재 `status`가 `active` 또는 `unknown`**
멱등성 가드다. 이미 `replaced`·`revoked`·`expired`인 것을 다시 전이하지 않는다. tick이 두 번 돌아도 결과가 같다.

### 8.3 수동 업로드 보호 (게이트 ③)

`POST /certificates/import`는 **항상** `location_type = 'file'` 위치 행을 만든다(`store/certificates.go:132-137`). 즉 "파일 위치를 가진 인증서"는 사실상 "사람이 손으로 올린 인증서"다.

그런 인증서는 위치만 `removed`로 하고 **상태는 그대로 둔다.**

근거: **업로드는 관측이 아니라 선언이다.** 사용자가 "이 인증서는 우리 자산이다"라고 등록한 것이고, "네트워크 어디에서도 안 보인다"가 "폐기됐다"를 뜻하지 않는다 — **아직 배포 전일 수 있다.** 갱신본을 미리 등록해 두고 배포는 다음 점검 창에 하는 것은 정상적인 운영 흐름이다. 이 인증서에는 만료 알림이 계속 가는 것이 옳다. 알림을 끊으면 배포를 잊은 채 만료된다.

### 8.4 CA 인증서는 자연 배제

위치(`certificate_locations`)는 리프에만 붙는다. 체인 상위 CA는 `network_endpoint` 위치를 갖지 않으므로 게이트 ②의 "모든 위치가 removed" 조건이 성립할 대상 자체가 없다. **별도 예외 코드가 필요 없다.** 이 사실을 코드가 아니라 문서에 남긴다.

### 8.5 전이 실행 지점은 주기 tick이다

job 완료 훅이 아니다.

| 후보 | 문제 |
| --- | --- |
| job 완료 훅 | 서버 실행 경로와 sensorgw 결과 수집 경로 **두 곳**을 건드려야 한다. 완료 직후 프로세스가 죽으면 전이가 통째로 유실되고 되살릴 트리거가 없다 |
| **주기 tick** | 한 곳. 조건은 DB 상태만 보므로 **멱등**이고, 재기동해도 다음 tick이 같은 판정을 다시 내린다 |

tick은 "지금 조건을 만족하는 것 전부"를 찾는 세트 기반 질의다. 놓친 전이는 다음 tick이 줍는다.

---

## 9. 옛 위치와 알림 연동

### 9.1 옛 `location`은 삭제하지 않는다

`deployment_status = 'removed'`로 전이한다. **0002의 CHECK에 `removed`가 이미 있다**(0002, 95-96행) — enum 변경이 필요 없다.

`last_verified_at`은 **갱신하지 않는다.** 그 값은 "마지막으로 실제 관측한 시각"이고, 제거를 판정한 시각이 아니다. 덮어쓰면 "이 인증서가 언제까지 실제로 서비스 중이었나"라는 사고 조사용 사실이 사라진다.

`certificates`에 교체 링크를 추가한다.

```sql
ALTER TABLE certificates
  ADD COLUMN replaced_by_certificate_id uuid NULL REFERENCES certificates(id) ON DELETE SET NULL,
  ADD COLUMN replaced_at timestamptz NULL;
```

`ON DELETE SET NULL`인 이유: 새 인증서가 나중에 지워져도 옛 인증서의 `replaced` 상태와 이력은 남아야 한다.

### 9.2 만료 알림 제외 — 쓰는 쪽은 이미 다 되어 있다

`c.status NOT IN ('revoked', 'replaced')` 필터가 **이미 3곳에 존재한다.**

| 위치 | 역할 |
| --- | --- |
| `store/notifications.go:678` | `detectExpiryEventsSQL`의 만료 예정(`certificate.expiring`) 탐지 분기 |
| `store/notifications.go:708` | 같은 const의 만료(`certificate.expired`) 탐지 분기 |
| `store/notifications.go:1629` | 보존 purge의 재탐지 예외 판정 |

팬아웃은 탐지의 `RETURNING`을 그대로 받으므로 별도 필터가 필요 없다. **알림 쪽 추가 작업은 0이다** — 이 설계는 `status = 'replaced'`를 **쓰는 쪽**만 만들면 된다.

### 9.3 이미 큐에 있는 딜리버리는 따로 끊는다

전이 시점에 이미 `pending`인 딜리버리는 그대로 발송된다. 05 설계 13장의 at-least-once 성질상 아웃박스에 들어간 것은 탐지 필터가 회수하지 못한다.

그래서 전이 트랜잭션에서, **감사 삽입 직전에** 해당 인증서의 미발송 딜리버리를 종결한다.

```sql
UPDATE notification_deliveries d
   SET status = 'skipped', last_error = 'certificate replaced'
  FROM notification_events e
 WHERE d.event_id = e.id
   AND e.certificate_id = $1
   AND d.status = 'pending';
```

SQL 1문장이다. `skipped`는 05 설계 14.3이 이미 쓰는 "보내지 않은 이유가 있는" 상태이므로 새 상태를 만들지 않는다.

---

## 10. 감사

### 10.1 전이마다 남긴다 (하드 규칙 5)

`certificate.replaced` 이벤트를 전이 1건마다 남긴다.

05 설계가 발송 감사를 생략한 근거(12장 ②)는 **차수 논거**였다 — "인증서 × 임계값 × 채널 × 수신자"만큼의 삽입이 감사 해시 체인의 advisory lock을 점유하면 로그인·업로드 같은 사용자 요청이 지연된다. **교체 전이는 인증서 1건당 생애 1회**다. 차수가 근본적으로 다르므로 그 논거는 여기에 적용되지 않는다. 인벤토리 상태를 바꾸는 변경이고, 하드 규칙 5의 정면 대상이다.

### 10.2 부하 방어 2겹

**① 관측 트랜잭션 안에서 전이하지 않는다.**
전이는 8.5대로 주기 tick의 별도 배치 단계에서 한다. tick당 트랜잭션 1개이고, 감사 N건은 **그 트랜잭션의 마지막 문장들**이다. `ReapOrphanedServerScanJobs`가 이미 쓰는 패턴 그대로다(`store/discovery.go:300-305` — 주석까지 같은 규약을 명시하고 있다). GOTCHAS "Audit insert must be the last statement in its transaction"을 그대로 지킨다.

**② tick당 전이 상한 200건.**
첫 가동 시 누적분이 한꺼번에 잡히는 경우(16.1)를 대비한 클램프다. 남은 건은 다음 tick이 처리한다.

### 10.3 이벤트 형태

| 필드 | 값 |
| --- | --- |
| `event_type` | `certificate.replaced` (기존 `certificate.import`·`certificate.export` 명명 규약과 동일) |
| `actor_id` | **NULL** — 사람이 아니라 시스템 판정이다 |
| `target_type` / `target_id` | `certificate` / 옛 인증서 id |
| `before_value` | `{status, fingerprint}` |
| `after_value` | `{status: 'replaced', replaced_by, evidence: {target_id, port, observed_at, confirmations}}` |

`evidence`가 핵심이다. "왜 이 인증서를 죽었다고 판정했나"에 답하지 못하는 자동 상태 전이는 감사 대상이 아니라 사고다.

**인증서 원문·PEM·DER은 어느 필드에도 넣지 않는다**(하드 규칙 3). 지문은 해시이므로 안전하다.

### 10.4 재스캔 job 자체의 감사

기존 `discovery.scan`(`store/discovery.go:22`)을 그대로 재사용한다. 새 이벤트 타입을 만들지 않고, `after_value`에 `origin: 'rescan'`을 실어 구분한다(4.3).

---

## 11. 스키마와 API, 화면

### 11.1 마이그레이션 `0015_endpoint_rescan`

번호 주의: 이 저장소의 현재 최대 번호는 **0013**이고, **0014는 병행 작업이 선점한다.** 이 기능은 **0015**다. 규칙대로 번호는 max+1이며 빈 번호(0003·0005)를 메우지 않는다.

> 착수 시 확인할 것: `backend/migrations/`에 0014가 실제로 존재하는지 본다. 없으면 그 작업이 취소된 것이므로 max+1 규칙에 따라 번호를 다시 정한다.

포함 내용:

1. `endpoint_monitors` 신설 + `UNIQUE (target_id, port)`
2. `certificates`에 `replaced_by_certificate_id`, `replaced_at` 추가 (9.1)
3. `scan_jobs`에 `origin` 추가 + `sweep_id IS NOT NULL` 백필 (4.3)
4. 기존 `network_endpoint` 위치에서 monitor 시딩 — `first_seen_at`·`last_success_at`·`current_fingerprint`는 기존 위치 값에서 채우고, `current_streak`은 1로 둔다(관측 1회로 간주)
5. 인덱스
   ```sql
   CREATE INDEX idx_endpoint_monitors_due ON endpoint_monitors (last_probe_at)
     WHERE enabled AND retired_at IS NULL;
   CREATE INDEX idx_certificates_replaced_by ON certificates (replaced_by_certificate_id);
   ```

**추가만 한다.** 기존 유니크 인덱스의 컬럼 구성과 `ON CONFLICT` 아비터를 바꾸지 않는다(4.2, 0010 42P10 전례).

### 11.2 API

| 메서드 | 경로 | 권한 | 비고 |
| --- | --- | --- | --- |
| GET | `/discovery/rescan/status` | 전 역할 | 활성 여부·유효 간격·감시 엔드포인트 수·마지막 실행·지연 |
| POST | `/discovery/rescan/run` | 3역할 | 즉시 1배치 발주. 감사 `discovery.scan` |
| GET | `/certificates` | 기존과 동일 | `exclude_status` 다중 값 파라미터 **추가** |

"3역할" = 스캔 발주와 같은 집합(`system_administrator` / `pki_administrator` / `operator`, `api/server.go:104-105`). 새 역할을 만들지 않는다.

**`exclude_status`는 서버 기본 동작을 바꾸지 않는다.** 파라미터가 없으면 현행과 완전히 동일하다. 목록 필터 입력(`ListCertificatesInput`)을 **CSV export가 그대로 공유하므로**(`store/certificates_export.go:27`), 서버 기본값을 바꾸면 아무도 요청하지 않은 CSV 내용 변경이 조용히 따라온다. 기본값 변경은 16.2 ⑥에서 기각한다.

라우트 등록과 `backend/api/openapi.yaml` 수정은 **반드시 같은 커밋**이다. `TestOpenAPISpecMatchesRoutes`가 파싱 단계에서 실패하고, `security:` 블록 아래에 path를 붙이면 조용히 깨진다(GOTCHAS).

### 11.3 화면 — 신선도

**API 추가가 필요 없다.** `last_verified_at`은 이미 인증서 상세 응답에 실려 있다(`store/certificates_query.go:276`, `frontend/lib/api.ts:121`). **렌더만 추가한다.**

- 상대 시각 표기는 `frontend/components/notification-bell.tsx:81`이 export하는 `relativeTime(iso, now)`을 재사용한다. 두 번째 포맷터를 만들지 않는다.
- 경고 임계는 **고정값** — 7일 초과 앰버 / 30일 초과 레드.
  **서버의 재스캔 간격을 프론트로 내리지 않는다.** 재스캔이 꺼진 설치(`RESCAN_ENABLED=false`)에서도 "이 정보가 얼마나 낡았나"는 의미가 통해야 한다. 간격을 기준으로 삼으면 기능이 꺼진 순간 경고가 사라져, 가장 낡은 데이터를 보고 있을 때 아무 표시도 안 나온다.
- 목록에는 인증서 단위 `last_seen_at = max(location.last_verified_at)` **1열**을 추가한다.
  **정렬 화이트리스트(`store/certificates_query.go:27`의 `listSortColumns`)에는 넣지 않는다** — 집계 파생값이라 `ORDER BY <컬럼>` 형태로 안전하게 끼워 넣을 수 없다. 표시 전용이다.

### 11.4 화면 — 교체된 인증서

- 목록: 프론트가 기본으로 `exclude_status=replaced`를 붙인다. "교체된 인증서 포함" 토글(기본 off, URL 쿼리 구동)로 되돌릴 수 있다.
- 상세: `replaced` 배지 + **"교체한 인증서 →"** 링크(`replaced_by_certificate_id`). 위치 목록의 `removed` 행은 회색 처리 + 기본 접힘.
- **대시보드 summary에서 `replaced`를 제외한다.**
  `store/certificates_summary.go`의 집계 쿼리에는 **status 필터가 하나도 없다** — 모든 구간을 `not_after`에서만 파생한다. 그래서 손대지 않으면 교체된 죽은 인증서가 `total`을 부풀리고 `expiring_7d`·`expiring_30d`에도 그대로 섞인다. 알림에서 뺀 것이 대시보드에서 다시 튀어나오면 두 화면이 서로를 부정한다.

### 11.5 재스캔 정책 화면은 만들지 않는다

환경변수 zero-config다. 대신 `/discovery`에 **읽기 전용 카드 1개**를 둔다 — 활성 여부 / 유효 간격 / 감시 중인 엔드포인트 수 / 마지막 실행 시각 / 지연 여부. 여기에 3역할 전용 "지금 1배치 실행" 버튼을 붙인다(11.2).

주기·임계값을 화면에서 편집 가능하게 만들면 정책 테이블·CRUD API·감사 이벤트·권한 게이트가 줄줄이 따라온다. 운영자가 실제로 조정하는 빈도는 설치당 0~1회다. 설정 표면을 늘리지 않는다(05 설계 12장 ⑤와 같은 판단).

"이 위치 다시 확인" 단건 버튼은 범위 밖이다(1.3) — 사용자 트리거 즉시 스캔은 레이트 예산 밖에서 도는 네 번째 발주 경로가 된다.

---

## 12. 설정

### 12.1 신규 환경변수 6개

| 변수 | 기본값 | 의미 |
| --- | --- | --- |
| `RESCAN_ENABLED` | `true` | 재스캔 기능 전체 on/off |
| `RESCAN_INTERVAL` | `24h` | 기본 재확인 간격 (3.1) |
| `RESCAN_RATE_PPS` | `20` | 재스캔 job 레이트 (6장) |
| `RESCAN_BATCH_IPS` | `1024` | 배치 1건의 최대 IP 수 (4.1) |
| `RESCAN_REPLACE_CONFIRM` | `2` | 교체 확정에 필요한 연속 관측 횟수 (8.2 ①) |
| `RESCAN_RETIRE_MISSES` | `30` | 은퇴 판정 연속 미관측 횟수 (3.4) |

### 12.2 값 오류 처리

**값이 잘못되어도 코어 API를 기동 불가로 만들지 않는다.** `cfg.Warnings`에 변수명을 지목한 경고를 담고 기본값으로 폴백한다. 04 설계 8.2·05 설계 10.1이 이미 확립한 규칙이고, `config.Config.Warnings`(`internal/config/config.go:64`)가 그 자리다.

### 12.3 `.env` 취급

`.env`·`.env.example`은 에이전트 권한 밖이라 읽지도 쓰지도 못한다. 따라서

- 신규 변수는 `docs/manual/INSTALL.md`에 **5.2.4 절**로 기재한다(5.2.1 알림 · 5.2.2 스캔 스윕 · 5.2.3 접근 IP 제한 다음 자리).
- `docker-compose.yml`에는 `${VAR:-기본값}` **패스스루만** 추가한다.
- 전부 비어 있어도 정상 동작한다 — 기본값이 곧 권장값이다.

---

## 13. 병렬 구현 단위

| 단위 | 범위 | 주요 파일 | 의존 |
| --- | --- | --- | --- |
| **U1** | 마이그레이션 0015 | `migrations/0015_endpoint_rescan.up/down.sql` | 없음 |
| **U2** | monitor 저장소 + 관측 훅 | `store/rescan.go`(신규), `store/discovery.go`(훅 1개) | U1 |
| **U3** | 교체 조정 | `store/replacement.go`(신규) | U2 |
| **U4** | 재스캔 스케줄러 | `discovery/rescan.go`(신규), `discovery/manager.go` | U3 |
| **U5** | 설정 + 배선 | `internal/config`, `cmd/server/`, `docker-compose.yml` | U4 |
| **U6** | API | `api/discovery.go`, `api/server.go`, `api/certificates_query.go`, `store/certificates_query.go`, `store/certificates_summary.go`, `backend/api/openapi.yaml` | U3 |
| **U7** | 프론트엔드 | `frontend/app/(app)/certificates/`, `frontend/app/(app)/discovery/`, `frontend/lib/api.ts` | U6 |
| **U8** | 문서 | `docs/manual/INSTALL.md`·`OPERATIONS.md`, `docs/MAP.md` | U5, U6 |

### 13.1 머지 순서

```
U1 → U2 → U3 → (U4 ∥ U6) → (U5 ∥ U7) → U8
```

### 13.2 공유 파일 주의

| 파일 | 소유 단위 | 주의 |
| --- | --- | --- |
| `store/discovery.go` | **U2만** | 관측 훅 1개만 넣는다. 다른 단위가 같은 파일을 만지면 충돌한다 |
| `backend/api/openapi.yaml` | **U6만** | 라우트 등록과 명세는 **반드시 같은 커밋** — `TestOpenAPISpecMatchesRoutes` |
| `cmd/server/` | **U5만** | 배선은 한 곳에서 |
| `discovery/manager.go` | **U4만** | |

`certificates_query.go`·`certificates_summary.go`는 `internal/api`와 `internal/store` **양쪽에 같은 이름으로 존재한다.** U6이 둘 다 만지므로 경로를 끝까지 적어 지시한다.

---

## 14. 리스크와 기각한 대안

### 14.1 리스크

| 리스크 | 대응 |
| --- | --- |
| **LB 뒤 다중 인증서 플래핑** — 지문이 A↔B로 번갈아 관측된다 | `current_streak >= 2`(8.2 ①). 진짜 번갈이면 streak이 1을 못 넘어 **어느 쪽도 전이하지 않는다** — 버그가 아니라 정확한 동작이다. "확신할 수 없으면 바꾸지 않는다" |
| **첫 가동 시 누적분 대량 전이** — 그동안 교체됐는데 모르고 있던 건이 한꺼번에 잡힌다 | tick당 전이 상한 200건 + 감사 삽입을 트랜잭션 마지막에 배치(10.2). 남은 건은 다음 tick |
| **job 실패 시 1주기 유실** | claim 방식(7.2)의 의도된 대가. 다음 주기에 자동 복구되며, 신선도 표시가 사용자에게 낡음을 알린다 |
| **감사 볼륨 증가** | 04가 이미 수용한 청크 단위 감사(`discovery.scan`)와 같은 수준. 전이는 인증서 생애 1회이므로 상한이 인벤토리 크기다 |
| **IPS 오탐** | 포트별 그룹 발주(4.1)가 "같은 포트·여러 IP" 형태를 강제해 연속 프로브가 같은 호스트로 가지 않는다. `InterleaveByHost`와 중복 방어(6장) |
| **은퇴 판정이 너무 이르다/늦다** | `RESCAN_RETIRE_MISSES`로 조정. 은퇴는 삭제가 아니라 상태이므로 되돌릴 수 있다(3.4) |

### 14.2 기각한 대안 6개

**① 전용 `rescan_jobs` 테이블** — 재스캔만을 위한 job 테이블을 새로 만든다.
기각 사유: 리퍼·센서 리스·감사·조회 UI를 **전부 다시 구현**해야 한다. `scan_jobs`는 이미 그 넷을 갖고 있고, 재스캔은 "다른 종류의 작업"이 아니라 **같은 스캔의 다른 발주자**다. 05 설계 8장이 알림에서 `scan_jobs` 재사용을 기각한 것과 방향이 반대인데, 근거도 반대다 — 알림은 컬럼 모양·회수 의미론이 달랐지만 재스캔은 완전히 같다.

**② IP당 job 1건** — 엔드포인트 쌍을 정확히 표현하려고 IP마다 job을 만든다.
기각 사유: 감시 엔드포인트 1000개면 하루 수천 건의 job이 생긴다. 조회 UI·감사·리퍼가 전부 그 규모를 떠안고, `scan_jobs` 테이블이 재스캔 노이즈로 뒤덮여 사람이 만든 스캔이 안 보인다.

**③ 관측 트랜잭션에서 즉시 전이** — 지문이 바뀐 그 자리에서 `replaced` 처리.
기각 사유: 감사 해시 체인의 advisory lock이 **스캔 처리량 경로 안으로 들어온다.** 대량 스캔 중 관측 배치마다 전역 락을 잡게 되어 스캔 자체가 느려지고, 로그인·업로드까지 지연된다. 05가 발송 감사를 포기하게 만든 바로 그 문제를 자초하는 셈이다.

**④ 스윕을 주기적으로 재무장** — 기존 전 포트 스윕을 주기 실행해 신선도를 얻는다.
기각 사유: 전 포트 스윕은 **며칠~수십 일짜리**다(04 설계 11.1의 ETA 계산). 신선도 도구로 쓰기엔 해상도가 근본적으로 부족하다. 더구나 04는 "청크는 seq→엔드포인트 순수 함수이므로 **스윕 생성 시점의 정의를 끝까지 유지한다**"(5.2·11.1)를 규약으로 못박았다. 재무장은 이 규약을 정면으로 어긴다.

**⑤ 옛 `location` 행을 삭제** — 교체 시 위치 행을 지운다.
기각 사유: **이력이 소실된다.** "이 인증서가 어느 서버에서 언제까지 서비스됐나"는 인증서 제품이 반드시 답해야 하는 질문이다. `removed` 전이는 0002 CHECK에 이미 있는 값이라 비용도 0이다(9.1).

**⑥ 목록 API의 기본값을 바꿔 `replaced`를 항상 제외** — 서버가 알아서 빼준다.
기각 사유: 기존 호출자의 동작이 조용히 바뀐다. 특히 **CSV export가 같은 필터 입력을 공유하므로**(`store/certificates_export.go:27`) 아무도 요청하지 않은 내보내기 내용 변경이 따라온다. 감사·보고에 쓰이는 산출물이 말없이 달라지는 것은 이 제품에서 허용되지 않는다. 제외는 **프론트가 명시적으로 요청**한다(11.2).
