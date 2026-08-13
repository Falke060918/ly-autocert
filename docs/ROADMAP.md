# ROADMAP — 사업·기술 로드맵 종합 관리 문서
갱신일: 2026-08-14

이 문서는 사업계획·기술구현 로드맵·핵심 기능 현황·페인포인트를 **한 곳에서 지속 갱신하는 관리·조망용 문서**다. Phase 체계와 범위의 **정본은 `docs/00-spec.md` 15장**(구→신 매핑 표 포함)이며, 두 문서가 어긋나면 15장을 기준으로 읽고 이 문서를 고친다. P1 상세 범위·제외 목록은 `docs/01-plan.md`, 성공 기준·마일스톤은 `docs/02-goals.md`, 사업 근거·시장 분석은 `docs/TLS_인증서_관리_신규사업_AI_context.md`(사업기획 v2.1 — **원본 보존, 수정 금지**)에 있다. 로드맵·범위·현황이 바뀌는 커밋마다 이 문서의 해당 절과 상단 갱신일을 함께 갱신한다. 진행 중인 세부 작업 상태는 `docs/STATE.md`가 담당하므로 여기에 옮겨 적지 않는다.

---

## 1. 사업 개요

### 제품 정의
폐쇄망·레거시 환경용 **온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼**. 고객 내부망에 경량 센서를 설치하고 네트워크 스캔 정보를 교차 분석해 인증서·도메인·엔드포인트·서비스·소유자의 관계를 중앙에서 가시화하고 만료 리스크를 관리하는 **온프레미스 우선 CLM 진입 제품**이다. 장기적으로 이 인벤토리를 기반으로 ACME/CA API 기반 발급·갱신 오케스트레이션과 환경별 배포 자동화로 확장한다.

### Why Now
CA/Browser Forum Ballot SC081v3로 공개 TLS 인증서 최대 유효기간이 단계 축소된다 — 398일(~2026-03) → 200일(2026-03~) → 100일(2027-03~) → **47일(2029-03~)**. 398→47일이면 갱신·교체 작업량이 **약 8.5배** 증가하며, Excel·이메일·담당자 기억 중심 프로세스는 누락·책임소재 불명·오배포 위험이 급증한다. 운영 부담은 인증서 수가 아니라 **배포된 서버/엔드포인트 수**가 결정한다.

### 타깃 고객 (ICP)
사내 데이터센터를 운영하는 중견기업, 폐쇄망·망분리 환경, AD CS 사용 기업, Linux/Windows 혼재, Kubernetes/OpenShift 사용 조직. 내부망 인증서를 외부 SaaS CLM에 등록할 수 없는 조직이 핵심 wedge다. 사용자: 시스템 관리자 / 보안 관리자 / PKI 관리자 / 감사 담당자.

### 고객 페인포인트 (사업기획 v2.1 §3 발췌)
- **Discovery** — 인증서가 외부/내부망/비표준 포트에 분산. 같은 IP:PORT에서 SNI별 다른 인증서. WAF/LB/CDN의 TLS termination. 파일·keystore 내부 인증서는 네트워크 스캔만으로 못 찾음.
- **Inventory** — 도메인 수·인증서 수·엔드포인트 수가 다른 개념인데 Excel 하나로 혼합 관리. 동일 인증서의 다중 배포 관계가 표현되지 않음.
- **Ownership** — 인증서 담당자 ≠ 서비스 담당자. 퇴사·조직변경 시 소유권 유실.
- **Renewal** — CA 포털·이메일·Excel·ITSM 티켓을 사람이 연결. 수명 단축으로 반복량 증가.
- **Deployment** — 발급과 실제 서비스 반영은 별개 문제. 환경별 설치·reload 방식이 전부 다름.
- **Audit/Compliance** — 변경 추적 부재. 미승인 CA·약한 알고리즘·소유자 부재를 중앙에서 못 봄.

---

## 2. Phase 로드맵

정본: `docs/00-spec.md` 15장. 구 Phase 3a(센서)→P1 전제 아키텍처, 구 3b(에이전트·배포)→P3, 구 4(자동 갱신)→P2(자동 배포·검증만 P3), 구 5(AI)→AI 보조 트랙.

### Phase 1 — Discovery / Inventory / Alert (현재 범위, 구현 대부분 완료)
로그인·RBAC / 데이터 모델 / 인증서 파싱·수동 업로드 / 네트워크 TLS 스캔(단발·전 포트 스윕·정기 스캔·자동 재스캔) / 인벤토리 목록·상세 / 만료 대시보드 / 만료 알림 4채널(이메일·웹훅·시스로그·알림함) / 감사 로그(해시 체인) / 사용자 관리 / **센서(원격 수집기)** — 격리 세그먼트 커버리지가 P1의 전제라 별도 Phase가 아니라 P1 아키텍처 구성요소(구현 완료: 1회용 토큰 등록, mTLS, pull 리스, 멱등 관측 수집).
잔여: 디스커버리 화면 재구성, 설정 화면(/settings/scans), 벤더/유형 태그, 잔여 정리 — 상세는 `docs/STATE.md` "다음 할 일".

