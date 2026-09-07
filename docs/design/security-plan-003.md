내가 이해한 설계 범위: ATC-20260907-003-B의 보안 보완 네 가지와 작은 UI 결함을 승인된 C·D 안에서 설계한다.
갈림길: 웹서버 적용·임시·백업 개인키의 저장 예외는 미답이다. 아래 네 보완은 구현 가능하며 해당 예외를 승인한 것으로 간주하지 않는다.

## 근거와 확정 경계
- 주입력은 `.codex/tmp/review-divergence-003/summary.md`다. 세 raw 판정을 재선택하지 않았고 지정 코드와 요구 본문만 대조했다.
- 사용자 원문 “1,2,3 진행해.”와 “일단 그 진행문구 작업은 다음에 이어서하고, 프로젝트 작업하던거 이어서해.”가 승인 근거다. 반복 진행 확인은 필요 없다.
- `docs/constitution.md` 보안 1·2·3·5 및 `docs/specs/ATC-20260907-001-auto-issuance.md` 성공 기준·하지 말 것이 요구 정본이다. 01-plan/02-goals의 초기 Phase 1보다 이후 C·D 승인을 우선한다.
- 과거 001-B 위임장의 mTLS 대체 문장은 요구 본문과 상충한다. 과거 기록은 보존하고 정정 부록에 “mTLS와 개별 작업 서명 모두 필수”를 추가하며 현재 설계·매뉴얼의 대체 설명을 고친다.
- `docs/apiinfo.txt`의 실제 계정은 이 설계에서 읽지 않았다. 별도 담당자의 실API 조회 검증과 분리하며, 도메인·대상 Linux 서버·웹서버 키 예외는 아직 주어지지 않았다.

## 접근
표준 AES-256-GCM으로 저장을 봉인하고 Ed25519로 작업 자체를 서명한다. 상태 변경과 감사는 같은 PostgreSQL 트랜잭션에서 성공하거나 함께 취소된다.
신뢰 공개키는 관리자가 별도 경로로 공급한다. 에이전트 전용 해제 키는 root 전용 운영 설정에서 기동마다 공급하며 서버 마스터 키를 공유하지 않는다. 신원 저장 디렉토리만 복사해서 개인키를 복호화할 수 없어야 한다.
새 코드·가상 데이터 회귀는 진행하되 실제 데이터 이행·키 생성·운영 서비스 변경은 실행하지 않는다. 이번 문서는 부모 검사 전 초안이며 승인 예외를 새로 만들지 않는다.

## 1. app_id 저장과 기존 행의 전진 이행
- `backend/internal/store/issuance.go`, `api/handlers_issuance.go`, `issuance/service.go`: AppID를 저장 모델에서 평문 응답 필드로 겸용하지 않고 `app_id_sealed`·`app_id_nonce`로 저장한다. 기존 `ISSUANCE_ENC_KEY`와 `sensorca.Seal/Open`을 쓰되 secret과 매번 다른 난수 nonce를 사용한다.
- 새 전진 SQL migration은 두 bytea 열과 짝 유효성 제약을 추가하고 기존 `app_id`의 NOT NULL을 해제한다. 신규·수정 경로는 봉인값만 쓰며 평문 열은 NULL로 만든다. 적용된 0023은 수정하지 않는다.
- 별도 명시적 이행 명령 `backend/cmd/issuance-migrate/main.go`를 구현한다. 유지보수 창의 전용 tx에서 대상 행 잠금→봉인→복호화 일치 확인→평문 NULL→감사까지 처리하고 전체 성공 시 commit한다. 실패 시 전부 rollback, 재실행 시 봉인 완료 행은 건너뛴다.
- 이행 전 레거시 행은 목록의 설정 상태만 보여 주고 연결 실행은 차단한다. 잘못된 키·손상 암호문을 평문 읽기로 우회하지 않는다. 앱 시작으로 실데이터 이행을 자동 실행하지 않는다.
- API는 `app_id_configured`만 반환한다. 생성 시 App ID 필수, 수정 시 누락/빈 값은 기존 유지, 새 값은 길이 검증 후 교체한다. `frontend/lib/issuance.ts`와 `settings/issuance/page.tsx`는 비밀번호 입력·기존 유지 안내로 맞추고 되읽기를 없앤다.
- Connector는 호출에 필요한 때만 두 값을 복호화하고 캐시 키는 연결 ID·URL·두 암호문/nonce에 결합한다. 수정 후 이전 자격증명 캐시를 재사용하지 않는다. 감사에는 값·암호문·해시를 싣지 않는다.
- 이행 완료 기준은 활성·비활성 기존 행 모두 봉인되고 평문 열이 NULL인 것, 신규 저장과 편집·연결이 동작하는 것이다. WAL·기존 백업·스토리지 잔존 데이터의 제거까지 완료했다고 주장하지 않으며 구버전 평문 writer로 롤백하지 않는다.

