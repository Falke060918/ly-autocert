# STATE — 마지막 갱신: 2026-09-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리 플랫폼. 명세는 docs/00-spec.md, 승인 발급·배포 범위는 docs/specs/ATC-20260907-001-auto-issuance.md와 003-product-validation.md를 따른다.

## 현재 상태
- 20260908 CA 발급 상태·비교 경고 복구(ATC-20260908-003): CA issued 사실과 결과 수집·등록을 분리하고 기간/DNS/키·체인 불일치는 등록 차단 대신 비교·경고로 보존한다. CA 원본 체인은 보정하지 않는다. A bd40b9e·08e94fc를 D 기존 소스에 최소 반영해 backend 33ddeebf / frontend a4198026 실행과 환경·키·DB·프록시 보존을 확인했다. test.com da5ac750, 이전906601da 및 동일 증상c997bbb0 3건을 기존 CA code GET으로 completed·인벤토리 연결까지 복구했다. leaf DER·원문 체인 SHA 동일, 목록/상세 경고와 당시 실패 이력·재처리 감사 유지. 직접 Go/프런트 검사·로컬 fixture Chromium과 실제 D 데이터/실행 검증을 구별하며 D 인증 로그인 화면 검증은 미실행이다. 현재 기준: docs/specs/ATC-20260908-003-ca-chain-warning.md, 실측: .codex/tmp/ca-issued-d-recovery/summary.md. 아래 데모 발급 미완료 항목은 각 당시 주문의 이력이다.
- 검사 건너뛴 단위: Codex ATC-20260908-003-A(08e94fc, 2026-09-08T05:56:00Z), 사용자 원문 "검토 패스해.". 구현자 테스트를 독립 검토 통과로 해석하지 않는다.
- 승인된 C·D와 필요한 CA 설정·agent 기반은 구현되어 있다. E 자동 갱신·재배포는 미구현이며 이번 작업에 포함하지 않는다. 릴리스 표기는 1.2.0을 유지한다.
- 003-A 알림 테스트 날짜 의존 4건은 수정 후 통과했다. 고정 테스트 시계를 사용하며 실제 메시지 생성 시계는 유지한다. 근거: 30e3db5, .codex/tmp/ATC-20260907-003-A/summary.md.
- 003-B 기존 제품 검증·커밋은 958416d에서 완료했으나 독립 검토는 사용자판 pass·위임장판 fail·스펙판 fail이었다. 기능 검증 통과를 보안 완료로 해석하지 않는다.
- B 보완: app_id·secret 봉인 및 API 설정 여부만 반환, 0027 스키마와 명시적 app_id 이행, lease/start/heartbeat 변경·감사 원자성, 독립 Ed25519 작업 서명·관리자 공개키 핀·재전송 journal, agent 신원 봉인·별도 identity.env 공급/명시적 이행을 구현했다. installer·CLI·매뉴얼로 최초 공급→등록→재시작을 연결했다. 근거: docs/design/security-plan-003.md, docs/manual/AGENT.md, .codex/tmp/security-repair-003/summary.md.
- 보완 구현자의 임시 PostgreSQL 전체 Go 검사와 프런트 테스트·lint·build는 통과했다. 독립 3자 재검토는 별도로 필요하다. 두 새 테스트 프로세스로 같은 신원 복원과 replay 거부를 확인했으며 실제 systemd·nginx 운영 실행의 증거가 아니다.
- 웹서버 적용·임시·백업 개인키 저장 예외 또는 대체 해제 방식은 미답이다. agent 신원 봉인과 분리된 결정이며 전체 B 보안 완료·실배포는 남아 있다.
- 003-C: 사용자가 제공한 데모 API 문서·계정으로 기존 Go 커넥터의 로그인 200·상품 조회 200/5개를 확인했다. API URL은 TLS Manager 데모 문서와 일치한다. 실제 주문·인증서 발급·다운로드·DNS 변경·배포는 하지 않았다. 와일드카드 DCV 정규화·중복/충돌 및 신규 organization 담당자 계약은 fa13c07에서 수정했다. 근거: .codex/tmp/live-ca-api-003/summary.md 및 implementation.md. 독립 검토 결과는 .codex/review/의 C 판정을 대조한다.
- apiinfo.txt는 로컬 비밀 입력이다. 내용·계정·토큰은 보고서와 Git에 넣지 않는다. 현재 검증은 API 로직 구현 목적이며 시험 도메인과 실제 Linux 대상은 제공되지 않았다.
- 기존 001의 mTLS가 개별 작업 서명을 대체한다는 위임은 원래 성공 기준과 달랐다. 과거 기록을 지우지 않고 001 스펙 정정 부록과 07 설계·AGENT 매뉴얼로 바로잡았다.

## 다음 할 일
1. 보안 보완 커밋을 독립 3자로 재검토하고 실제 변경 HEAD와 판정을 대조한다. 테스트 통과·문서의 다음 할 일은 새 작업 승인 근거가 아니다.
2. 웹서버 키 저장 경계를 사용자에게 확인한다. 질문 버튼이 이 세션에서 보이지 않았으므로 필수 질문을 평문 진행 안내 속에 묻히게 하지 않는다. 응답 없음은 승인이 아니다.
3. 도메인·Linux 시험 대상·운영 키 공급이 준비되고 해당 범위가 승인되면 실발급→배포→reload·롤백을 검증한다. 현재 데모 로그인 성공만으로 무료 발급·검증 생략을 추정하지 않는다.
4. 템플릿 개선과 Orca/상태표시줄 진행 문구는 사용자가 보류했다. 후보는 docs/learn/2026-09-07-template-improvements.md 한 파일의 11항목에 모았다. 알림·검사/훅·대기 간격·질문 UI 문제를 별도 승인 없이 실행기 설정 변경으로 확대하지 않는다.
5. E 자동 갱신·재배포, P1.5 9단위 분해안 및 소유자 디렉토리 공개 범위, 오프라인 라이선스, P2 멀티테넌시·SNI·Origin/Edge는 기존 보류를 유지한다. 선행 착수하지 않는다.

## 이번 세션 특이사항
- 실제 API 계정 제공 전 검증은 가짜 CA·httptest·가짜 API였으며 실제 인증서 발급 성공이 아니었다. 이후 실API 증거와 구분한다.
- 내부 collaboration agent는 실제 사용했으나 별도 Orca agent 터미널·worktree가 생성되었다고 보고하지 않는다. 글로벌 Codex status_line 설정은 적용하지 않았으며 사용자가 해당 표시 작업을 보류했다.
- 템플릿 이관의 기존 사용자 diff는 별도 보존한다. validator·hook 가정 문제와 macOS sync self-update·gitattributes 보존 문제는 개선 후보로 남았고 제품 검증으로 해결됐다고 주장하지 않는다.
- 사업 문서 docs/TLS_인증서_관리_신규사업_AI_context.md는 원본 보존한다. WebtoB·WAPPLES 고객 PoC 전제를 일반화하지 않는다.
- Notion 외부 반영은 이번 범위에 없다. 실제 운영 서비스·데이터 이행·키 생성은 실행하지 않는다.
