# ATC-20260907-003 — 제품 검증과 커밋 정리

## 사용자 원문과 승인된 목록

사용자 원문:

> 1,2,3 진행해.

승인된 목록:

> 1. 알림 테스트의 날짜 의존 문제 4건 수정.
> 2. 로컬 발급·배포 코드 재검증, 독립 검토, 커밋 정리.
> 3. 실제 CA·nginx/Apache 환경에서 발급·배포·롤백 검증.

## 목적

현재 로컬 발급·배포 구현과 필요한 기반 구현을 검증해 제품 커밋으로 정리하고, 사용자가 지정한 실제 시험 환경에서 동작을 확인한다.

## 공통 범위와 기준 문서

기존 제품 요구와 구현 범위는 [기존 승인 스펙](ATC-20260907-001-auto-issuance.md)과 [자동 발급 설계](../design/07-auto-issuance.md)의 승인 C/D 및 필요한 A/B를 따른다. 이 문서는 검증 작업을 구분하며 기존 스펙을 수정하지 않는다. 기존 제품 단위 A/B/C/D와 아래 검증 단위 `ATC-20260907-003-A/B/C`는 구분한다.

## 독립 검토 절차

각 검증 단위 A/B/C는 독립 3자 검토 대상이다. 검토 입력을 구분하고 각 단위의 실제 대상 커밋 HEAD와 일치하는 판정 3장 중 2장 이상 pass를 요구한다. 구현자의 테스트 결과를 독립 검토로 대신하지 않는다. 이 절은 검토 진행 절차이며 개별 검사관이 다른 검사관의 판정을 읽어 충족해야 하는 구현 성공 기준이 아니다.

사용자 시험 환경 정보는 대기 중이며, 검증 단위 C의 실제 외부 실행에 필요한 선행 조건이다. 이 입력을 기다리는 동안 검증 단위 A/B는 진행할 수 있다. 사용자 비밀값은 이 문서와 검증 근거에 기록하지 않는다.

## 공통 범위 외

템플릿 수정, 원격 push, 릴리스, E 자동 갱신 구현, P1.5, 운영 데이터 변경은 이번 범위에 포함하지 않는다. 새 유료 발급 승인을 추정하지 않는다. 기존 사용자 변경과 다른 에이전트의 작업을 보존한다.

## ATC-20260907-003-A — 알림 테스트의 날짜 의존 문제 수정

### 목적

시간이 지나도 알림 테스트가 동일한 조건에서 만료 전후의 기대 동작을 검증하도록 한다.

### 범위

날짜 의존 문제가 있는 notify 테스트 네 건을 고정 시각으로 동작하도록 수정하고 관련 notify 테스트를 실행한다.

### 성공 기준

1. 네 테스트가 실제 실행 날짜와 무관한 고정 시각으로 동작한다.
2. 만료 전과 만료 후 알림의 기대 동작 검증을 유지한다.
3. notify 테스트가 통과한 근거를 남긴다.

### 범위 외

공통 범위 외 항목과 알림 기능의 새 요구 추가, 문제와 무관한 리팩터링은 제외한다.

### 가정

수정 대상은 앞서 보고된 날짜 의존 문제 네 건이다. 사용자 시험 환경 정보는 이 단위의 선행 조건이 아니다.

## ATC-20260907-003-B — 로컬 제품 검증과 커밋 정리

### 목적

승인된 발급·배포 구현의 로컬 동작을 재검증하고, 검토 가능한 제품 커밋으로 정리한다.

### 범위

기존 승인 C/D와 필요한 A/B의 로컬 구현을 확인하고 백엔드·프런트엔드·브라우저 검증 근거를 남긴다. 제품 커밋에는 해당 범위의 변경만 포함한다.

### 성공 기준

1. 제품 커밋에는 기존 승인 C/D와 필요한 A/B만 포함하며 기존 사용자 변경을 보존한다.
2. 전용 임시 PostgreSQL을 사용하는 전체 Go 테스트(`-p 1 -count=1 ./...`), 프런트 테스트·lint·build, 브라우저 주요 경로 검증의 근거를 남긴다.
3. 현재 제품 커밋과 재실행 가능한 검증 근거를 명시한다.

### 범위 외

공통 범위 외 항목과 기존 승인 제품 범위를 넓히는 구현은 제외한다. 실제 CA 검증 결과는 검증 단위 C에서 다룬다.

