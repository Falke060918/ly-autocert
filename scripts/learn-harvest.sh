#!/usr/bin/env bash
# learn-harvest.sh — 세션 종료(SessionEnd 훅) 후 백그라운드 학습 후보 회수 (01-c)
#
# 역할: 세션 대화 기록에서 "규칙감"·"스킬감" 후보를 싼 모델의 headless
#   `claude -p`로 추출해 docs/learn/ 수신함(로컬 전용)에 적재한다.
#   hermes background_review의 축소판 — 상시 데몬이 아니라 종료 시 1회 실행.
# 원칙: best-effort. claude CLI가 없거나, 파싱이 실패하거나, 무엇이 잘못돼도
#   조용히 0으로 종료한다. 세션 종료를 절대 막지 않는다 — 실제 추출은
#   detach된 백그라운드 서브셸에서 돌고 훅 자체는 즉시 반환한다.
# 반영은 하지 않는다: 후보는 수신함에 쌓일 뿐, 확정은 /learn 스킬의
#   사람 승인 관문에서만 일어난다 (자동 반영 금지 — 01 문서).
#
# 입력: stdin으로 SessionEnd 훅 JSON
#   ({"session_id":..., "transcript_path":..., "reason":...}). 쓰는 건 앞 둘뿐이다.
# 훅 등록은 별도 단위(settings.json)가 한다 — 이 파일은 스크립트만 담당.

set -u

# ── 가드: 어떤 실패도 밖으로 내보내지 않는다 ─────────────────────────────
quiet_exit() { exit 0; }
trap quiet_exit ERR INT TERM

# ── 재귀 차단 (필수) ────────────────────────────────────────────────────
# 이 스크립트가 띄우는 `claude -p` 자식 세션도 끝날 때 SessionEnd 훅을 발화시킨다.
# 가드가 없으면 자식이 손자를 낳는 무한 연쇄가 되어 토큰을 계속 태운다
# (2026-08-24 실측: 6초당 1세션 × 세션당 캐시쓰기 ~220k 토큰).
# 1) 환경변수: 자식 세션의 훅은 이 변수를 물려받아 즉시 종료한다.
[ -n "${LEARN_HARVEST_CHILD:-}" ] && exit 0
# 2) 쿨다운은 아래 perf 갈래 뒤에 둔다 — 쿨다운의 목적은 `claude -p` 상한이고
#    perf 갈래는 세션을 스폰하지 않으므로, 앞에 두면 락이 신선할 때 리포트만 유실된다.

# ── 훅 입력 파싱: transcript_path / session_id ──────────────────────────
HOOK_INPUT="$(cat 2>/dev/null || true)"

json_field() {
  # $1: 필드명. jq → python3 순으로 시도, 둘 다 없으면 빈 문자열.
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$HOOK_INPUT" | jq -r --arg k "$1" '.[$k] // empty' 2>/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    printf '%s' "$HOOK_INPUT" | python3 -c \
      'import json,sys; d=json.load(sys.stdin); print(d.get(sys.argv[1],""))' \
      "$1" 2>/dev/null
  fi
}

TRANSCRIPT="$(json_field transcript_path)"
SESSION_ID="$(json_field session_id)"

# ── 저장소 루트 ─────────────────────────────────────────────────────────
ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd)"