## 2. lease/start/heartbeat와 감사 원자성
- `store/deployment.go`: `LeaseDeployJob`은 pending→leased 및 token/만료 생성·`deploy.job.lease` 감사를 같은 tx에 넣는다. `TouchDeployJob`의 start/heartbeat도 조건부 UPDATE와 해당 감사가 하나의 tx다.
- 감사의 target_type은 deploy_job, target_id는 작업 ID, 실행 주체는 인증된 agent_id 메타데이터다. user FK에 agent ID를 넣지 않는다. Request ID·이전/이후 상태·만료만 기록하고 lease token과 키는 제외한다.
- 성공한 job heartbeat는 매번 변경 감사가 남는다. 상태 불일치·만료·다른 agent/token은 변경·성공 감사 0건, 감사 삽입 실패는 상태·만료 모두 불변, 병렬 start는 1건만 성공해야 한다.
- `agentgw/gateway.go:auth`의 오류를 무시하는 `TouchAgentLastSeen` 부수 UPDATE를 제거한다. 명시적 agent heartbeat는 `store/agents.go`에서 last_seen·`agent.heartbeat` 감사를 같은 tx로 처리하고 실패를 응답한다. 다른 요청이 필요로 하는 생존 갱신은 그 변경 tx 안에 포함한다.
- material 응답은 성공한 start 이후 running·유효 lease·활성 agent에서만 가능하게 좁히고 기존 key.release 감사를 유지한다. 인증 middleware가 먼저 별도 상태 변경을 commit하지 않아야 원자성 주장이 성립한다.

## 3. mTLS와 독립된 작업 서명·신뢰·재전송 경계
- Go `crypto/ed25519`의 별도 작업 키를 사용한다. 센서/서버 CA 개인키·agent 신원 개인키를 재사용하지 않는다. 작업 서명 seed는 AES-GCM 봉인 파일로만 저장하고 서버가 기존 ISSUANCE_ENC_KEY로 메모리에서 해제한다.
- `backend/internal/deploysign/`에 서명·검증·봉인 읽기와 명시적 초기화 명령용 함수를 둔다. `backend/cmd/deploy-signing-key/main.go`는 생성/공개키 내보내기를 제공하되 기존 키를 덮어쓰지 않고 개인키·평문 seed를 출력하거나 임시 저장하지 않는다. 이번 작업에서는 테스트 난수 외 실제 키 생성 명령을 실행하지 않는다.
- `config/config.go`·`cmd/server/main.go`는 `DEPLOY_SIGNING_KEY_FILE`의 봉인 키를 주입한다. 미설정·손상·해제 실패 시 배포 lease를 비활성화하며 서명 없이 내려보내지 않는다. 나머지 조회·인벤토리는 기존대로 사용한다.
- `agentwire/wire.go`의 lease 응답은 `{payload, signature}`이며 payload는 base64 원본 바이트다. 서명 대상은 고정 프로토콜 접두어와 payload 바이트의 결합이고 알고리즘은 Ed25519 한 가지다.
- payload의 고정 구조체는 version, key_id(SHA-256 공개키 지문), job_id, agent_id, kind, Target 전체 스냅샷, expected_fingerprint, lease_token, issued_at·expires_at(UTC 정수 초)를 포함한다. JSON 재인코딩 일치 검사로 중복/미지 필드·모호한 표현을 거부하고 크기를 제한한다.
- 관리자는 서버의 공개키 내보내기 결과를 신뢰된 관리 경로로 확인해 agent의 `--job-signing-public-key-file`에 설치한다. CA 지문과 별도 핀이고 등록 응답/lease가 준 키를 자동 신뢰하지 않는다. 파일은 state-dir 밖 관리자 소유·비인가 쓰기 금지이며 누락/다른 키면 기동을 거부한다.
- `agentgw`의 lease 생성은 순수 로컬 signer를 store tx 안에서 호출해 서명 실패도 lease·감사와 rollback한다. 네트워크 호출은 tx에 넣지 않는다. 서명된 만료는 최초 start 허용 기한이며 start 이후 연장은 DB heartbeat 만료가 지배한다.
- `agent/run.go`는 어떤 start/material/파일 변경보다 먼저 서명·버전·현재 agent ID·작업 종류·대상 검증·지문·시각을 검사한다. 발행 미래 시각 허용은 최대 30초, 최초 수락은 expires_at 이전이며 lease 길이는 기존 90초를 넘기지 않는다.
- 검증 후 `(job_id, SHA-256(lease_token))` 수락 기록을 0600 journal에 원자 저장하고 fsync한 뒤 start한다. 단일 agent 프로세스 잠금으로 병렬 수락을 막으며 재시작·중복 payload도 거부한다. journal 손상/쓰기 실패는 실행 중단이며 기록 삭제로 우회하지 않는다.
- 서버의 start CAS는 leased→running 1회만 허용한다. 소비 기록 후 응답 손실·크래시는 재실행하지 않고 기존 만료/수동 재시도 경로로 보낸다. 중복 결과 보고의 기존 멱등성과 혼동하지 않는다. root가 디스크·프로세스를 장악한 공격까지 막는 설계는 아니다.

