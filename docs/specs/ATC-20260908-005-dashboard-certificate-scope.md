# ATC-20260908-005 — 대시보드 인증서 집계 범위

## 사용자 원문

> WISEKEY DEMO 로 이번에 튜링사인으로 받은 인증서가 일주일 내 만료 인증서로 대시보드에 보이는데, 이건 적용도 안된 인증서인데 보이는게 맞나? 대시보드에서 토글 만들고, 모든 관리자 보유 인증서 or 서버인증서 로 하고 서버인증서를 기본값으로 하자. 서버 인증서는 실제 서버에 적용된 인증서 중에서만. 보유 인증서는 적용할 수 있는 인증서 전부 (관리자페이지에서 관리중인)

> 지금 요청한것은 코드만 수정하고 커밋 푸시해. 리뷰하지말고. 지금 테스트 중인 서버 건들지마.

## 범위와 성공 기준

- 대시보드에 “서버 인증서”와 “모든 관리자 보유 인증서” 토글을 두고 첫 진입은 서버 인증서로 한다. 인증서 카드·만료 목록·차트·위험 집계·클릭한 목록의 범위가 일치해야 한다.
- 서버 인증서는 은퇴하지 않은 엔드포인트의 성공한 최신 TLS 관측 또는 활성 배포 대상의 검증 완료된 현재 인증서 연결로 판별한다. CA·추정 위치·미완료 배포·과거 목표는 제외하고 여러 대상에 적용한 같은 인증서는 한 번만 센다. 현재 연결이 있으면 replaced 상태만으로 제외하지 않는다.
- 관리자 보유 범위는 기존 관리 인벤토리 기본값을 유지하며 미적용 발급 인증서도 포함한다. 기존 RBAC과 읽기 권한을 유지한다. 에이전트·스캔 작업 집계는 인증서 범위로 제한하지 않는다.
- 로컬 격리 DB 회귀·프런트 타입/빌드·fixture 브라우저 토글 검사 후 이번 변경과 최소 의존성만 커밋하고 origin/main에 일반 push한다. 사용자의 직접 지시에 따라 독립 리뷰는 생략 기록으로 남긴다.

## 범위 외

테스트 서버 SSH 접속·조회·설치·재시작·배포, 실제 CA 요청, 보류 중인 004 설치본 작업, 배포 409·취소·서명키 수정, 무관 dirty 변경이나 커밋, 비밀 출력, force push, rebase, 후속 HANDOFF 자동 실행은 제외한다.

## 가정

- “관리자 보유”는 특정 owner_id의 소유물이 아니라 기존 관리 인벤토리를 뜻한다. 기존 교체 이력 제외 기본값은 유지하며 유효기간·개인키 보유 여부로 새로 제한하지 않는다.
- 실제 적용은 마지막 저장 관측/검증 기준이다. 이 작업은 테스트 서버에 접속하거나 실시간 재검사를 수행하지 않는다.
- 서버 집계 필터와 화면 토글은 함께 적용돼야 사용 가능하므로 ATC-20260908-005-A 한 단위로 처리한다. 현재 미추적 콘솔 화면과 스타일·polling 의존성은 최소한 포함하되 기존 다른 작업은 보존한다.

## 위임장 ATC-20260908-005-A

이미 승인됨 — 확인 질문 없이 완료까지. 기존설치본004작업은보류유지, 새단위대시보드코드만구현한다. 사용자확정원문: 'WISEKEY DEMO 로 이번에 튜링사인으로 받은 인증서가 일주일 내 만료 인증서로 대시보드에 보이는데, 이건 적용도 안된 인증서인데 보이는게 맞나? 대시보드에서 토글 만들고, 모든 관리자 보유 인증서 or 서버인증서 로 하고 서버인증서를 기본값으로 하자. 서버 인증서는 실제 서버에 적용된 인증서 중에서만. 보유 인증서는 적용할 수 있는 인증서 전부 (관리자페이지에서 관리중인)'. 최신제한원문 '지금 요청한것은 코드만 수정하고 커밋 푸시해. 리뷰하지말고. 지금 테스트 중인 서버 건들지마.' status unit: ATC-20260908-005-A; note: 대시보드 인증서 집계 범위 전환. 단일결합: 서버집계필터와화면토글이함께적용돼야사용가능. 이위임장을 docs/specs/ATC-20260908-005-dashboard-certificate-scope.md에원문·현재범위/범위외·성공기준·가정과그대로저장, 확인필요마커0. 조사관최종 .codex/tmp/dashboard-certificate-scope-probe/summary.md곧생기며현재확정facts: HEAD90d5c52 origin동일, 현재UI는미추적 frontend/components/console-dashboard.tsx + 수정app/(app)/page.tsx, summarySQL certificates.status<>'replaced'만적용되어설치여부무관. endpoint_monitors.current_fingerprint 최신TLS관측 및 deployment_targets.current_certificate_id(CompleteDeployJob가completed+observedfingerprint일치일때만갱신)가실적용근거. deploy_jobs.certificate_id,pending/intended, certificate_type='server', serverEndpointCTE의estimatedfallback은실적용증거아님. 성공기준: 대시보드에 '서버 인증서'/'모든 관리자 보유 인증서' 토글, 처음진입기본server. 서버범위에는실제현재관측또는검증완료된현재적용cert만·중복배제, 미적용발급/미완료배포/추정연관제외; 관리자보유는기존관리페이지의관리대상전체(미적용포함). 인증서기반카드/7일등만료목록/분포차트/위험표/해당클릭이동필터가선택범위에일관적, 비인증서agent운영집계까지억지필터금지. 실제currentbinding cert를replaced라는사유만으로가릴수있다면현재적용사실우선(관리보유의기존폐기필터와구분해테스트). 새namespace/권한노출없고 기존RBAC/tenant범위유지. 기존화면스타일·폰트재사용하며대대적UI개편금지. 최소코드/API계약/관련회귀·격리DB 및frontend타입/필요빌드·로컬fixture토글검사로확인. 사용자review생략은이번005직접지시이므로 .codex/review/ATC-20260908-005-A.skipped.md에원문/정확최종HEAD/시각/3자생략을기록하고reviewer호출대기금지. 금지: 테스트서버SSH접속/조회/설치/재시작/배포, CA실요청, 설치본빌드004재개, 과거배포409/취소/서명키코드작업, 새기능, 무관dirty변경·커밋, 비밀출력, force/rebase. 네이전baseline보존, 현재추가된상태와대조하고다른담당도있으므로타인변경되돌리지마라. 커밋은자기diff만별도index선별, 미추적console-dashboard등필수의존성이있으면이번요청최소의존성전체가commitHEAD에서빌드되게선별하고범위보고; 무조건gitadd전체금지. 커밋메시지ATC-20260908-005-A 포함, 직후gitshowstat혼입확인·남의커밋되감기금지. 사용자commitpush명시승인, 최종최소검사후제품origin/main일반push까지질문없이완료, remote충돌이면force/타인변경덮기없이보고. 작업물commit실존/main도달/원격HEAD일치·dirty보존증거도남김. 산출 .codex/tmp/dashboard-certificate-scope/summary.md 및 delivery.json, scratch .codex/tmp/dashboard-certificate-scope/scratchpad/. 로컬브라우저확인은webapp-testing사용OS temp/dashboard-certificate-scope/에artifacts, 실제서버검사로위장금지. 완료후새HANDOFF/설치본/다른작업자동착수없음, 필요한최소기록만이번커밋에묶고끝내라. Do not return full file contents; return only the list of changed files plus a key summary.