# ── perf 요약 적재 — 학습 후보 추출과 독립된 갈래 ───────────────────────
# 세션 종료 시 병목·토큰 리포트를 파일로 남긴다. /perf 를 사람이 부르지 않으면
# 관측 데이터(.claude/perf/*.jsonl)를 아무도 보지 않기 때문이다.
# 계산은 재구현하지 않고 scripts/perf-report.sh 를 호출해 그 출력을 저장한다.
# 저장 위치는 학습 수신함과 분리한다: .claude/perf/reports/<날짜-세션>.md
#   — .claude/perf/ 는 템플릿 .gitignore 와 자체 .gitignore(*)로 이미 추적 제외다.
# 세션 지정은 --session 으로 한다. 인자 없는 `perf-report.sh 1` 은 mtime 최신 세션을
# 고르므로 병렬 세션에서는 이 세션이 아닐 수 있다(실측: 입력 893087af → 내용 6abdfdc6).
# 그래도 남는 폴백 경로 때문에 파일명은 입력 session_id가 아니라 **리포트 본문의
# `## 세션 <id>`** 에서 뽑는다 — 이름이 내용과 어긋나는 오라벨을 구조로 막는다.
# best-effort: jq 부재·데이터 없음 등 무엇이 실패하든 파일 없이 조용히 넘어간다.
(
  command -v jq >/dev/null 2>&1 || exit 0
  [ -f "$SCRIPT_DIR/perf-report.sh" ] || exit 0
  PERF_DIR="$ROOT/.claude/perf/reports"
  mkdir -p "$PERF_DIR" 2>/dev/null || exit 0
  # perf-log.sh 와 같은 자체 gitignore — 파생 프로젝트의 .gitignore 를 건드리지 않는다.
  [ -f "$ROOT/.claude/perf/.gitignore" ] || printf '*\n' > "$ROOT/.claude/perf/.gitignore" 2>/dev/null
  # 겹치지 않는 보관본 경로를 하나 잡아 빈 파일로 만들고 그 경로를 낸다. $1 = .md 앞까지의 접두.
  # 파일명 스탬프가 초 단위라 같은 초에 두 번 끝나면 이름이 겹쳐 아래 `>` 가 앞 보관본을
  # 통째로 덮어썼다. 겹치면 -2, -3… 으로 비켜 가되, 존재 검사와 생성을 나누면 동시 실행에서
  # 다시 겹치므로 noclobber(set -C)로 "없을 때만 만든다"를 원자적으로 시킨다.
  # 상한(-99)까지 전부 차 있으면 예전엔 마지막 후보 경로를 그대로 돌려줘, caller 의 `>` 가
  # 남의 보관본을 통째로 덮었다 — 겹침을 피하려고 만든 함수가 겹침을 만들던 셈이다.
  # 상한에서는 빈 문자열과 rc 1로 **포기**하고, 포기했다는 사실은 caller 가 perf_note 로
  # 남긴다. 조용히 사라지지 않게 하되 아무것도 덮지 않는다.
  perf_uniq() {
    local base="$1" path="$1.md" n=2
    until ( set -C; : > "$path" ) 2>/dev/null; do
      [ "$n" -gt 99 ] && return 1
      path="$base-$n.md"; n=$((n + 1))
    done
    printf '%s' "$path"
  }
  # 덧붙이기 전용 로그 — `>>` 라 무엇도 덮지 않으므로 상한에 닿았을 때도 안전하다.
  PERF_OVERFLOW="$PERF_DIR/collect-overflow.log"
  perf_note() {
    printf '%s %s\n' "$(date +%Y-%m-%dT%H:%M:%S)" "$1" >> "$PERF_OVERFLOW" 2>/dev/null || true
  }
  # stderr는 버리지 않고 파일로 받는다 — perf-report.sh 는 깨진 jsonl 줄을
  # stderr 경고로 알리는데, 이 갈래는 서브셸째 2>&1 로 버려져 자동 경로에서는
  # 그 경고가 아무에게도 닿지 않았다. 아래에서 리포트 끝에 덧붙인다.
  PERF_ERR="$PERF_DIR/.stderr.$$"
  PERF_ERR1="$PERF_DIR/.stderr1.$$"   # 지정 세션 호출의 stderr 보존본 (폴백이 돌 때만 쓴다)
  PERF_REPORT=""
  PERF_RC=0
  PERF_RC1=""                          # 비어 있으면 지정 세션 호출이 아예 안 돌았다는 뜻
  if [ -n "$SESSION_ID" ]; then
    PERF_REPORT="$(CLAUDE_PROJECT_DIR="$ROOT" bash "$SCRIPT_DIR/perf-report.sh" \
      --session "$SESSION_ID" 2>"$PERF_ERR")" || PERF_RC=$?
  fi
  # session_id가 없거나 그 이름의 트랜스크립트가 없을 때만 최근 1개로 폴백한다.
  # 폴백하면 stderr도 다시 받는다(> 로 덮어쓴다) — 실제로 저장되는 리포트의 경고만 남긴다.
  # 다만 둘 다 실패하면 저장되는 리포트가 없으므로, 덮어쓰기 전에 지정 세션 호출의
  # rc와 stderr를 옆으로 옮겨 보존한다. 예전엔 폴백이 같은 파일에 덮어써, 실제로 먼저
  # 터진 실패의 rc도 경고도 기록에서 사라졌다.
  if [ -z "$PERF_REPORT" ]; then
    if [ -n "$SESSION_ID" ]; then
      PERF_RC1="$PERF_RC"
      mv -f "$PERF_ERR" "$PERF_ERR1" 2>/dev/null || true
    fi
    PERF_RC=0
    PERF_REPORT="$(CLAUDE_PROJECT_DIR="$ROOT" bash "$SCRIPT_DIR/perf-report.sh" 1 \
      2>"$PERF_ERR")" || PERF_RC=$?
  fi
  PERF_STAMP="$(date +%Y%m%d-%H%M%S)"
  # 리포트가 비었다는 건 perf-report.sh 가 중단했다는 뜻이다 — 정상 종료는 항상 머리말을
  # 출력한다. 예전엔 여기서 stderr 파일을 지우고 조용히 나가, 중단 사실도 rc도 경고도
  # 아무 데도 남지 않았다(리포트가 통째로 유실되던 결함의 형제다). 보관본과 같은 자리에
  # 기록으로 남긴다 — /perf 스킬이 지난 세션을 볼 때 읽는 디렉터리다.
  if [ -z "$PERF_REPORT" ]; then
    PERF_IN_SID="$(printf '%s' "${SESSION_ID:-}" | tr -cd 'A-Za-z0-9-')"
    PERF_IN_SID="${PERF_IN_SID:0:8}"
    PERF_FAIL="$(perf_uniq "$PERF_DIR/$PERF_STAMP-${PERF_IN_SID:-unknown}-collect-failed" || true)"
    if [ -n "$PERF_FAIL" ]; then
      { printf '# perf 수집 실패 — %s\n\n' "$PERF_STAMP"
        printf '세션: %s\n' "${SESSION_ID:-unknown}"
        if [ -n "$PERF_RC1" ]; then
          printf 'rc(지정 세션): %s\nrc(폴백): %s\n\n' "$PERF_RC1" "$PERF_RC"
        else
          printf 'rc: %s\n\n' "$PERF_RC"
        fi
        printf 'scripts/perf-report.sh 가 리포트를 내지 못했다 (stdout 빈 출력).\n\n'
        printf '## 수집 경고\n\n'
        if [ -n "$PERF_RC1" ]; then
          printf -- '### 지정 세션 호출\n\n'
          if [ -s "$PERF_ERR1" ]; then
            while IFS= read -r line; do printf -- '- %s\n' "$line"; done < "$PERF_ERR1"
          else
            printf -- '- (stderr 없음)\n'
          fi
          printf '\n### 폴백 호출\n\n'
        fi
        if [ -s "$PERF_ERR" ]; then
          while IFS= read -r line; do printf -- '- %s\n' "$line"; done < "$PERF_ERR"
        else
          printf -- '- (stderr 없음)\n'
        fi
      } > "$PERF_FAIL" 2>/dev/null || true
    else
      perf_note "수집 실패 기록을 포기했다 (같은 이름의 보관본이 -99 까지 차 있다): $PERF_STAMP-${PERF_IN_SID:-unknown}-collect-failed"
    fi
    rm -f "$PERF_ERR" "$PERF_ERR1" 2>/dev/null || true
    exit 0
  fi
  PERF_SID="$(printf '%s\n' "$PERF_REPORT" \
    | awk '/^## 세션 /{ print $3; exit }' | tr -cd 'A-Za-z0-9-')"
  PERF_OUT="$(perf_uniq "$PERF_DIR/$PERF_STAMP-${PERF_SID:-unknown}" || true)"
  if [ -z "$PERF_OUT" ]; then
    perf_note "리포트 보관을 포기했다 (같은 이름의 보관본이 -99 까지 차 있다): $PERF_STAMP-${PERF_SID:-unknown}"
    rm -f "$PERF_ERR" "$PERF_ERR1" 2>/dev/null || true
    exit 0
  fi
  printf '%s\n' "$PERF_REPORT" > "$PERF_OUT" 2>/dev/null || true
  # 경고가 있었으면 보관본 끝에 절로 남긴다 — /perf 스킬이 지난 세션을 볼 때 읽는 파일이다.
  if [ -s "$PERF_ERR" ]; then
    { printf '\n## 수집 경고\n\n'
      while IFS= read -r line; do printf -- '- %s\n' "$line"; done < "$PERF_ERR"
    } >> "$PERF_OUT" 2>/dev/null || true
  fi
  rm -f "$PERF_ERR" "$PERF_ERR1" 2>/dev/null || true
) >/dev/null 2>&1 &
disown 2>/dev/null || true