## 4. 에이전트 신원 개인키 봉인·해제 공급·안전 이행
- `agent/state.go`는 PKCS#8 DER을 표준 AES-256-GCM으로 봉인한 version/nonce/ciphertext 파일만 저장한다. AAD에 고정 용도·version·agent_id를 결합하고 입력 키는 난수 32바이트다. 평문 DER/PEM은 메모리에서만 처리하며 가능한 바이트 버퍼를 clear한다.
- 선택한 최소 공급은 서버 `ISSUANCE_ENC_KEY`처럼 관리자 소유 비밀 설정을 신뢰하는 방식이다. agent마다 별도 난수 키를 `/etc/autocert-agent/identity.env`의 `AGENT_IDENTITY_ENC_KEY=<64자리 hex>` 한 항목으로 보관한다. 이 해제 키 파일은 평문 운영 비밀이며 신원 개인키 파일은 아니다. 디렉토리 root:root 0700·파일 0600, state-dir 밖 별도 설정/백업 범위이며 두 경로의 포함 관계·symlink·비인가 쓰기를 거부한다.
- `agent/keysource.go`·`cmd/agent/main.go`의 `init-identity-key --key-file <경로>`는 관리자가 명시적으로 실행하는 최초 설정 명령이다. Go crypto/rand로 32바이트를 생성해 배타적 생성·fsync로 위 파일에만 기록하고 기존 키는 덮지 않는다. 값은 stdout·로그·argv·일반 unit 본문·신원 백업에 넣지 않는다. 이 명령 구현과 가상 테스트만 수행하며 실제 생성은 하지 않는다.
- `agent/enroll.go`·`agent/run.go`·이행 명령은 공통 loader로 `--identity-key-file`의 한 항목을 엄격 파싱하거나 `AGENT_IDENTITY_ENC_KEY` 환경 입력을 읽는다. 두 공급원이 동시에 있으면 오류로 처리하고 키를 읽은 직후 환경변수를 제거한다. 기존 deploy.go의 자식 프로세스 환경 허용 목록을 유지하며 원문 키가 자식·진단에 남지 않는지 검증한다.
- `scripts/install-agent.sh`는 명시적 키 파일·핀 파일 경로를 받고 키 파일을 생성/복사하지 않는다. root 소유·권한·분리 조건을 검사한 뒤 `EnvironmentFile=/etc/autocert-agent/identity.env`와 공개키 핀 경로를 unit에 쓴다. systemd가 시작/재시작마다 같은 키를 다시 읽으므로 LoadCredentialEncrypted 미지원 Linux도 신규 supervisor·TPM·외부 서비스 없이 기존 systemd 설치 범위에서 지원한다.
- 최초 enroll은 같은 파일을 `--identity-key-file`로 읽고 서비스는 같은 파일의 EnvironmentFile 입력을 쓴다. 공급 없음·잘못된 길이·다른 키·암호문 변조·인증서 불일치는 네트워크 접속 전에 중단한다. 키 분실·교체는 명시적 운영 복구이며 installer/run이 키를 재생성하지 않는다.
- 명시적 `agent migrate-identity`는 agent 중지·잠금 상태에서 기존 key.pem/인증서 일치를 확인하고 메모리 봉인→0600 임시 암호문→fsync→재복호화 확인→rename·디렉토리 fsync까지 완료한 뒤 기존 평문 파일을 제거한다. 평문 백업·평문 임시 파일을 새로 만들지 않는다.
- 중간 실패는 기존 원본을 보존하고 일반 run은 레거시 평문을 거부한다. 암호문과 평문이 함께 남은 중단 상태는 이행 명령이 두 키 일치를 검증한 뒤 정리한다. 이행 후 동일 agent ID/키로 재시작해야 하며 기존 디스크 잔존/백업 제거는 별도 운영 책임이다.

