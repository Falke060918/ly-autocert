# 프론트엔드 UI 규약 — INHERIT_AutoCert 고유

같은 디렉터리의 `frontend-design.md`·`fonts.md`는 **템플릿 소유**라 `scripts/template-update.sh`가
동기화 때 통째로 덮어쓴다. 이 저장소에서만 통하는 규약은 그 파일들을 고치지 말고 여기에 쌓는다
(sync는 template/main에 **존재하는** 파일만 checkout하므로 프로젝트가 새로 만든 이 파일은 살아남는다).
목업·UI 위임 프롬프트에 `frontend-design.md`·`fonts.md`와 함께 이 경로를 넘긴다.

## 상태색은 토큰 3개뿐이고 인증서 상태 전용이다

`--danger` / `--warn` / `--ok`(정의는 `frontend/app/globals.css`, 배지 변형은 `components/ui/badge.tsx`)만
쓴다. Tailwind 팔레트 색(`text-red-600`·`bg-amber-50` 같은 것)을 화면 코드에 직접 적지 않는다 —
라이트/다크 대응값이 토큰에만 있어서 하드코딩한 색은 다크 테마에서 혼자 어긋나고, 색의 뜻도
파일마다 갈린다. 상태 3색은 **인증서·엔드포인트의 상태**(만료·임박·정상, 위험 판정 심각도,
데이터 신선도)를 뜻하며 장식·강조·브랜드에 쓰지 않는다. 그 셋 어디에도 속하지 않는 값(예: 위험
판정의 `info`)은 중립 톤(`secondary`/`muted-foreground`)이다.

## 필수 입력은 색으로 알리지 않는다

라벨 끝에 `*` 마커를 붙이되 색은 상태 3색이 아니라 중립(`text-muted-foreground`)이고, 마커 자체는
`aria-hidden`으로 보조기기에서 감춘다 — 필수 여부의 정본은 컨트롤의 `required` 속성이다.
근거는 상태색 예산이다: `danger`는 "지금 조치해야 하는 인증서"를 뜻하는데 폼을 열자마자 빨간 별표가
네댓 개 흩뿌려지면 같은 화면의 만료 배지가 그 색의 뜻을 잃는다(필수 입력은 위험이 아니라 서식이다).
마커의 문자·색·접근성 속성은 `frontend/components/required-mark.tsx`의 `<RequiredMark />` 한 곳에만
둔다 — 라벨에 `*`를 직접 타이핑하지 않는다. 필수가 **아닌** 항목에는 아무 표시도 하지 않는다
(선택 항목에 "(선택)"을 붙이면 표시가 두 벌이 된다).