# ── 쿨다운 (재귀 차단 2/2) — 여기부터가 `claude -p`를 띄울 수 있는 갈래다 ──
# 환경변수가 유실되는 경로가 있어도 분당 1회로 상한을 건다.
LOCK="${TMPDIR:-/tmp}/claude-learn-harvest.last"
if [ -f "$LOCK" ]; then
  NOW=$(date +%s); LAST=$(cat "$LOCK" 2>/dev/null || echo 0)
  case "$LAST" in (*[!0-9]*|'') LAST=0 ;; esac
  [ $((NOW - LAST)) -lt 60 ] && exit 0
fi
# 락 *기록*은 실제로 claude -p를 띄우는 직전에 한다 (아래) — 조기 종료한 실행이
# 쿨다운을 태우면 SessionEnd가 세션당 1회뿐이라 다음 세션 회수분이 통째로 유실된다.

# ── 여기서부터는 학습 후보 추출 갈래 — 훅 입력과 claude CLI가 필요하다 ──
[ -n "$HOOK_INPUT" ] || exit 0
# claude CLI 부재 시 조용히 종료 (best-effort의 최소선)
command -v claude >/dev/null 2>&1 || exit 0

# ── transcript_path 폴백 ────────────────────────────────────────────────
# SessionEnd 훅이 transcript_path를 준다는 보장이 공식 문서에 없다(Stop만 명시).
# 없으면 트랜스크립트 디렉터리에서 직접 찾는다. 우선순위 3단:
#   (1) <session_id>.jsonl 정확 일치
#   (2) <session_id>* .jsonl 접두 일치 중 가장 최근 것 (session_id가 있을 때만)
#   (3) 그것도 없을 때만 디렉터리 내 가장 최근 .jsonl
# (3)은 병렬 세션·headless 자식 트랜스크립트가 더 최신이면 엉뚱한 세션을 집을 수
# 있다 — 그 경로로 잡힌 경우 산출물 머리말에 추정임을 표시한다(TRANSCRIPT_GUESSED).
# 슬러그 규칙은 scripts/index-sessions.py 의 project_slug()와 동일하다 (영숫자 외 전부 '-').
TRANSCRIPT_GUESSED=""
if [ -z "$TRANSCRIPT" ] || [ ! -f "$TRANSCRIPT" ]; then
  PROJ_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "$ROOT" | tr -c 'A-Za-z0-9' '-')"
  TRANSCRIPT=""
  if [ -d "$PROJ_DIR" ]; then
    if [ -n "$SESSION_ID" ] && [ -f "$PROJ_DIR/$SESSION_ID.jsonl" ]; then
      TRANSCRIPT="$PROJ_DIR/$SESSION_ID.jsonl"
    else
      PREFIX_MATCH=""
      if [ -n "$SESSION_ID" ]; then
        PREFIX_MATCH="$(ls -t "$PROJ_DIR/$SESSION_ID"*.jsonl 2>/dev/null | head -1)"
      fi
      if [ -n "$PREFIX_MATCH" ]; then
        TRANSCRIPT="$PREFIX_MATCH"
      else
        TRANSCRIPT="$(ls -t "$PROJ_DIR"/*.jsonl 2>/dev/null | head -1)"
        if [ -n "$TRANSCRIPT" ]; then
          TRANSCRIPT_GUESSED=1
        fi
      fi
    fi
  fi