## 대안·위험·미결정
- 권장 Ed25519+AES-GCM은 표준 라이브러리로 충분하고 서명 신뢰와 채널 신뢰를 분리한다. HMAC은 간단하지만 agent도 서명 위조 가능한 공유 비밀을 갖게 된다. CA 키 재사용은 배선 비용만 줄이고 키 용도를 섞으므로 제외한다.
- 공급 대안 비교: root 전용 설정/EnvironmentFile은 별도 agent 키·상태와 분리된 설정을 관리하면 구형 systemd에서도 매 재시작에 공급되며 추가 인프라가 없어 이번 최소안으로 선택한다. OS LoadCredentialEncrypted는 TPM/OS credential 키로 디스크 비밀까지 보호하지만 버전·초기 credential 등록이 필요하다. 상속 FD는 메모리 전달에는 적합하나 생성·영속 보관·재공급 프로그램을 별도로 구현해야 하므로 이번 기반으로 가정하지 않는다. OS credential/FD/HSM/KMS 확장은 이 보완의 필수 구현이 아니다.
- 위협 경계: 선택안은 서버 master key의 현재 환경 공급과 동일하게 호스트 관리자·운영 비밀 설정을 신뢰한다(`docker-compose.yml:58`, `docs/manual/AGENT.md:7`). 신원 디렉토리/그 백업 단독 유출은 방어하지만 root 침해·설정과 state를 함께 담은 전체 디스크/백업 유출·동일 권한 프로세스 관측은 방어하지 않는다. 해제 키와 신원 데이터를 같은 폴더/일반 백업에 묶지 않으며 파일 권한만으로 신원 키 암호화를 대신하지 않는다. 신규 외부 공급자가 이미 존재한다고 가정하지 않는다.
- [확인 필요: 웹서버 키] nginx/Apache의 적용 파일·임시 파일·백업 파일 각각에 저장 예외를 승인할지, 암호화와 서비스별 무인 해제를 구현할지는 미결정이다. agent 신원 키는 이 예외에 포함하지 않는다. 질문 도구의 무응답은 승인 근거가 아니다.
- 네 보완은 fake deployer·생성한 가상 인증서·격리 DB로 검증할 수 있다. 실제 웹서버 배포·reload·롤백 및 B 전체 보안 완료 판정은 위 예외 또는 대체 구현이 확정될 때까지 남긴다. 실제 도메인·대상 호스트도 필요하다.
- 운영 전환은 명시적 이행·서명키 공급·agent 공개키 핀/해제 공급 준비 후 서버/agent를 함께 올리는 유지보수 절차다. 구버전 unsigned agent 호환 폴백은 두지 않는다. 실제 실행은 이번 보완 구현의 검증과 별도다.