### Phase 1.5 — Ownership / Workflow / 커넥터
- 서비스 소유자 지정(인증서·서버 단위) / 알림 acknowledgement·snooze / 에스컬레이션 정책
- 정책 위반 점수(policy violation score) / 기간별 현황 리포트
- 보조 커넥터 확대 — CMDB, ITSM, Kubernetes, LB/WAF 인벤토리 import
- **WebtoB 설정 파일 read-only 파서** — `http.m` 등에서 SSL/VHOST/인증서 정보를 추출해 스캔 결과와 대조(쓰기·교체는 안 함)
- **Origin/Edge(내/외부) 인증서 이원 관리 설계** — 설계까지만. 구현 스키마는 P2 스키마 설계 직전 일괄 재론(§5)

### Phase 2 — 발급과 갱신 오케스트레이션
골격은 **내부 ACME-compatible RA/게이트웨이** — 표준 ACME 클라이언트(certbot·cert-manager)가 내부 게이트웨이에 요청하면 정책·승인·감사를 거쳐 upstream CA(공개 ACME·상용 CA API·Private CA)에 별도 주문.
- CA adapter 추상화(ACME / AD CS / Vault PKI) / 프로파일·CSR·발급 이력 / 계정·EAB·credential 관리
- 갱신 정책·스케줄러·승인 워크플로·자동 발급·재시도·알림(구 P4 흡수)
- Prometheus 메트릭 최소셋(`/metrics`)
- **자동교체 클라이언트 전략(사용자 확정 순서)**: ① WebtoB·WAF에도 certbot류 표준 클라이언트 적용을 먼저 개발·검증 → ② 어려우면 하이브리드(불가 장비는 P3 배포 adapter) → ③ 최종 확정은 P2 착수 시 재결정

### Phase 3 — 배포 자동화 / Multi-CA
발급(P2)과 배포(P3)는 다른 문제 — 전달→바인딩→reload→사후 검증→롤백까지가 이 단계.
- Linux/Windows 배포 에이전트(mTLS) / 환경별 deployment adapter(Nginx·Apache·IIS·파일·K8s Secret) / WebtoB·WAF 교체 adapter / 배포 검증·롤백 / Multi-CA 확대
- OpenTelemetry 분산 트레이스 / Helm Chart·Kubernetes 설치 경로
- **capability matrix 원칙**: 플랫폼별 지원 수준(Discover/Correlation/Issue·Renew/Deploy/Reload/Verify)을 표로 명시. **"WebtoB 자동 교체"는 MVP 약속이 아니다** — 고객 인터뷰·PoC 후 adapter 지원 수준 확정.

### 별도 트랙 (Phase 번호 없음)
- **상용화 준비 트랙**(P2 병렬, 실질 마감은 첫 유상 납품 직전) — 오프라인 라이선스: 하드웨어 지문 + Ed25519 서명 `.lic`, 오프라인 발급 도구(`backend/cmd/licensegen` 미작성). 설계는 `docs/design/03-license.md`.
- **AI 보조 트랙**(P3 이후) — 자연어 검색·위험 설명·실패 로그 요약·소유자 후보 추천. 실행 경로 금지, 보조 전용.

---

## 3. 핵심 기능 현황

### 구현 완료 (P1, v1.1.0 기준)
- 인증서 인벤토리: 파싱(PEM/DER, 원문 raw_der 보존)·fingerprint 중복 판정·위치 N개 연결·검색/필터/정렬/CSV 내보내기
- 스캔: 단발 스캔, 전 포트 백그라운드 스윕, 정기 스캔 스케줄(full/preset/registered), 자동 재스캔·교체 감지, 레이트 리미터·IPS 오탐 억제
- 센서: 등록·mTLS·pull 리스·멱등 관측 수집 (Linux/Windows)
- 위험 판정 엔진(규칙 15종, 조회 시 계산·미저장), CA 체인 트리(N계층)·영향 범위 패널, 서버(엔드포인트) 뷰
- 만료 알림 4채널 + CA 전용 임계값, 감사 로그(해시 체인·무결성 검증), 사용자 관리·관리 IP 허용 목록
- 오프라인 설치: Docker Compose·오프라인 이미지 번들(`scripts/bundle-offline.sh`)·설치 스크립트·매뉴얼 4종
- TLS 관측: 협상 버전·암호 스위트 수집, 지원 버전 탐지(옵트인)