fi
[ -n "$TRANSCRIPT" ] && [ -f "$TRANSCRIPT" ] || exit 0

# ── 수신함 ──────────────────────────────────────────────────────────────
INBOX="$ROOT/docs/learn"
mkdir -p "$INBOX" 2>/dev/null || exit 0

MODEL="${LEARN_HARVEST_MODEL:-haiku}"   # 싼 모델 기본값, 환경변수로 교체 가능
CONTEXT_MAX=40000                       # 프롬프트에 싣는 대화 텍스트 상한(바이트, 뒷부분)
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$INBOX/${STAMP}-${SESSION_ID:0:8}.md"

# 디렉터리 최신 .jsonl로 폴백한 경우, 회수 결과가 이 세션의 것이 아닐 수 있다.
SESSION_NOTE=""
if [ -n "$TRANSCRIPT_GUESSED" ]; then
  SESSION_NOTE=" (추정 — session_id 불일치, 디렉터리 최신 트랜스크립트에서 회수)"
fi

# ── 대화 텍스트 추출 ────────────────────────────────────────────────────
# 원문 jsonl은 uuid·parentUuid·usage·툴 결과가 대부분이라 그대로 실으면 낭비다.
# user/assistant 레코드의 텍스트만 뽑는다 — 파싱은 새로 짜지 않고
# scripts/index-sessions.py 의 extract_text()/INDEXED_ROLES 를 그대로 불러 쓴다.
extract_context() {
  command -v python3 >/dev/null 2>&1 || return 1
  python3 - "$TRANSCRIPT" "$SCRIPT_DIR/index-sessions.py" "$CONTEXT_MAX" <<'PY'
import importlib.util, json, os, sys

src, mod_path, limit = sys.argv[1], sys.argv[2], int(sys.argv[3])
spec = importlib.util.spec_from_file_location("index_sessions", mod_path)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)          # __main__ 가드가 있어 CLI는 돌지 않는다