## 최초 공급부터 재시작까지의 구현 계약
1. 서버 관리자는 기존 ISSUANCE_ENC_KEY 공급을 유지한 채 `cmd/deploy-signing-key`로 별도 signer를 초기화하고 공개키만 export한다. 봉인 파일 경로를 서버 설정에 넣는다. 가상 검증은 두 키가 독립적이고 서버 재시작 후 signer 공개키가 같으며 초기화 재실행이 기존 키를 보존하는 것이다.
2. 대상 관리자는 export된 공개키/지문을 신뢰된 관리 경로로 대조해 `/etc/autocert-agent/job-signing.pub`에 설치한다. `cmd/agent`의 pin loader가 소유권·경로를 검사한다. 가상 gateway가 다른 키로 서명하면 start/material 호출 0건이어야 한다.
3. 대상 관리자는 `agent init-identity-key --key-file /etc/autocert-agent/identity.env`를 최초 1회 실행하고 별도 비밀 백업을 관리한다. `agent/keysource.go` 테스트는 일회용 디렉토리의 임의 키로 생성 권한·중복 실행·symlink/포함경로 거부와 출력 무노출을 검증한다. 서버 마스터 키는 대상에 전달하지 않는다.
4. 대상 관리자는 `agent enroll --identity-key-file <같은 파일> --job-signing-public-key-file <핀 파일>`에 기존 server/state-dir/CA 지문/token-file 인자를 더해 등록한다. 기존 설치는 서비스 중지 후 같은 공급으로 migrate-identity를 먼저 실행한다. 가상 등록 후 신원 디렉토리에는 평문 개인키·해제 키가 없고 같은 파일로만 복호화되어야 한다.
5. 대상 관리자는 `install-agent.sh`에 바이너리·서버·같은 key-file·pin-file을 넘긴 뒤 서비스를 시작/재시작한다. 설치기는 기존 unit을 덮지 않는 규칙을 유지하고 매뉴얼은 기존 unit의 EnvironmentFile/핀 경로 수동 갱신 절차를 지정한다. 운영 명령은 docs/manual/AGENT.md, 생성/읽기는 cmd/agent·keysource, unit은 installer가 책임진다.
6. 구현자는 가상 키 파일로 새 agent 프로세스를 두 번 띄워 동일 신원 복원→가상 서명 작업 수락을 확인하고 잘못된 키·파일 누락·재전송을 거부시킨다. installer 생성 unit의 EnvironmentFile이 그 파일과 일치하고 환경 값이 출력·배포 자식에 없는지 검증한다. 일회용 Linux systemd에서는 LoadCredentialEncrypted 없이 등록→서비스 재시작을 검증하고, 미실행이면 process 검증과 구분해 미검증으로 기록한다. 실제 CA·nginx 적용은 이 검증의 전제가 아니다.

## 동일 003-B 보완의 병렬 단위표
| 담당 묶음 | 코드 소유권·의존 | 사용자 도달·성공 기준 | 검증 계획·하지 말 것 |
| --- | --- | --- | --- |
| 보안 한 구현자, 내부 순차 | 위 backend API/store/schema/agentwire/agent/deploysign/config/cmd 전체, scripts/install-agent.sh, frontend/lib/issuance.ts·settings/issuance/page.tsx, 관련 테스트·OpenAPI·docs/design/07-auto-issuance.md·manual/AGENT.md·스펙 정정 부록 | CA 설정 저장→발급 연결이 봉인값으로 동작한다. agent 등록/재시작→서명된 작업 수락·단계 감사까지 사용자 경로가 이어진다. | 가상 기존행 전진 이행/재실행/실패 rollback; DB audit 실패 강제·동시 start; payload 필드별 변조·다른 핀/agent·만료·재시작 replay; 신원 이행 단계별 실패·재기동. 실제 키/데이터 변경·실CA 발급·웹서버키 예외 결정 금지. |
| UI 최소 정리, 병렬 가능 | frontend/app/(app)/issuance/page.tsx·agents/page.tsx만 소유, 보안 계약 변경 파일과 겹치지 않음 | 상태 필터 라벨이 줄바꿈되지 않고 모바일 상태/조치 필요가 읽힌다. 에이전트 제목 아래 고정 부제는 제거되며 필요한 입력 도움말은 유지된다. | 라벨 shrink-0/whitespace-nowrap, select min-w-0, 상태 셀의 충분한 폭·배지 wrap를 최소 조정. 375/768/1440px 브라우저로 긴 도메인·긴 상태·필터 동작 확인. API·공유 UI·스타일 체계 변경 금지. |
- 보안 네 항목은 lease DTO·store tx·gateway·agent state/run·migration이 강하게 결합되어 한 구현자가 app_id→감사→서명/신원·배선 순으로 통합한다. 별도 backend 구현자 병렬 배정은 계약과 파일 충돌을 만든다. UI는 동시에 진행하고 최종 통합 뒤 같은 003-B HEAD를 검토한다.
- 부모가 최종 분해와 위임장을 발급한다. 여기서는 새 하위 ID를 만들지 않는다. 단위별 성공 조건을 위임장에 옮기고 3자 입력 분리·HEAD 일치·2/3 pass를 유지하되 미결정 요구를 완료로 세지 않는다.
- 구현자는 관련 Go package/격리 PostgreSQL/API 회귀와 frontend type/build, installer 구문 검증을 수행한다. DB 미설정 skip·fake deployer 성공은 실DB/실nginx 성공으로 보고하지 않는다. 실제 키값을 증거에 남기지 않는다.