### 가정

로컬 검증에는 운영 데이터와 분리된 전용 임시 PostgreSQL을 사용한다. 사용자 시험 환경 정보는 이 단위의 선행 조건이 아니다.

## ATC-20260907-003-C — 제공 데모 CA의 실연결과 API 계약 검증

### 추가 사용자 원문과 현재 실행 경계

> docs 폴더 밑에 apiinfo.txt 파일 만들었고 거기 turingsign api 문서url이랑 , api key , api id 저장해놨어. 참고해서 테스트해봐 아마 실제 인증서 발급은안될거고 어차피. (테스트할 도메인도 없잖아. api 로직이랑, 관련해서 구현만 해두는 용도. ) 근데 데모라서 인증서 발급될 수도 있어.

최신 승인은 도메인 없이 실제 로그인·상품 조회 및 API 계약을 검증하고 필요한 계약 수정을 구현하는 범위다. 주문·실발급·실Linux배포는 도메인·호스트·비용 전제가 충족되지 않아 실행하지 않는다. 아래 기존 실환경 검증 목적과 원문은 후속 작업의 경계로 보존하며 현재 완료 기준은 이 절의 성공 기준으로 좁힌다.

### 목적

사용자가 지정한 시험 CA·도메인과 Linux nginx·Apache 환경에서 발급부터 배포·롤백까지 실제 동작을 확인한다.

### 범위

기존 목표는 지정된 시험 환경에서 발급 → 등재 → 배포 → reload → 지문 확인 → 롤백 → 감사 확인을 수행하는 것이다. 현재는 제공된 공개 문서와 실제 로그인·상품 조회를 대조하고, wildcard 검증 이름 정규화·중복 제거·충돌 거부와 문서에 명확한 신규 주문 신청자의 organization 역할 검증을 최소 구현한다. 인증서 SAN과 입력 Spec은 보존한다.

### 성공 기준

1. 사용자 제공 인증정보로 동일 문서 호스트의 실제 로그인·상품 조회를 실행하고 비밀 없는 상태·응답 구조·기존 Go 커넥터 해석 근거를 남긴다.
2. 공식 주문 계약에 맞게 domain_validations.name의 정확한 `*.` 접두사만 제거하고 같은 이름·방식은 순서가 일정하게 중복 제거한다. 같은 이름의 다른 방식은 주문 요청 전에 거부한다. 일반 도메인·다중 SAN과 입력 Spec을 보존하며 실제 주문 없이 httptest의 outgoing JSON으로 검증한다.
3. 공식 단일 주문 신청자 스키마의 organization 역할을 신규 연락처 검증에 적용한다. 기존 연락처 ID만 있는 요청의 역할은 서버에서 검증하며 로컬에서 추정하지 않는다.
4. 실제 API 성공과 로컬 계약 테스트를 구분하고 관련 upstream·issuance/API 회귀 테스트 근거를 남긴다. 주문·실발급·실Linux배포는 미실행임을 명시하고 해당 후속 검증을 성공으로 판정하지 않는다.

### 범위 외

공통 범위 외 항목과 사용자가 지정하지 않은 CA·도메인·배포 환경에서의 실행은 제외한다. 시험 권한을 운영 환경 변경이나 새 유료 발급 권한으로 확대하지 않는다.

### 가정과 실행 선행 조건

실제 외부 실행에는 사용자 지정 시험 CA, 도메인, Linux nginx·Apache 대상 환경과 필요한 접근 권한이 있어야 한다. 새 비용이 발생하는 실행은 해당 비용의 승인이 필요하다. 환경 정보 대기는 이 단위의 실행 선행 조건이며 검증 단위 A/B를 막지 않는다.

## 위임장 기록 원칙

확정 위임장은 전달받은 원문 그대로 아래 해당 단위 절에 보관한다. A/B는 동일한 순차 작업 위임장의 적용을 받는다.

## 위임장 ATC-20260907-003-C

