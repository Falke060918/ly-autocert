---
name: ui-ux-pro-max
description: "BM25-searchable design database (79+ visual styles, 192 palettes, 74 font pairings, 119 UX rules, chart guidance, product-type reasoning) for picking or recommending a visual style, color palette, typography mood, chart type, or UX pattern. Use when a mockup or UI needs a deliberate look-and-feel decision instead of a generic default."
---

<!--
Ported from nextlevelbuilder/ui-ux-pro-max-skill — https://github.com/nextlevelbuilder/ui-ux-pro-max-skill
Licensed under the MIT License; full terms in LICENSE.txt next to this file.
Local deviations: SKILL.md rewritten from scratch (not the upstream text); only 6 of the
upstream CSVs are bundled (styles, colors, typography, ux-guidelines, charts, ui-reasoning) —
the font/icon/stack/motion databases, the per-stack search, the design-system generator,
the CLI, and the gallery are dropped; scripts/search.py and scripts/core.py are trimmed
accordingly; upstream's free font selection is replaced by the font guard below
(this repository forces its own font stack — `.claude/rules/fonts.md`).
-->

# UI/UX Pro Max — 디자인 DB 검색

시각 결정을 감이 아니라 DB 검색으로 내린다. `data/`의 CSV 6개가 원천이고, `scripts/search.py`(BM25, stdlib만)가 검색기다.

**CSV를 통째로 읽지 마라.** `data/*.csv`를 Read 도구로 여는 것은 금지다 — 360KB가 컨텍스트를 오염시킨다. 항상 `search.py`를 호출해 검색 결과(기본 3행)만 받는다.

## Search workflow (Python 필요)

```bash
cd .claude/skills/ui-ux-pro-max/scripts
python search.py "<질의>" --domain <도메인> [--max-results 3] [--full] [--json]
```

도메인 6개와 샘플 질의:

| 도메인 | DB | 언제 | 샘플 |
| --- | --- | --- | --- |
| `style` | styles.csv (79+ 스타일) | 시각 스타일·무드 고르기 | `python search.py "glassmorphism" -d style` |
| `color` | colors.csv (192 팔레트) | 제품 유형별 시맨틱 팔레트 | `python search.py "fintech" -d color` |
| `typography` | typography.csv (74쌍) | 무드에 맞는 폰트 짝 (아래 폰트 가드 필수) | `python search.py "elegant luxury" -d typography` |
| `ux` | ux-guidelines.csv (119규칙) | 접근성·상호작용 Do/Don't | `python search.py "keyboard navigation" -d ux` |
| `chart` | charts.csv | 데이터 유형→차트 유형 매핑 | `python search.py "time series trend" -d chart` |
| `reasoning` | ui-reasoning.csv | 제품 유형→스타일·색·타이포 종합 추천 | `python search.py "saas dashboard" -d reasoning` |

- `--domain`을 빼면 질의에서 자동 감지한다(결과에 `auto-detected` 표기). 확신 있으면 명시가 낫다.
- 0건이면 "no database match" — 결과의 Closest known terms로 재시도하고, 그래도 없으면 일반 기본값으로 간다고 명시적으로 말한다. 빈 결과를 빈 값 매치로 읽지 마라.
- 새 화면 하나를 잡을 때의 기본 순서: `reasoning`(제품 유형 종합) → `style`(스타일 상세) → `color`(팔레트) → `ux`(해당 패턴 Do/Don't).

## 폰트 가드 (강제)

typography 검색 결과는 디스플레이(제목) 층에만 얹는다. 본문·UI·코드 스택은 `.claude/rules/fonts.md`의 강제값 그대로다(규칙 본문은 그 파일이 단일 원본 — 여기 복사 금지).

DB가 주는 CSS Import·Tailwind Config도 같은 한계 안에서만 쓴다 — 헤딩용 폰트 로드만 취하고, body·UI에 거는 부분은 버린다.

## Quick Reference (Python 없이 쓰는 요약)

Python이 없거나 검색이 실패할 때 이 절만으로 진행한다. DB의 상세(정확한 hex, 구현 체크리스트)는 못 대신하니, 여기서 고른 방향을 결과물에 "DB 미검색" 1줄과 함께 적는다.

**스타일 지도(카테고리 → 대표 스타일)**
- 신뢰·업무(SaaS, 대시보드, 핀테크): Minimalism/Swiss, Data-Dense Dashboard, Enterprise, Dark Mode(OLED)
- 무드·브랜드(랜딩, 포트폴리오, 패션): Glassmorphism, Aurora UI, Editorial/Magazine, Bold Typography, Brutalism
- 촉감·장난기(컨슈머, 키즈, 웰니스): Claymorphism, Soft/Rounded, Biomimetic/Organic
- 기술·미래(게임, 크립토, AI): Cyberpunk UI, 3D & Hyperrealism, Chromatic Aberration, AI-Native UI

**팔레트 요령**: 제품 유형에서 출발한다(핀테크=신뢰 blue+高대비 accent, 웰니스=저채도 green/beige, 크립토·게임=다크+네온). 시맨틱 토큰 구조(Primary/On Primary/Background/Foreground/Muted/Destructive…)로 4~6색을 이름 붙여 잡고, 본문 대비는 WCAG AA(4.5:1) 이상.

**타이포 짝 예(헤딩 + 본문 — 디스플레이 층 한정, 위 폰트 가드 적용)**
- Luxury Serif: Playfair Display + (본문은 fonts.md 스택)
- Minimal Swiss / Modern Professional: Inter·Helvetica 계열 헤딩 — 우리 스택의 Geist가 이미 이 무드다, 얹을 것 없음
- Editorial Classic: Merriweather·Lora 계열 세리프 헤딩
- Developer Mono: 모노 헤딩 무드 — 우리 스택의 "Geist Mono"로 충족
- Brutalist Raw / Bold Statement: Archivo Black·Space Grotesk 계열 헤딩

**UX 최소선**: 터치 타깃 44px+, 포커스 아웃라인 제거 금지, 모션은 200~300ms·`prefers-reduced-motion` 존중, 키보드로 전 흐름 도달 가능.

**차트 최소선**: 시간 추세=라인, 범주 비교=바, 구성비는 5조각 이하일 때만 파이/도넛, 상관=산점도. 색만으로 계열을 구분하지 않는다.

## 결과 적용 체크리스트

- [ ] 스타일·팔레트를 DB 검색으로 골랐고, 0건 폴백이면 그 사실을 결과물에 적었다
- [ ] 폰트: 본문·UI·코드 스택은 `.claude/rules/fonts.md` 강제값 그대로, DB 폰트는 디스플레이(제목) 층에만 — `fonts.md` §1의 디스플레이 예외 형식(`"Playfair Display", Pretendard, serif` 꼴) 준수
- [ ] 팔레트는 시맨틱 토큰으로 정리했고 본문 대비 AA 이상
- [ ] 해당 화면 유형의 `ux` 도메인 Do/Don't를 1회 검색해 반영했다
- [ ] 차트가 있으면 `chart` 도메인으로 유형을 확인했다
- [ ] `data/*.csv`를 통째로 읽지 않았다

폴백: Python 없으면 Quick Reference 절만으로 진행한다.