## 검토 반성 원문과 참고
1. 무엇이 요구와 달랐나: 작업 서명, app_id 암호화, 로컬 개인키 저장 규칙, 변경 API 감사 요구가 충족되지 않았다.
2. 왜 그렇게 됐다고 보나: mTLS·파일 권한·종료 이벤트와 기존 테스트 성공을 각각 서명·암호화·전 단계 감사의 충분한 증거로 취급했다.
3. 다음 시도는 무엇을 다르게 해야 하나: 명시된 요구대로 구현·회귀 검증하고, 필요한 보안 예외는 승인된 요구 본문에 먼저 확정한 뒤 그 경계로 재검토한다.
1. 무엇이 요구와 달랐나: 에이전트 mTLS 개인키가 헌장의 저장 암호화 요구와 달리 평문 PKCS#8로 저장됐다.
2. 왜 그렇게 됐다고 보나: 파일 권한 제한을 저장 암호화와 동일한 보호로 취급했으며 해당 키의 예외 승인을 기록하지 않았다.
3. 다음 시도는 무엇을 다르게 해야 하나: 에이전트 키의 저장 암호화·키 해제 방식을 구현하거나 정확한 키 범위의 명시적 예외 승인을 확보하고 저장 및 재기동 회귀를 검증해야 한다.
- F1 — high, docs/design/security-plan-003.md:47, :54: LoadCredentialEncrypted 미지원 레거시는 “기존 secret 제공 supervisor”가 있을 때만 동작한다. 지정 입력에는 그 공급자가 있다는 근거가 없으며, 없을 때 필요한 새 운영 구성·지원 제외 영향도 제시하지 않았다. 54행의 “별도 외부 서비스 없이 기존 운영 체계”라는 설명만으로 이 의존성이 해소되지 않는다. 서버 signer에는 기존 ISSUANCE_ENC_KEY 공급 경계를 재사용하면서(33행), agent 쪽에서는 같은 신뢰 경계의 최소 공급안을 비교하지 않았다. systemd/FD 자체가 부적합하다는 판정은 아니다. agent 전용 해제 키를 기존 비밀 공급 방식으로 주입하는 최소안과 OS credential/FD안을 저장 분리·위협 경계·재시작·레거시 지원·추가 운영 요구로 비교하고, 확인되지 않은 supervisor를 필수 기반으로 확정하지 않는 것이 최소 수정이다. 서버 마스터 키를 agent에 배포하라는 뜻은 아니다.
- F2 — medium, docs/design/security-plan-003.md:46, :48, :62: 신원 해제 키는 난수 32바이트라고만 하고, 최초 생성/공급 주체, credential 저장소에 넣는 명시적 방법, 최초 enroll에서 같은 키를 받는 방법, 서비스 설치와 재시작에서 재공급되는 순서가 없다. FD는 전달 인터페이스이므로 공급 프로그램·운영 절차의 존재를 대신하지 않는다. 매뉴얼 수정 소유권만 지정하면 구현자가 공급 단계를 외부 선행 조건으로 남긴 채 “등록/재시작 경로 완료”로 보고할 수 있다. 선택한 최소 공급안으로 관리자 최초 설정→signer 초기화/export→공개키 pin→agent 키 공급/enroll→서비스 설치/재시작→서명 작업 수락의 실행 순서와 담당 파일·가상 검증 기준을 추가하면 된다. 이 절차를 설계하는 데 실제 키 생성이나 운영 실행은 필요 없다.
1. 무엇이 요구와 달랐나: 레거시 지원의 최소 키 공급안 비교와 최초 공급부터 agent 무인 재시작까지의 실행 경로가 빠졌다.
2. 왜 그렇게 됐다고 보나: OS credential/상속 FD 인터페이스를 정하면 기존 운영 체계가 해제 키를 준비하고 재공급할 것이라고 가정했다.
3. 다음 시도는 무엇을 다르게 해야 하나: 기존 서버 비밀 공급과 같은 신뢰 경계의 최소안을 비교하고 선택한 방식의 최초 설정·등록·서비스 설치·재시작 순서와 검증 책임을 명시한다.
- 표준 구현·공급 근거: [Go Ed25519](https://pkg.go.dev/crypto/ed25519), [Go AEAD/GCM](https://pkg.go.dev/crypto/cipher#NewGCM), [systemd credentials](https://systemd.io/CREDENTIALS/). 프로토콜·이행 순서는 이 프로젝트의 설계 제안이다.