window = limit * 40                   # 메타데이터 비중을 감안한 원문 읽기 창
size = os.path.getsize(src)
with open(src, "rb") as fh:
    if size > window:
        fh.seek(size - window)
        fh.readline()                 # 잘린 첫 줄은 버린다
    raw = fh.read()

out = []
for line in raw.decode("utf-8", "replace").splitlines():
    try:
        rec = json.loads(line)
    except ValueError:
        continue
    if not isinstance(rec, dict) or rec.get("type") not in mod.INDEXED_ROLES:
        continue
    text = mod.extract_text(rec)
    if text:
        out.append("[%s] %s" % (rec.get("type"), text))

body = "\n\n".join(out).encode("utf-8")
if not body.strip():
    sys.exit(1)
# 바이너리로 쓴다 — Windows stdout(cp949) 리다이렉트가 한글을 깨뜨리므로.
sys.stdout.buffer.write(body[-limit:].decode("utf-8", "ignore").encode("utf-8"))
PY
}

# ── 락 기록: 여기서부터가 claude -p를 실제로 띄우는 경로다 ──────────────
date +%s > "$LOCK" 2>/dev/null || true

# ── 백그라운드 추출: 훅은 여기서 즉시 반환한다 ──────────────────────────
(
  # 대화 기록은 클 수 있다 — 뒤쪽 일부만 입력으로 쓴다 (비용 상한).
  CONTEXT="$(extract_context 2>/dev/null || true)"
  # 추출 실패(python3 부재·파싱 실패·빈 결과)면 원문 뒷부분으로 폴백한다.
  [ -n "$CONTEXT" ] || CONTEXT="$(tail -c "$CONTEXT_MAX" "$TRANSCRIPT" 2>/dev/null || true)"
  [ -n "$CONTEXT" ] || exit 0

  # mem0식 모순 검사의 시작점: 기존 기억을 함께 읽혀 충돌 항목을 표시시킨다.
  EXISTING=""
  [ -f "$ROOT/docs/GOTCHAS.md" ] && \
    EXISTING="$EXISTING
=== 기존 GOTCHAS ===
$(head -120 "$ROOT/docs/GOTCHAS.md" 2>/dev/null || true)"
  if [ -d "$ROOT/.claude/rules" ]; then
    EXISTING="$EXISTING
=== 기존 rules ===
$(head -60 "$ROOT/.claude/rules"/*.md 2>/dev/null || true)"
  fi

  PROMPT="너는 Claude Code 세션 종료 후 학습 후보를 회수하는 도구다.
아래 세션 대화 기록(뒷부분)에서 다음 두 종류의 후보만 추출하라.

- 규칙 후보(규칙감): 다음 세션에도 지켜야 할 행동 규칙이 될 만한 것.
  사용자 교정, 실패에서 얻은 함정(GOTCHA), 반복 지적이 여기 해당한다.
- 스킬 후보(스킬감): 반복 절차로 굳혀 재사용 절차 문서(스킬)가 될 만한 것.
  이번 세션에서 성공한 다단계 절차가 여기 해당한다.

기존 기억(아래 첨부)과 대조해 각 후보에 판정을 붙여라:
[신규] 겹치지 않음 / [갱신] 기존 항목을 새 사실로 덮어써야 함(어느 항목인지 명시)
/ [무효화] 기존 항목이 틀렸음이 확인됨(어느 항목인지 명시).

출력 형식 (마크다운, 이 형식 그대로):
## 규칙 후보
- [신규|갱신|무효화] 한 줄 요약 — 근거가 된 대화 맥락 한 줄
## 스킬 후보
- [신규|갱신] 절차 이름 — 절차 개요 한 줄 (성공 횟수 표시)

이 저장소 고유가 아닌 일반 상식, 일회성 상태(다음 주에 안 참인 것)는 제외하라.
후보가 하나도 없으면 다른 말 없이 정확히 NONE 만 출력하라.
$EXISTING

=== 세션 대화 기록 (뒷부분) ===
$CONTEXT"

  # LEARN_HARVEST_CHILD: 자식 세션의 SessionEnd 훅이 다시 이 스크립트를 돌리지 못하게 한다.
  RESULT="$(printf '%s' "$PROMPT" | LEARN_HARVEST_CHILD=1 claude -p --model "$MODEL" 2>/dev/null || true)"

  # 빈 결과·NONE은 적재하지 않는다 — 수신함에 소음을 쌓지 않는다.
  TRIMMED="$(printf '%s' "$RESULT" | tr -d '[:space:]')"
  [ -n "$TRIMMED" ] && [ "$TRIMMED" != "NONE" ] || exit 0

  {
    printf '# 학습 후보 — %s\n\n' "$STAMP"
    printf '세션: %s%s\n상태: 미승인 (반영은 /learn 스킬의 사람 승인 관문에서만)\n\n' \
      "${SESSION_ID:-unknown}" "$SESSION_NOTE"
    printf '%s\n' "$RESULT"
  } > "$OUT" 2>/dev/null || true
) >/dev/null 2>&1 &
disown 2>/dev/null || true

exit 0
