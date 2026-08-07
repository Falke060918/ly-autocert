# INHERIT_AutoCert

폐쇄망과 레거시 시스템을 운영하는 기업을 위한, 설치가 쉽고 가벼운 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼.

기업 내부의 TLS·사설 인증서는 Linux/Windows 서버, IIS, Nginx, Apache, Java KeyStore, Kubernetes Secret,
로드밸런서, 네트워크 장비에 흩어져 있다. 어디에 무엇이 설치돼 있는지 모른 채 만료 장애가 발생하고,
갱신·배포는 수작업으로 처리된다. INHERIT_AutoCert는 이 인증서들을 자동으로 발견해 하나의 인벤토리로 모으고,
만료 위험을 사전에 드러내는 것에서 출발한다.

외부 SaaS 연결 없이 단일 VM 또는 Docker Compose로 설치되는 **폐쇄망 우선** 설계다.

## 문서

| 문서 | 내용 |
|---|---|
| [docs/00-spec.md](docs/00-spec.md) | 원본 명세 — 요구사항 전문 (기술 스택 11장, 제외 목록 16장) |
| [docs/01-plan.md](docs/01-plan.md) | 기획 — 문제 정의, 대상 사용자, Phase 1 범위, 비기능 요구사항 |
| [docs/02-goals.md](docs/02-goals.md) | 최종 목표 — 측정 가능한 성공 기준 |
| [mockup/index.html](mockup/index.html) | 목업 — 화면 흐름 프로토타입 (브라우저로 열어서 확인) |

운영 관련 문서는 [docs/STATE.md](docs/STATE.md)(진행 상황), [docs/GOTCHAS.md](docs/GOTCHAS.md)(반복하면 안 되는 실수)에 있다.

## 기술 스택

`docs/00-spec.md` 11장의 고정값이다. 특별한 이유 없이 변경하지 않는다.

| 영역 | 스택 |
|---|---|
| Backend | Go, REST API, OpenAPI 3, PostgreSQL, SQL migration, 비동기 작업 큐 |
| Frontend | Next.js, React, TypeScript |
| 배포 | Docker, Docker Compose (이후 Helm Chart / Kubernetes) |
| 관측성 | OpenTelemetry, Prometheus Metrics, 구조화 JSON 로그, Trace/Request ID |
| Agent | Go 단일 바이너리 (Windows/Linux) — Phase 3 |

## 현재 범위 — Phase 1 (인증서 인벤토리)

1. 사용자 로그인 + 기본 RBAC (로컬 계정)
2. PostgreSQL 데이터 모델 + 마이그레이션
3. 인증서 파서 (PEM/DER 우선)
4. 인증서 수동 업로드 API (SHA-256 fingerprint 중복 판정)
5. 네트워크 TLS 스캔(Discovery) — 비동기 Job
6. 인벤토리 목록·상세 API + 화면
7. 만료 대시보드 (7/30/60/90일)
8. 감사 로그
9. Docker Compose 개발환경

Phase 2 이후(CA 커넥터·발급, 에이전트 배포, 자동 갱신 워크플로, AI 운영 지원)는 **이번 범위 밖**이다.
제외 항목 전체는 `docs/00-spec.md` 16장과 `docs/01-plan.md`의 "이번 범위에서 제외"에 있다.

## 설치 및 실행

> Phase 1 구현 착수 후 작성. 아직 코드가 없다.

계획된 형태는 `docker compose up` 한 번으로 API·DB·프론트엔드가 기동되는 개발환경이다.

## 개발

> 빌드·테스트 명령은 Phase 1 구현 착수 후 작성.

이 저장소는 [claude-starter](https://github.com/Falke060918/claude-starter) 템플릿 기반이며,
Claude Code 오케스트레이터 모드로 개발한다. 운영 규칙은 [CLAUDE.md](CLAUDE.md), 초기 설정 체크리스트는 [SETUP.md](SETUP.md)에 있다.

## 라이선스

[MIT](LICENSE)