이미 승인됨 — 확인 질문 없이 완료까지. 사용자 원문 "docs 폴더 밑에 apiinfo.txt 파일 만들었고 거기 turingsign api 문서url이랑 , api key , api id 저장해놨어. 참고해서 테스트해봐 아마 실제 인증서 발급은안될거고 어차피. (테스트할 도메인도 없잖아. api 로직이랑, 관련해서 구현만 해두는 용도. ) 근데 데모라서 인증서 발급될 수도 있어." status unit ATC-20260907-003-C, note 데모 CA API 계약 보완, 3자 검토 대상. 기존003-C는실환경검증항목이었으나최신승인은도메인없이실API로직확인에초점이있다. 첫단계로 docs/specs/ATC-20260907-003-product-validation.md에위사용자추가원문과 '실제로그인/상품조회 및 API계약검증; 주문/실발급/실Linux배포는도메인·호스트·비용전제미충족으로미실행'을추가기록하고 C성공기준을그경계로명확히하라. 과거원문지우지말고003-C위임장절에이프롬프트를그대로저장,확인필요마커없는확정범위만수행한다.
성공 기준: 공식public-spec의wildcard금지에맞게 upstream order domain_validations.name에서 * . 아닌정확한 '*.' 접두사만제거하고 certificate.dns_names/SAN과입력Spec은보존. 동일base+wildcard가합쳐지면같은name+method는결정론적으로dedup,같은name다른method는주문요청전에명확히거부(임의선택금지). 일반domain/다중SAN기존동작유지. 실제주문POST없이httptest의outgoingJSON으로SAN보존·검증이름정규화·dedup·충돌거부·입력불변검증. 추가발견인organization역할필수는정확한공식schema를대조하고상황에맞는Subscriber.Validate검증을최소보완한다: 복수contacts중역할요구인지각contact요구인지/기존contactID예외를문서와코드로확인하고임의로강화하지않는다. 문서가모호하면이항목은미확정으로보고하되wildcard수정은완료.
항목별범위판정: wildcard계약수정 — 동일CA커넥터파일·가역적·위험감소 — 즉시투입. organization역할검증 — 동일요청검증파일·공식요구명확할때가역적위험감소 — 즉시투입. 모호할때는보고하고자의적제품요구추가금지.
소유권은 backend/internal/issuance/upstream/{tlsmanager.go,tlsmanager_test.go,types.go 및필요한동일패키지test} + 003스펙 C절만. 다른보안planner가설계중이며B보안코드/API/프런트/설정/기존apiinfo파일수정금지. 다른사용자/에이전트의변경을되돌리지말것. 수정전해당코드만본인scratch백업. secret보호조건은이전위임그대로: apiinfo tracked금지,로컬프로그램으로만키읽기,응답원문/키/ID/token출력저장/argv전달금지. 실제login/products는앞서기존Go커넥터성공근거를유지하고이번변경이해당경로에영향없으면실외부호출반복불필요. 새주문·발급·취소·DNS·실Linux배포금지.
검증은해당upstream단위테스트및관련issuance/API회귀를전용일회용DB로필요한만큼실행. 공유frontendbuild불필요. API의정확한공개문서근거와변경테스트결과를 .codex/tmp/live-ca-api-003/implementation.md에저장. stage는명시파일만, 커밋메시지에ATC-20260907-003-C 포함,커밋직후git show --stat으로혼입없음확인. 전역add/stash/reset/clean금지,실행기/템플릿/비밀파일커밋금지. 새로발견한타인commit을되감지말고현재HEAD변동은보존보고. 원격push금지. 이로인한A/B기존판정대상HEAD변경은부모가후속범위검사에서관리하니판정파일자가갱신금지. Do not return full file contents; return only the list of changed files plus a key summary.

## 위임장 ATC-20260907-003-A