### 진행 중 (P1 잔여)
- 디스커버리 화면 재구성(단일 폼 + 통합 활동 목록), 정기 스캔 설정 화면 + 대시보드 스캔 카드, preset 스케줄 생성·삭제 API
- 잔여 정리(상태색 하드코딩·gofmt·사이드바 접기 등) — 상세·우선순위는 `docs/STATE.md`

### 계획 (미착수)
- P1.5 전체(소유자·ack/snooze·에스컬레이션·점수·리포트·커넥터·WebtoB 파서·Origin/Edge 설계)
- P2 전체(ACME 게이트웨이·CA adapter·갱신 오케스트레이션·Prometheus), 상용화 트랙(licensegen)
- P3 전체(배포 에이전트·adapter·롤백·Multi-CA·OTel·Helm), AI 보조 트랙
- TLS 후속(목록 TLS 열·필터, cipher suite 활용 — cipher·지원버전은 소급 불가, 재스캔이 유일 경로)

---

## 4. 페인포인트 대응 · 리스크

### 사업/기술 리스크 (사업기획 v2.1 §23)
- **R1** (기술) 네트워크 스캔 coverage가 고객 기대보다 낮을 수 있음 → DNS/SNI/CMDB/K8s/LB/WAF 커넥터 + coverage report
- **R2** (제품) 단순 만료 모니터링만으로 예산 확보 곤란 → topology·ownership·blast radius·audit로 가치 확대
- **R3** (시장) Full CLM 기도입 대기업엔 wedge 약함 → 미도입/부분도입·폐쇄망·사업부 단위 공략
- **R4** (배포) 망별 센서 설치가 보안 심사로 무거울 수 있음 → no-private-key·handshake-only·서명 이미지·오프라인 배포
- **R5** (자동화) 환경별 교체 adapter 비용 폭증 위험 → capability matrix·adapter 우선순위·고객 확약 기반 통합
- **R6** (아키텍처) ACME 발급을 배포 자동화로 오해 → P2 발급과 P3 배포를 명확히 분리

### 기술 리스크·제약 (이 저장소 고유)
- **SNI 미전송 스캔** — 현 스캐너는 SNI를 보내지 않아(`discovery/scanner.go:217`) 같은 IP:PORT 뒤 virtual host별 복수 인증서를 구분 못 함. 식별자 확장은 보류(§5).
- **WAPPLES 일반화 금지** — 공개 문서만으로 API·파일 구조를 일반화하지 않는다. 고객 PoC 확인 7항목(v2.1 §11.2): ① 버전·배포 형태 ② 인증서 등록 방식 ③ 개인키 저장 위치/방식 ④ CLI/API/UI 지원 범위 ⑤ 변경 적용 시 서비스 영향 ⑥ HA 환경 인증서 동기화 ⑦ listener/VHOST-인증서 매핑 구조
- 파일·keystore·truststore 내부 인증서는 네트워크 스캔 한계 밖(P1.5 커넥터·파서로 보완), STARTTLS 수집 미지원
- cipher suite·지원 TLS 버전은 인증서 원문에서 계산 불가 — 백필 불가, 재스캔만이 경로
- 센서 enroll 토큰 프로세스 인자 노출(1회용·24h라 치명도 낮음, 근본 해결은 stdin/파일 입력), Windows 메인 서버 미지원(센서만)

---

## 5. 보류·재론 묶음 — P2 스키마 설계 직전 일괄 재론

**역방향 불가급** 스키마 결정이라 지금 정하지 않는다. 재론 시점은 **P2 스키마 설계 직전**이고 아래 3건을 **일괄로** 결정한다(개별 선행 착수 금지). 착수 전 planner에 비용 산정 위임.

1. **멀티테넌시 컬럼** — 테넌트 스키마 컬럼 + 쿼리 필터 강제 계층(옵션 B)
2. **SNI 기반 스캔 + 엔드포인트 식별자 확장** — 유일성 기준 (IP, Port) → (IP, Port, Protocol, SNI). SNI virtual host별 복수 인증서의 전제
3. **Origin/Edge 이원 관리 구현 스키마** — 설계는 P1.5, 스키마 반영은 이 시점

---

## 6. 미채택 아이디어 후보 (P1.5/P2 분해 때 재검토)

사업기획 v2.1 분석에서 나왔으나 로드맵에 싣지 않은 항목. 폐기가 아니라 후보 보관이다.

- FQDN 인벤토리 1급 개체화 (도메인 축을 인증서와 대등한 개체로)
- 정책 위반 점수·scan coverage 리포트 (점수 자체는 P1.5 채택 — coverage 리포트 확장이 후보)
- unauthorized CA 정책 (미승인 CA 발급 탐지)
- external probe (외부에서 보는 공개 엔드포인트 검증)
- GraphQL API
