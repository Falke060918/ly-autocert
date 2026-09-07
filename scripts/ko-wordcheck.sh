#!/usr/bin/env bash
# ko-wordcheck.sh — 한국어 굳은 직역 어휘 검사기 (읽기 전용)
#
# .claude/rules/korean-wordlist.md 의 '금지어 후보' 표를 읽고, 인자로 받은 파일에서
# 그 표현이 쓰인 자리를 찾아 **위치와 물음**을 낸다. 판단은 사람·모델이 한다.
#
# 이 스크립트는 어떤 파일도 고치지 않는다. 치환 기능은 옵션으로도 없다 —
# 후보의 대부분이 정상 용법을 함께 가지고 있어서, 자동 치환은 멀쩡한 문장을 망가뜨린다.
#
# 사용법:
#   bash scripts/ko-wordcheck.sh <파일> [<파일>...]
#   bash scripts/ko-wordcheck.sh --brief <파일>        # 한 줄 요약 형식 (훅용)
#   bash scripts/ko-wordcheck.sh --list <목록파일> <파일>
#
# 종료 코드:
#   0  걸린 것 없음 (출력 없음)
#   1  걸린 곳 있음 (stdout 에 목록)
#   2  사용법 오류 · 목록 파일 없음 · 대상 파일 없음
# 걸린 곳과 오류가 함께 나면 1 이 이긴다 — 오류는 언제나 stderr 에도 적힌다.
#
# 검사기가 못 보는 것: 인용문·코드블록 경계. 사용자 원문 인용 안의 낱말도 그대로 걸린다
# (말투 규칙은 인용을 고치지 말라고 한다). 그래서 이 도구는 지적만 하고 판단을 넘긴다.
set -u

self_dir=$(cd "$(dirname "$0")" && pwd)
list=${KO_WORDLIST:-$self_dir/../.claude/rules/korean-wordlist.md}
brief=0
targets=()

while [ $# -gt 0 ]; do
  case "$1" in
    --brief) brief=1; shift ;;
    --list)  shift; [ $# -gt 0 ] || { echo "ko-wordcheck: --list 뒤에 경로가 없다" >&2; exit 2; }
             list=$1; shift ;;
    -h|--help)
      sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    --) shift; while [ $# -gt 0 ]; do targets+=("$1"); shift; done ;;
    -*) echo "ko-wordcheck: 모르는 옵션 $1" >&2; exit 2 ;;
    *)  targets+=("$1"); shift ;;
  esac
done