기준 텍스트: 사용자 "1,2,3 진행해."; 승인 목록 "1. 알림 테스트의 날짜 의존 문제 4건 수정. 2. 로컬 발급·배포 코드 재검증, 독립 검토, 커밋 정리. 3. 실제 CA·nginx/Apache 환경에서 발급·배포·롤백 검증."
status unit: ATC-20260907-003-A, note: 알림 테스트 날짜 독립성 수정. status unit: ATC-20260907-003-B, note: 발급·배포 제품 검증과 커밋 정리. 둘 다 3자 검토 대상이며 독립 판정은 부모가 별도로 소환한다. 현재 위임은 A/B만, C 실환경은 사용자 시험 환경 정보 대기 중이다.
성공 기준은 위 스펙 A/B 기준이며 구체적으로: A는 notify 만료 전·경계·후 판단을 고정된 기준 시각으로 검사하여 네 실패를 시계와 독립적으로 해결한다. 운영의 실제 시각 판정은 유지하고 전역 가변 clock이나 단순 먼 미래 날짜로 연기하지 않는다. B는 기존 승인 C/D와 필요한 A/B 코드만 제품 커밋으로 정리하고 임시 DB 전체 Go 테스트(-p 1 -count=1 ./...), build/vet, frontend test/lint/build, 주요 발급/설정/에이전트 화면의 실제 브라우저 검증 근거를 확보한다. 로컬/mock 검증을 실 CA 검증으로 보고하지 않는다.
작업 근거와 소유권: .codex/tmp/product-scope-probe/summary.md의 명시 제품 후보 목록+notify 수정 파일+신규 003 스펙, 필요 시 C/D 관련 제품 설명의 낡은 예정 문구만. main 기준 커밋 7c5c9e95615ceae0a4008e351e82be8f0e705973을 시작 시 확인하고 다른 세션의 변화가 있으면 보존한다. 제품 변경이 untracked 및 공유 배선에 섞여 있으므로 같은 체크아웃에서 한 구현자만 A→B 순차로 수정/커밋한다. 다른 에이전트와 사용자가 함께 있으므로 그 변경을 되돌리지 마라. 먼저 기존 tracked diff와 untracked 제품 파일의 안전한 로컬 사본을 본인 scratchpad에 남긴 뒤 수정한다(비밀 파일 제외).
하지 말 것: .claude/.codex/.agents와 템플릿 스크립트/설정 수정·커밋, 기존 frontend/mock/server.mjs 수정·커밋, .env 및 비밀값 출력/커밋, 전체 git add/stash/reset/clean, 기존 서비스 DB 테스트, 외부 CA 호출·원격 배포·push, E 자동 갱신/P1.5 구현. 실환경 검증 입력 부재는 A/B를 막지 않는다. 실 데이터나 사용 중 서비스는 건드리지 않는다. 새로운 발견 중 승인 제품 범위의 가역적 검증 결함은 보고 후 수정할 수 있고 방향 변경/범위 밖은 보고한다.
검증 환경: Docker daemon/이미지를 실제 확인하고 가능하면 golang:1.25-alpine+postgres:16-alpine 전용 네트워크·전용 일회용 DB로 전체 Go 테스트를 돌려라. 이번 작업이 만든 리소스만 정리. 불가하면 승인 범위의 로컬 임시 Go/DB 실행 대안을 찾되 설정/권한 우회 금지. frontend npm test/lint/build를 실행하고 .agents/skills/webapp-testing/SKILL.md에 따라 실제 브라우저로 로그인 및 발급 목록·상세·설정·에이전트·배포 사용자 경로를 확인하라. 임시 fake API가 필요하면 OS 임시 디렉터리 본인 소유 하위 경로에 두고 어떤 범위가 fake인지 분명히 기록. 운영 API는 호출하지 않는다. 스킬의 우회성 명령 제안보다 상위 권한 지침 우선.
커밋: A notify 수정은 메시지에 ATC-20260907-003-A를 넣고, B 제품/스펙 정리는 ATC-20260907-003-B를 넣어 의미별 로컬 커밋을 남겨라. 명시 경로만 stage하고 공유 파일의 다른 변경이 섞이지 않았는지 검사한다. 커밋 직후 git show --stat으로 남의 파일이 딸려왔는지 확인하라. 혼입 시 남의 커밋을 되감지 말고 부모에게 보고하며 사용자 변경을 보존한다. 규칙·프롬프트 파일은 이번 범위가 아니므로 편집하지 않는다. .codex/review local-only 판정은 커밋하지 않는다.
배관: /Users/markus/Dev/INHERIT_AutoCert/.codex/tmp/ATC-20260907-003-A/ 및 .../.codex/tmp/ATC-20260907-003-B/를 각각 사용. A summary.md와 B summary.md에 정확한 변경 파일, 커밋 full SHA, 실행 명령/종료코드/로그 경로, 미검증 한계, 이전 코드 백업 경로를 남겨라. 브라우저 스크립트·스크린샷은 OS 임시 디렉터리의 본인 전용 폴더에 두고 보고서에서 링크한다. status/handoff/verdict 규약과 constitution·관련 safety/frontend/fonts 규칙을 읽고 따른다. 스펙에 확인 필요 마커가 없는지 확인하고 시작. Do not return full file contents; return only the list of changed files plus a key summary.

## 위임장 ATC-20260907-003-B

