# CHANGELOG

형식: `## v<버전> (YYYY-MM-DD)` + 변경 요약 + 관련 이슈 `#번호`. 최신이 위로 온다.

## 버전 체계

**Semantic Versioning(`MAJOR.MINOR.PATCH`) 하나로 제품 전체를 센다.** backend·frontend·센서 바이너리는
같은 커밋에서 함께 빌드·반입되므로 컴포넌트별 버전을 따로 두지 않는다 — 반입 번들 하나 = 버전 하나다.

- `MAJOR` — 하위 호환이 깨지는 변경(마이그레이션 되돌리기 불가, API 계약 파기, 센서 프로토콜 비호환).
- `MINOR` — 기능 추가와 하위 호환되는 변경. Phase 완료는 보통 여기에 해당한다.
- `PATCH` — 버그 수정·문서·보안 패치만.
- `0.x` 구간(현재)은 Phase 1~5 로드맵이 끝나기 전이라는 뜻이다. `1.0.0`은 Phase별 목표(docs/02-goals.md)의
  성공 기준을 충족하고 고객사 운영 반입이 끝난 시점에 붙인다.

**버전이 적힌 곳은 세 군데이고 릴리즈 시 같은 커밋에서 함께 올린다.**

| 위치 | 역할 |
| --- | --- |
| 루트 `VERSION` | 단일 진실원천. 번들 스크립트가 읽는다 |
| `docker-compose.yml`의 `${AUTOCERT_VERSION:-…}` | 이미지 태그 기본값. `VERSION`과 달라지면 `scripts/bundle-offline.sh`가 중단한다 |
| 이 파일 | 사람이 읽는 변경 이력 |

이미지 태그는 `inherit-autocert/backend:<버전>`·`inherit-autocert/frontend:<버전>`이다.
`AUTOCERT_VERSION`·`AUTOCERT_IMAGE_PREFIX` 환경변수로 덮어쓸 수 있다(사내 레지스트리 미러 등).

---

## v0.1.0 (2026-08-10)

고객사 반입이 가능한 첫 버전. Phase 1(인증서 인벤토리)과 Phase 3a(원격 수집 센서)를 담는다.

**인벤토리**
- 인증서 업로드(PEM·DER·CRT·CER)와 파싱 — 개인키 포함 파일은 전체 거부. 원문은 저장하지 않고 메타데이터만 보관 (#1, #2)
- 인증서 목록·상세·체인 조회, 검색·필터·정렬·페이지네이션, 대시보드 집계, CSV 내보내기 (#8, #9)
- 에이전트리스 Discovery 네트워크 TLS 스캔(IP/CIDR × 포트, 비동기 Job) (#6)
- 포트 자동 탐지 — 단일·범위·프리셋(`web`/`common`/`all`) 문법과 전 포트 백그라운드 스윕(레이트 제한·웨이브 분할·pause/resume/cancel)
- 만료 임박 알림 4채널 — 알림함·메일(SMTP)·웹훅·Syslog. 임계값 기본 D-90/60/30/7/1

**보안·운영**
- 사용자·역할(RBAC 6종), 세션 인증, 사용자 관리 API·화면 (#4, #5)
- 감사 로그 해시 체인과 무결성 검증 API (#7)
- 원격 수집 센서(Phase 3a) — mTLS 게이트웨이(8443), 1회용 토큰 등록, pull 방식 Job 리스
- Docker Compose 배포와 SQL 마이그레이션 자동 적용

**반입**
- 오프라인 반입 번들 절차 — `scripts/bundle-offline.sh`(개발측 생성) / `scripts/load-offline.sh`(고객사 적재).
  이미지 태그 고정, SHA-256 체크섬 대조, 매니페스트 동봉. 절차는 docs/manual/INSTALL.md 5.1절

**알려진 제약**
- CA 연결·자동 발급·자동 갱신·배포(Phase 2 이후)는 아직 없다 — 인벤토리와 알림까지가 이번 범위다
- 내부 통신은 평문이다(`sslmode=disable`, HTTP). HTTPS 종단은 리버스 프록시로 별도 구성한다
- STARTTLS 승격 포트(25·587·5432 등)는 인증서를 수집할 수 없다 — 수동 업로드로 등록한다
- 오프라인 라이선스는 설계만 확정되어 있고 제품에 포함되지 않는다