[ ${#targets[@]} -gt 0 ] || { echo "사용법: ko-wordcheck.sh [--brief] <파일> [<파일>...]" >&2; exit 2; }
[ -s "$list" ] || { echo "ko-wordcheck: 목록 파일이 없거나 비었다 — $list" >&2; exit 2; }

# 목록 파일 자신은 검사하지 않는다. 표의 모든 행이 금지어를 그대로 담고 있어서
# 검사하면 반드시 자기 자신에 걸린다.
canon() { [ -e "$1" ] && printf '%s/%s\n' "$(cd "$(dirname "$1")" && pwd)" "$(basename "$1")"; }
list_canon=$(canon "$list")

found=0
err=0

for f in "${targets[@]}"; do
  if [ ! -f "$f" ]; then
    echo "ko-wordcheck: 파일이 없다 — $f" >&2
    err=1
    continue
  fi
  [ "$(canon "$f")" = "$list_canon" ] && continue

  # 파일명은 -v 로 넘기지 않는다 — awk 는 -v 값의 이스케이프를 해석해서
  # 윈도우식 역슬래시 경로(`docs\STATE.md`)가 `docsSTATE.md` 로 뭉개지고,
  # `\t` 로 시작하는 조각은 제어문자가 되어 훅 JSON 을 깨뜨린다. ENVIRON 은 해석하지 않는다.
  out=$(KO_FNAME="$f" awk -v brief="$brief" '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }

    # 긴 줄은 통째로 뱉지 않고 걸린 낱말 둘레만 잘라 보여준다.
    # 자르는 자리는 반드시 공백(ASCII) 또는 낱말 경계다 — 한글 한 글자를 반토막 내지 않기 위해서다.
    function excerpt(s, w,   p, st, en, k, cut, out) {
      p = index(s, w)
      # index/length/substr 는 셋 다 바이트 단위라 서로 어긋나지 않는다
      # (실측: length("가나다라마바사")=21, index(s,"바")=16, substr(s,16,3)="바").
      # 한글이 반토막 나지 않는 것은 이 셋의 일관성 덕이 아니라, 자르는 자리를
      # ASCII 공백과 낱말 경계로만 잡는 아래 설계 덕이다.
      if (p == 0 || length(s) <= 120) return s
      st = p - 40
      if (st < 1) { st = 1 }
      else {
        cut = 0
        for (k = st; k < p; k++) if (substr(s, k, 1) == " ") { cut = k + 1; break }
        st = (cut > 0) ? cut : p
      }
      en = p + length(w) + 60
      if (en >= length(s)) { en = length(s) }
      else {
        cut = 0
        for (k = en; k > p + length(w); k--) if (substr(s, k, 1) == " ") { cut = k - 1; break }
        en = (cut > 0) ? cut : p + length(w) - 1
      }
      out = substr(s, st, en - st + 1)
      if (st > 1) out = "… " out
      if (en < length(s)) out = out " …"
      return out
    }

    BEGIN { sec = ""; nw = 0; hits = 0; fname = ENVIRON["KO_FNAME"] }

    # 첫 파일 = 목록. 제목 줄로 구역을 가른다 (앵커 문구는 목록 파일에 명시돼 있다).
    FNR == NR {
      if (substr($0, 1, 3) == "## ") {
        if (index($0, "금지어 후보") > 0)     sec = "list"
        else if (index($0, "면제 목록") > 0)  sec = "exempt"
        else                                   sec = ""
        next
      }
      if (sec == "exempt") {
        if (match($0, /`[^`]+`/)) {
          t = substr($0, RSTART + 1, RLENGTH - 2)
          if (t != "") ex[t] = 1
        }
        next
      }
      if (sec == "list") {
        if (substr($0, 1, 1) != "|") next
        if (index($0, "---") > 0) next
        n = split($0, c, "|")
        if (n < 6) next
        w = trim(c[2])
        # 표 머리 줄 건너뛰기. w == "표현" 으로는 못 거른다 — macOS 기본 awk 는
        # UTF-8 로케일에서 순한글 문자열끼리의 == 를 전부 참으로 답해서
        # (BEGIN 블록에서 1*("표식"=="표현") 을 찍어 보면 1, LC_ALL=C 면 0)
        # 목록 18개가 통째로 버려졌다. index() 는 정상이므로 그쪽으로 건다.
        if (w == "") next
        if (index($0, "| 표현 |") == 1) next
        nw++; W[nw] = w; CTX[nw] = trim(c[3]); ALT[nw] = trim(c[4]); OK[nw] = trim(c[5])
        next
      }
      next
    }

    # 두 번째 파일 = 검사 대상
    {
      for (i = 1; i <= nw; i++) {
        if (W[i] in ex) continue
        if (index($0, W[i]) == 0) continue
        hits++
        if (brief == 1) {
          printf "%s:%d: \"%s\" → %s (정상 용법: %s)\n", fname, FNR, W[i], ALT[i], OK[i]
        } else {
          printf "%s:%d\n", fname, FNR
          printf "  표현: %s  |  대안: %s\n", W[i], ALT[i]
          printf "  의심 맥락: %s\n", CTX[i]
          printf "  정상 용법: %s\n", OK[i]
          printf "  본문: %s\n", excerpt(trim($0), W[i])
          printf "  물음: 이 자리의 \"%s\" — 정상 용법인가, 굳은 직역인가? 직역이면 대안으로 바꾸고 정상이면 그대로 둬라.\n\n", W[i]
        }
      }
    }
    END { if (hits > 0) exit 10 }
  ' "$list" "$f")
  rc=$?

  if [ "$rc" -eq 10 ]; then
    found=1
    printf '%s\n' "$out"
  elif [ "$rc" -ne 0 ]; then
    echo "ko-wordcheck: 검사 실패 — $f" >&2
    err=1
  fi
done

[ "$found" -eq 1 ] && exit 1
[ "$err" -eq 1 ] && exit 2
exit 0