기준 텍스트: 사용자 "1,2,3 진행해."; 승인 목록 "1. 알림 테스트의 날짜 의존 문제 4건 수정. 2. 로컬 발급·배포 코드 재검증, 독립 검토, 커밋 정리. 3. 실제 CA·nginx/Apache 환경에서 발급·배포·롤백 검증."
status unit: ATC-20260907-003-A, note: 알림 테스트 날짜 독립성 수정. status unit: ATC-20260907-003-B, note: 발급·배포 제품 검증과 커밋 정리. 둘 다 3자 검토 대상이며 독립 판정은 부모가 별도로 소환한다. 현재 위임은 A/B만, C 실환경은 사용자 시험 환경 정보 대기 중이다.
성공 기준은 위 스펙 A/B 기준이며 구체적으로: A는 notify 만료 전·경계·후 판단을 고정된 기준 시각으로 검사하여 네 실패를 시계와 독립적으로 해결한다. 운영의 실제 시각 판정은 유지하고 전역 가변 clock이나 단순 먼 미래 날짜로 연기하지 않는다. B는 기존 승인 C/D와 필요한 A/B 코드만 제품 커밋으로 정리하고 임시 DB 전체 Go 테스트(-p 1 -count=1 ./...), build/vet, frontend test/lint/build, 주요 발급/설정/에이전트 화면의 실제 브라우저 검증 근거를 확보한다. 로컬/mock 검증을 실 CA 검증으로 보고하지 않는다.
작업 근거와 소유권: .codex/tmp/product-scope-probe/summary.md의 명시 제품 후보 목록+notify 수정 파일+신규 003 스펙, 필요 시 C/D 관련 제품 설명의 낡은 예정 문구만. main 기준 커밋 7c5c9e95615ceae0a4008e351e82be8f0e705973을 시작 시 확인하고 다른 세션의 변화가 있으면 보존한다. 제품 변경이 untracked 및 공유 배선에 섞여 있으므로 같은 체크아웃에서 한 구현자만 A→B 순차로 수정/커밋한다. 다른 에이전트와 사용자가 함께 있으므로 그 변경을 되돌리지 마라. 먼저 기존 tracked diff와 untracked 제품 파일의 안전한 로컬 사본을 본인 scratchpad에 남긴 뒤 수정한다(비밀 파일 제외).
하지 말 것: .claude/.codex/.agents와 템플릿 스크립트/설정 수정·커밋, 기존 frontend/mock/server.mjs 수정·커밋, .env 및 비밀값 출력/커밋, 전체 git add/stash/reset/clean, 기존 서비스 DB 테스트, 외부 CA 호출·원격 배포·push, E 자동 갱신/P1.5 구현. 실환경 검증 입력 부재는 A/B를 막지 않는다. 실 데이터나 사용 중 서비스는 건드리지 않는다. 새로운 발견 중 승인 제품 범위의 가역적 검증 결함은 보고 후 수정할 수 있고 방향 변경/범위 밖은 보고한다.
검증 환경: Docker daemon/이미지를 실제 확인하고 가능하면 golang:1.25-alpine+postgres:16-alpine 전용 네트워크·전용 일회용 DB로 전체 Go 테스트를 돌려라. 이번 작업이 만든 리소스만 정리. 불가하면 승인 범위의 로컬 임시 Go/DB 실행 대안을 찾되 설정/권한 우회 금지. frontend npm test/lint/build를 실행하고 .agents/skills/webapp-testing/SKILL.md에 따라 실제 브라우저로 로그인 및 발급 목록·상세·설정·에이전트·배포 사용자 경로를 확인하라. 임시 fake API가 필요하면 OS 임시 디렉터리 본인 소유 하위 경로에 두고 어떤 범위가 fake인지 분명히 기록. 운영 API는 호출하지 않는다. 스킬의 우회성 명령 제안보다 상위 권한 지침 우선.
커밋: A notify 수정은 메시지에 ATC-20260907-003-A를 넣고, B 제품/스펙 정리는 ATC-20260907-003-B를 넣어 의미별 로컬 커밋을 남겨라. 명시 경로만 stage하고 공유 파일의 다른 변경이 섞이지 않았는지 검사한다. 커밋 직후 git show --stat으로 남의 파일이 딸려왔는지 확인하라. 혼입 시 남의 커밋을 되감지 말고 부모에게 보고하며 사용자 변경을 보존한다. 규칙·프롬프트 파일은 이번 범위가 아니므로 편집하지 않는다. .codex/review local-only 판정은 커밋하지 않는다.
배관: /Users/markus/Dev/INHERIT_AutoCert/.codex/tmp/ATC-20260907-003-A/ 및 .../.codex/tmp/ATC-20260907-003-B/를 각각 사용. A summary.md와 B summary.md에 정확한 변경 파일, 커밋 full SHA, 실행 명령/종료코드/로그 경로, 미검증 한계, 이전 코드 백업 경로를 남겨라. 브라우저 스크립트·스크린샷은 OS 임시 디렉터리의 본인 전용 폴더에 두고 보고서에서 링크한다. status/handoff/verdict 규약과 constitution·관련 safety/frontend/fonts 규칙을 읽고 따른다. 스펙에 확인 필요 마커가 없는지 확인하고 시작. Do not return full file contents; return only the list of changed files plus a key summary.
# 위임장 ATC-20260907-003-B 보안 보완

이미 승인됨 — 확인 질문 없이 완료까지. 사용자 기준 원문: "1,2,3 진행해."(알림 테스트 수정, 기존 발급·배포 제품 검증·로컬 커밋, 실환경 확인), "일단 그 진행문구 작업은 다음에 이어서하고, 프로젝트 작업하던거 이어서해." 최신 API 파일 제공은 .codex/tmp/live-ca-api-003/summary.md와 커밋 fa13c07018e993b7388b09dbeeca841e0a15b6d1에서 로그인·상품 조회/계약 수정만 완료했으며 실제 발급은 하지 않았다.
status unit: ATC-20260907-003-B, note: 발급·배포 보안 보완. 3자 검토 대상. 역할/runtime/constitution/관련 안전 규칙을 읽고 실행하라. 당신은 혼자 작업하지 않으므로 다른 사용자의 템플릿/제품 수정과 타인 커밋을 되돌리지 마라. 공유 체크아웃 단일 코드 작성·커밋 담당이다. 시작 기준 main fa13c07018e993b7388b09dbeeca841e0a15b6d1과 현재 HEAD를 확인하고 타인 진전은 보존. 필수 참조 docs/design/security-plan-003.md 및 .codex/review/ATC-20260907-003-B-design.verdict.md는 로컬 파일이며 설계 사전 검사 PASS다.
첫 단계: docs/specs/ATC-20260907-003-product-validation.md에 이 최종 위임장 본문을 '위임장 ATC-20260907-003-B 보안 보완'으로 보존하고 확정 실행 범위를 별도 절에 작성한 뒤 해당 절의 확인 필요 마커 0을 검사한다. 전체 설계에 있는 미답 웹서버 키는 범위 밖의 보류이므로 없애거나 승인으로 간주하지 마라.
성공 기준: 통과한 security-plan-003의 1~4 및 최초 공급→재시작 계약을 구현한다. (1) app_id를 secret과 함께 암호화 저장·API 비노출·편집 시 기존 유지·기존행 명시적 이행/원자성. (2) lease/start/job heartbeat/agent heartbeat 상태와 감사 동일 트랜잭션, middleware 무감사 상태갱신 제거, material running 이후만 허용. (3) 별도 봉인 Ed25519 signer, 외부 공급 신뢰 공개키 핀, payload 전체 서명·엄격 형식/시간/agent검증, 디스크 journal·잠금으로 재시작 replay 차단, 서명 실패 tx rollback, unsigned fallback 금지. (4) agent PKCS8 개인키 AESGCM 봉인, state 밖 별도 root 전용 identity.env 공급·초기화/명시적 이행 구현, installer와 CLI 등록/재시작까지 연결. 설계의 위협 경계·실제 실행 구분을 유지하라. 같은 B UI 두 파일의 최소 정리도 당신이 순차 수행: issuance/page.tsx 상태 라벨 nowrap/모바일 상태 셀 읽힘, agents/page.tsx 고정 부제 제거. 다른 UI 체계 변경 금지.
소유권: 설계 표의 backend API/store/schema/agentwire/agent/deploysign/config/cmd 및 해당 테스트, scripts/install-agent.sh, frontend/lib/issuance.ts·settings/issuance/page.tsx·issuance/page.tsx·agents/page.tsx, 제품 OpenAPI/07 design/manual AGENT와 001 스펙의 정정 부록 및003 스펙. 001의 옛 mTLS 대체 위임은 역사로 보존하고 원래 요구와 달랐음을 부록으로 바로잡는다. C의 upstream 계약 파일은 수정하지 말 것. backend 공유 계약이 강하게 결합되고 UI 수정은 소규모라 한 담당이 통합하며 추가 worker를 띄우지 않는다.
하지 말 것: 웹서버 적용/임시/백업 개인키의 평문 예외 승인 또는 대체 설계 선택, 실제 키 생성·운영 데이터 이행·운영 서비스 변경·실CA 주문/발급/추가 조회·DNS/실배포, apiinfo.txt 읽기/출력/커밋, 템플릿·실행기·전역설정·mock/server.mjs 변경, 원격 push. 웹서버 미답은 전체B 완료를 막지만 확정된 4개 보완은 진행한다. 신규 운영 공급자를 가정하지 말고 설계의 기존 systemd EnvironmentFile 최소안을 따른다.
검증: 먼저 중요한 실패/거부 회귀를 만들고 구현. 전용 임시 PostgreSQL의 전체 Go -p 1 -count=1 ./..., build/vet, agent linux amd64/arm64 빌드, frontend test/lint/build, installer 격리 검증과 실제 브라우저 375/768/1440px 설정 편집·발급·agent 경로를 검증. DB audit 실패 rollback/동시start, 필드 변조/다른핀/agent/만료/재시작 replay, appid 이행 실패/재실행, identity 원본 보존/크래시 재실행/키 불일치, 새 agent 프로세스 두 번으로 같은 신원 복원 등 meaningful evidence 필수. webapp-testing 스킬 적용. fake 증거를 실CA/systemd/nginx 성공으로 보고하지 말고 미실행은 구별. 본인 테스트 자원만 정리.
배관: .codex/tmp/security-repair-003/ 전용 scratchpad와 summary.md에 파일/테스트 명령/결과/한계/백업/커밋 full SHA를 기록. status/handoff-protocol 준수. 명시 파일만 stage, ATC-20260907-003-B 포함 로컬 커밋. 커밋 직후 git show --stat으로 혼입 확인. 전체add/stash/reset/clean 금지, 남의 커밋 되감기 금지. 판정 파일은 부모의 독립 검사관 몫이므로 자가 pass 금지.
실패 판정 반성 원문:
1. 무엇이 요구와 달랐나: 에이전트 mTLS 개인키가 헌장의 저장 암호화 요구와 달리 평문 PKCS#8로 저장됐다.
2. 왜 그렇게 됐다고 보나: 파일 권한 제한을 저장 암호화와 동일한 보호로 취급했으며 해당 키의 예외 승인을 기록하지 않았다.
3. 다음 시도는 무엇을 다르게 해야 하나: 에이전트 키의 저장 암호화·키 해제 방식을 구현하거나 정확한 키 범위의 명시적 예외 승인을 확보하고 저장 및 재기동 회귀를 검증해야 한다.
1. 무엇이 요구와 달랐나: 작업 서명, app_id 암호화, 로컬 개인키 저장 규칙, 변경 API 감사 요구가 충족되지 않았다.
2. 왜 그렇게 됐다고 보나: mTLS·파일 권한·종료 이벤트와 기존 테스트 성공을 각각 서명·암호화·전 단계 감사의 충분한 증거로 취급했다.
3. 다음 시도는 무엇을 다르게 해야 하나: 명시된 요구대로 구현·회귀 검증하고, 필요한 보안 예외는 승인된 요구 본문에 먼저 확정한 뒤 그 경계로 재검토한다.
Do not return full file contents; return only the list of changed files plus a key summary.

## B 보안 보완 확정 실행 범위

security-plan-003의 1~4와 최초 공급·재시작 계약: app_id 봉인·명시적 전진 이행, 상태 변경과 감사 원자성, Ed25519 개별 작업 서명·관리자 핀·재전송 차단, agent 신원 봉인·별도 identity.env·명시적 이행을 구현하고 가상 데이터로 검증한다. 발급 상태 라벨과 모바일 상태 셀, agent 고정 부제를 최소 수정한다. 웹서버 키 정책 결정·실제 운영 키 생성·운영 이행·실CA 호출·실배포·템플릿 수정·push는 제외한다. 미답 웹서버 정책은 별도 설계의 보류로 유지하며 이 실행 범위의 완료를 전체 B 보안 완료로 바꾸지 않는다.
