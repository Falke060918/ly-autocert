#!/usr/bin/env bash
# 병목·관측 리포트 — 세션의 시간과 토큰이 어디로 갔는지를 마크다운으로 분해한다.
# v1(claude-starter) perf-report.sh 이식 + 개선: 위임 1건당 소요·토큰 표,
# 재탈락(refails.jsonl) 집계 섹션 신설 (design/05 "관측").
#   도구별 소요 시간 상위 / 서브에이전트 스폰→완료 / 위임 1건당 소요·토큰 /
#   사용자 응답(승인 게이트) 대기 / 총 벽시계 시간 대비 분해 / 재탈락 누적.
#
# Usage: bash scripts/perf-report.sh [세션 개수] [--gap 초] [--session <세션 id>]
#   세션 개수  기본 1, 최근 세션부터 (세션당 1개 섹션)
#   --gap N    "사용자 응답 공백"으로 세는 최소 간격(초). 기본 30
#   --session  특정 세션 하나만 리포트한다(정확 일치 → 접두 일치 순). 주면 세션 개수는 무시.
#              "최근 = 방금 끝난 세션"이 병렬 세션에서는 성립하지 않기 때문에 필요하다
#              — SessionEnd 훅(learn-harvest.sh)이 자기 session_id로 이 경로를 쓴다.
#
# 데이터원 3개:
#   (1) 세션 transcript JSONL — <config>/projects/<프로젝트 slug>/<세션UUID>.jsonl
#       (필수. 경로 유도·파싱 함정은 scripts/fanout.sh 헤더 참조 — message.id
#        그룹핑, async_launched → <task-notification> 수명, isSidechain 부재)
#   (2) 훅 타임스탬프 로그 — <저장소>/.claude/perf/<세션UUID>.jsonl
#       (scripts/perf-log.sh 가 PreToolUse/PostToolUse/SubagentStart/SubagentStop
#        에서 적재. 서브에이전트 내부 도구 호출과 SubagentStop의 tok_in/tok_out
#        토큰도 여기만 잡힌다 — transcript의 사각을 보완한다.
#        없거나 비어 있으면 그 섹션만 생략하고 transcript만으로 리포트한다.)
#   (3) 재탈락 로그 — <메인 저장소>/.claude/perf/refails.jsonl
#       (perf-log.sh refail 모드가 적재. 세션 단위가 아니라 전체 누적이라
#        리포트 끝에 한 번만 집계한다. 없으면 섹션 생략.)
#
# fanout.sh 와의 관계: fanout은 병렬성 지표(cc1/cc2/cc3·직렬 손실·fan-out=1 경고)의
# A/B 판정용, 이 스크립트는 단일 세션의 병목·시간·토큰 분해용이다. 겹치는 파싱
# 지식은 fanout.sh 헤더에 있고, 여기서는 재설명하지 않는다. 둘을 합치지 않는다.
#
# 의존성: jq (훅 로그·transcript 구조 추출). 없으면 즉시 중단한다.
# Windows Git Bash 에서도 동작해야 한다 — GNU 전용 플래그를 피한다.

usage() {
  echo "Usage: bash scripts/perf-report.sh [세션 개수] [--gap 초] [--session <세션 id>]   # 기본 1, gap 30"
}

# ISO8601(UTC) -> epoch 초 변환은 각 awk 프로그램 안의 ts2s()가 한다
# (date(1) 플래그가 GNU/BSD에서 갈리므로 직접 센다 — fanout.sh와 동일).
AWK_TS='
  function ts2s(s,   y, mo, d, h, mi, se, yy, era, yoe, doy, doe, days, mp) {
    y  = substr(s, 1, 4) + 0;  mo = substr(s, 6, 2) + 0;  d  = substr(s, 9, 2) + 0
    h  = substr(s, 12, 2) + 0; mi = substr(s, 15, 2) + 0; se = substr(s, 18, 2) + 0
    if (y < 1970) return -1
    yy  = y - (mo <= 2)
    era = int((yy >= 0 ? yy : yy - 399) / 400)
    yoe = yy - era * 400
    mp  = (mo + 9) % 12
    doy = int((153 * mp + 2) / 5) + d - 1
    doe = yoe * 365 + int(yoe / 4) - int(yoe / 100) + doy
    days = era * 146097 + doe - 719468
    return days * 86400 + h * 3600 + mi * 60 + se
  }
  function fmt(s) {
    s = int(s); if (s < 0) s = 0
    if (s >= 3600) return sprintf("%dh %dm", s / 3600, (s % 3600) / 60)
    if (s >= 60)   return sprintf("%dm %02ds", s / 60, s % 60)
    return s "s"
  }
  function pc(part, whole) { return whole > 0 ? int(part * 100 / whole) : 0 }
'

# transcript 1줄 JSON -> "ts kind name id meta" TSV.
# 구조가 다른 줄(queue-operation 등)은 try/catch 로 조용히 버린다.
JQ_EVENTS='
  def txt: if type == "string" then . else tostring end;
  try (
    .timestamp as $ts | select($ts != null)
    # async 판정은 줄 전체로 한다 — status 필드가 content 밖(toolUseResult)에
    # 있는 버전이 있다.
    | (tostring | test("async_launched")) as $async
    | if .type == "assistant" then
        ((.message.content // [])[]
         | select(.type? == "tool_use")
         | [$ts, "use", .name, .id,
            ((.input.subagent_type // "") as $st | ((.input.description // "") | txt) as $de
             | (if $st != "" and $de != "" then $st + ": " + $de
                elif $st != "" then $st else $de end) | .[0:70])])
      elif .type == "user" then
        ((.message.content // "")
         | if type == "array" then .[] else {type: "text", text: txt} end
         | if .type? == "tool_result" then
             [$ts, "res", "", (.tool_use_id // ""),
              (if $async then "async" else "" end)]
           elif .type? == "text"
                and (((.text // "") | txt)
                     | test("<task-notification>|<tool-use-id>|<command-name>|<local-command|<system-reminder>")
                     | not) then
             [$ts, "utext", "", "", ""]
           else empty end)
      else empty end
    | @tsv
  ) catch empty
'

# 훅 로그 1줄 JSON -> "ts event tool agent tok_in tok_out aid" TSV.
# 토큰 필드는 SubagentStop 일부 줄에만 있다 — 없으면 빈 칸(하위 호환: v1 로그도 읽힌다).
# aid(서브에이전트 개체 고유값)도 새 로그에만 있다 — 빈 칸이면 유형별 선입선출로 물러난다.
JQ_HOOK='try ([.ts // "", .event // "", .tool // "", .agent // "",
               (.tok_in // "" | tostring), (.tok_out // "" | tostring),
               (.aid // "")] | @tsv) catch empty'

# 재탈락 로그 1줄 JSON -> "ts unit cause" TSV.
JQ_REFAIL='try (select(.event == "refail") | [.ts // "", .unit // "", .cause // ""] | @tsv) catch empty'

TAB=$(printf '\t')

# jsonl 을 줄 단위로 읽는다 — 한 줄이 깨져도 그 줄만 버리고 파일 끝까지 간다.
# jq 기본 모드는 스트림 파서라 깨진 줄에서 rc=5 로 멈추는데, 그러면 그 줄부터
# 파일 끝까지가 통째로 사라지고 2>/dev/null 이 그 사실마저 삼켜 리포트는
# "이벤트 없음"으로 조용히 보고했다. -R(줄을 문자열로) + fromjson 으로 줄마다
# 따로 파싱하고, 못 읽은 줄은 세어 stderr 로 알린다 — 조용한 유실 대신 시끄러운 경고.
# 전제: 여기 넘기는 필터는 언제나 @tsv 로 2개 이상 필드를 낸다. 출력에 탭이 최소
#   하나 들어가야 아래 awk의 `$0 == mark`(줄 전체 비교)가 표식과 같아질 수 없다.
#   맨 스칼라를 내는 필터를 붙이면 표식과 같은 값의 정상 줄이 깨진 줄로 재분류된다.
BROKEN_MARK='__PERF_BROKEN_LINE__'
# 파일 경로는 `-v src=` 가 아니라 환경(PERF_SRC)으로 넘긴다 — awk -v 는 값 안의
#   이스케이프를 해석해서, 역슬래시 경로(C:\Users\...)를 주면 정상 파일인데도
#   gawk 경고가 쏟아지고 경고 문면의 경로까지 뭉개진다 (.claude/rules/shell-traps.md).
jq_lines() { # $1 = jsonl 파일, $2 = 줄 1개(객체)에 적용할 jq 필터
  jq -R -r --arg mark "$BROKEN_MARK" \
    'if (gsub("[ \t\r]"; "") | length) == 0 then empty
     else ([fromjson?] | if length == 0 then $mark else (.[0] | ('"$2"')) end) end' \
    < "$1" 2>/dev/null \
    | PERF_SRC="$1" awk -v mark="$BROKEN_MARK" '
        $0 == mark { b++; next }
        { print }
        END { if (b > 0)
                printf "경고: %s — JSON으로 못 읽은 줄 %d개를 건너뛰었다(그 줄만 빠지고 나머지는 집계했다).\n", \
                  ENVIRON["PERF_SRC"], b > "/dev/stderr" }'
  local rc=${PIPESTATUS[0]}
  [ "$rc" = 0 ] || printf '경고: %s — jq가 rc=%s로 끝났다. 이 파일의 집계가 불완전할 수 있다.\n' "$1" "$rc" >&2
  return 0
}

# <task-notification> 통지 줄 -> "ts note '' toolu ''" TSV. 같은 통지가 두 번
# 남고 재개 에이전트는 여러 번 통지되지만, 최솟값 선택은 아래 awk가 한다.
notes_of() {
  awk '
    index($0, "<tool-use-id>toolu_") {
      ts = ""; id = ""
      if (match($0, /"timestamp":"[0-9][0-9-]*T[0-9][0-9:]*/))
        ts = substr($0, RSTART + 13, RLENGTH - 13)
      if (match($0, /<tool-use-id>toolu_[A-Za-z0-9_-]+/))
        id = substr($0, RSTART + 13, RLENGTH - 13)
      if (ts != "" && id != "") printf "%s\tnote\t\t%s\t\n", ts, id
    }' "$1"
}

report_transcript() { # $1 = transcript 파일, $2 = 세션 id
  # jq는 파일 인자 대신 stdin 리다이렉트로 읽는다 — Windows jq.exe는 260자
  # 초과 경로를 파일 인자로 못 연다(bash가 열면 문제없다). jq_lines 안에서 그렇게 연다.
  { jq_lines "$1" "$JQ_EVENTS"; notes_of "$1"; } \
    | sort -s -t "$TAB" -k1,1 \
    | awk -F '\t' -v gapmin="$gap" -v sid="$2" "$AWK_TS"'
    { t = ts2s($1); if (t < 0) next
      if (first == 0) { first = t; firstiso = substr($1, 1, 19) }
      if (t >= last)  { last = t;  lastiso  = substr($1, 1, 19) } }
    $2 == "use" {
      id = $4; st[id] = t; nm[id] = $3; mt[id] = $5; siso[id] = $1
      ord[++nu] = id; lastts = t; next
    }
    $2 == "res" {
      id = $4
      if (id in st) {
        if ($5 == "async") { st[id] = t; siso[id] = $1; pend[id] = 1 }
        else if (!(id in pend) && !(id in en)) en[id] = t
      }
      lastts = t; next
    }
    $2 == "note" {
      id = $4
      if ((id in st) && (!(id in en) || t < en[id])) en[id] = t
      lastts = t; next
    }
    $2 == "utext" {
      if (lastts > 0) {
        g = t - lastts
        if (g >= gapmin) { uw += g; ng++; if (g > maxg) maxg = g }
      }
      lastts = t; next
    }
    { lastts = t }

    function isort(a, b, cnt2,   i, j, ta, tb) {
      for (i = 2; i <= cnt2; i++) {
        ta = a[i]; tb = b[i]; j = i - 1
        while (j >= 1 && (a[j] > ta || (a[j] == ta && b[j] > tb))) {
          a[j + 1] = a[j]; b[j + 1] = b[j]; j--
        }
        a[j + 1] = ta; b[j + 1] = tb
      }
    }
    function md(s) { gsub(/\|/, "/", s); gsub(/\\[tn]/, " ", s); return s }

    END {
      if (first == 0) { printf "## 세션 %s — 이벤트 없음\n", substr(sid, 1, 8); exit }
      wall = last - first
      tn = 0; na = 0; m = 0; adrop = 0; asktot = 0; askn = 0
      for (i = 1; i <= nu; i++) {
        id = ord[i]
        if (!(id in en) || st[id] < 0 || en[id] < st[id]) {
          if (nm[id] == "Agent") adrop++
          continue
        }
        dur = en[id] - st[id]
        if (nm[id] == "Agent") {
          na++; A_m[na] = mt[id]; A_d[na] = dur; A_t[na] = substr(siso[id], 12, 8)
        } else {
          name = nm[id]
          if (!(name in cnt)) { tn++; TN[tn] = name }
          cnt[name]++; tot[name] += dur; if (dur > mx[name]) mx[name] = dur
        }
        if (nm[id] == "AskUserQuestion") { asktot += dur; askn++ }
        else { m++; S[m] = st[id]; E[m] = en[id] }   # busy 구간(Agent 포함, Ask 제외)
      }

      # -- 벽시계 분해: busy = 도구·에이전트 구간 합집합(스윕, fanout.sh와 동일 기법)
      ne = 0
      for (i = 1; i <= m; i++) {
        ne++; ET[ne] = S[i]; ED[ne] = 1
        ne++; ET[ne] = E[i]; ED[ne] = -1
      }
      isort(ET, ED, ne)
      cur = 0; busy = 0
      for (i = 1; i <= ne; i++) {
        if (i > 1 && cur > 0) busy += ET[i] - ET[i - 1]
        cur += ED[i]
      }
      uwait = uw + asktot
      other = wall - busy - uwait; if (other < 0) other = 0

      printf "## 세션 %s — %s → %s UTC (벽시계 %s)\n\n", substr(sid, 1, 8), firstiso, lastiso, fmt(wall)
      print "| 구간 | 시간 | 비율 |"
      print "| --- | ---: | ---: |"
      printf "| 도구·서브에이전트 실행(겹침 제거) | %s | %d%% |\n", fmt(busy), pc(busy, wall)
      printf "| 사용자 대기 — 승인 게이트 %d회(%s) + %d초 이상 응답 공백 %d회(%s) | %s | %d%% |\n", \
        askn, fmt(asktot), gapmin, ng, fmt(uw), fmt(uwait), pc(uwait, wall)
      printf "| 그 외(모델 추론·오버헤드 등) | %s | %d%% |\n", fmt(other), pc(other, wall)
      if (maxg > 0) printf "\n최장 응답 공백: %s\n", fmt(maxg)

      print ""
      print "### 도구별 소요 시간 상위 (transcript 기준, 메인 스레드만)"
      if (tn == 0) print "_완료된 도구 호출 없음_"
      else {
        for (i = 1; i <= tn; i++) { K1[i] = -tot[TN[i]]; K2[i] = i }
        isort(K1, K2, tn)
        print "| 도구 | 호출 | 합계 | 최장 |"
        print "| --- | ---: | ---: | ---: |"
        top = tn > 10 ? 10 : tn
        for (i = 1; i <= top; i++) {
          name = TN[K2[i]]
          printf "| %s | %d | %s | %s |\n", md(name), cnt[name], fmt(tot[name]), fmt(mx[name])
        }
      }

      print ""
      print "### 서브에이전트 스폰 → 완료 (transcript 기준)"
      if (na == 0 && adrop == 0) print "_스폰 없음_"
      else {
        print "| # | 위임 | 시작(UTC) | 소요 |"
        print "| ---: | --- | --- | ---: |"
        for (i = 1; i <= na; i++)
          printf "| %d | %s | %s | %s |\n", i, md(A_m[i]), A_t[i], fmt(A_d[i])
        if (adrop > 0) printf "\n완료 통지가 없어 제외한 스폰: %d\n", adrop
      }
    }'
}

report_hooklog() { # $1 = 훅 로그 파일
  echo
  echo "### 훅 로그 기반 도구 소요 (서브에이전트 내부 호출 포함)"
  jq_lines "$1" "$JQ_HOOK" | awk -F '\t' "$AWK_TS"'
    { t = ts2s($1); if (t < 0) next }
    $2 == "PreToolUse"  { qt[$3]++; q[$3 SUBSEP qt[$3]] = t; next }
    $2 == "PostToolUse" {
      if (qh[$3] + 0 < qt[$3] + 0) {
        qh[$3]++
        d = t - q[$3 SUBSEP qh[$3]]
        if (d >= 0) {
          if (!($3 in c)) { n++; N[n] = $3 }
          c[$3]++; s[$3] += d; if (d > mx[$3]) mx[$3] = d
        }
      }
      next
    }
    # -- 위임 1건당: SubagentStart -> SubagentStop 를 짝짓고, SubagentStop 줄의
    #    tok_in/tok_out(perf-log.sh가 완료 통지에서 적재)을 싣는다. 짝짓기는 aid(개체
    #    고유값)가 양쪽에 있으면 완전 일치, 없으면 예전대로 agent 유형별 선입선출이다.
    #    Start 훅이 미설정인 로그(v1 포함)에서는 소요가 "-"로 남고 토큰만 나온다.
    $2 == "SubagentStart" {
      ag = ($4 == "" ? "?" : $4)
      sq[ag]++; sqt[ag SUBSEP sq[ag]] = t
      if ($7 != "") { sat[$7] = t; sai[$7] = ag SUBSEP sq[ag] }
      next
    }
    # 유형이 빈 SubagentStop 은 위임 종료가 아니라 서브에이전트 생존 중의 주기 발화다
    # — 세면 위임 건수가 통째로 부풀어 오른다(한 세션 311건 중 272건이 이것이었다).
    # 새 로그는 perf-log.sh 가 event 를 SubagentStopNoAgent 로 갈라 적어 여기 오지도 않고,
    # 옛 로그는 이 줄이 걸러낸다.
    $2 == "SubagentStop" && $4 == "" { next }
    $2 == "SubagentStop"  {
      ag = $4
      dur = -1
      if ($7 != "" && ($7 in sat)) {
        dur = t - sat[$7]                      # aid 완전 일치 — 겹쳐 돌아도 확정된다
        used[sai[$7]] = 1                      # 선입선출 자리도 소비 표시(혼합 로그 대비)
        delete sat[$7]
      } else {
        while (sh[ag] + 0 < sq[ag] + 0 && ((ag SUBSEP (sh[ag] + 1)) in used)) sh[ag]++
        if (sh[ag] + 0 < sq[ag] + 0) {
          sh[ag]++
          dur = t - sqt[ag SUBSEP sh[ag]]
        }
      }
      if (dur < 0) dur = -1
      ns++; D_a[ns] = ag; D_d[ns] = dur; D_i[ns] = $5 + 0; D_o[ns] = $6 + 0
      ti += $5 + 0; to += $6 + 0
      next
    }
    function isort(a, b, cnt2,   i, j, ta, tb) {
      for (i = 2; i <= cnt2; i++) {
        ta = a[i]; tb = b[i]; j = i - 1
        while (j >= 1 && (a[j] > ta || (a[j] == ta && b[j] > tb))) {
          a[j + 1] = a[j]; b[j + 1] = b[j]; j--
        }
        a[j + 1] = ta; b[j + 1] = tb
      }
    }
    function tk(v) { return v > 0 ? sprintf("%d", v) : "-" }
    END {
      if (n == 0) print "_짝지어진 Pre/Post 훅 이벤트 없음_"
      else {
        for (i = 1; i <= n; i++) { K1[i] = -s[N[i]]; K2[i] = i }
        isort(K1, K2, n)
        print "| 도구 | 호출 | 합계 | 최장 |"
        print "| --- | ---: | ---: | ---: |"
        top = n > 10 ? 10 : n
        for (i = 1; i <= top; i++) {
          name = N[K2[i]]
          printf "| %s | %d | %s | %s |\n", name, c[name], fmt(s[name]), fmt(mx[name])
        }
        print ""
        print "주의: Pre/Post 짝짓기는 도구명별 선입선출 근사라, 같은 도구가 병렬로 겹치면 개별 값이 섞일 수 있다(합계는 유효)."
      }
      print ""
      print "### 위임 1건당 소요·토큰 (훅 로그 기준)"
      if (ns == 0) print "_SubagentStop 이벤트 없음_"
      else {
        print "| # | agent | 소요 | tok_in | tok_out |"
        print "| ---: | --- | ---: | ---: | ---: |"
        for (i = 1; i <= ns; i++)
          printf "| %d | %s | %s | %s | %s |\n", i, D_a[i], \
            (D_d[i] < 0 ? "-" : fmt(D_d[i])), tk(D_i[i]), tk(D_o[i])
        printf "\n위임 %d건 · 서브에이전트 토큰 합계 in %s / out %s", ns, tk(ti), tk(to)
        print " — tok_in은 그 위임의 최종 컨텍스트 크기(캐시 포함), tok_out은 생성량 합이다."
        print "소요 \"-\"는 SubagentStart 훅 미적재(v1 로그 등), 토큰 \"-\"는 완료 통지에서 토큰을 못 구한 건이다."
      }
    }'
}

report_refails() { # $1 = refails.jsonl
  echo "## 재탈락 기록 (같은 지적으로 다시 탈락한 재위임 — 전체 누적)"
  jq_lines "$1" "$JQ_REFAIL" | awk -F '\t' '
    { n++; printf "- %s · %s — %s\n", substr($1, 1, 10), ($2 == "" ? "?" : $2), $3 }
    END {
      if (n == 0) print "_형식에 맞는 기록 없음_"
      else printf "\n합계 %d건 — 이 수가 줄지 않으면 반성 3줄 형식(verdict-protocol)을 의심한다 (design/02).\n", n
    }'
  echo
}

main() {
  n=1; gap=30; got_n=0; want_sid=
  while [ $# -gt 0 ]; do
    case "$1" in
      -h|--help) usage; exit 0 ;;
      --gap) shift; gap=${1:-} ;;
      --session) shift; want_sid=${1:-} ;;
      *)
        if [ "$got_n" = 0 ]; then n=$1; got_n=1
        else echo "중단: 알 수 없는 인자 '$1'." >&2; usage >&2; exit 1
        fi ;;
    esac
    shift
  done
  case "$n" in
    ''|*[!0-9]*|0) echo "중단: 세션 개수는 1 이상의 정수여야 한다 (받은 값: '$n')." >&2; usage >&2; exit 1 ;;
  esac
  case "$gap" in
    ''|*[!0-9]*) echo "중단: --gap 은 0 이상의 정수(초)여야 한다 (받은 값: '$gap')." >&2; usage >&2; exit 1 ;;
  esac
  # 파일명 조각으로 쓰이므로 경로 문자를 배제한다(세션 id는 영숫자와 '-'뿐이다).
  case "$want_sid" in
    '') : ;;
    *[!A-Za-z0-9-]*) echo "중단: --session 은 영숫자와 '-'만 쓴다 (받은 값: '$want_sid')." >&2; usage >&2; exit 1 ;;
  esac

  command -v jq >/dev/null 2>&1 || {
    echo "중단: jq가 필요하다 — winget install jqlang.jq [win] / apt install jq [unix]" >&2
    exit 1
  }

  # -- 트랜스크립트 디렉터리 유도(fanout.sh와 동일 — 근거는 그쪽 주석 참조). ---
  root=${CLAUDE_PROJECT_DIR:-}
  [ -n "$root" ] || root=$(git rev-parse --show-toplevel 2>/dev/null)
  [ -n "$root" ] || root=$PWD

  # 재탈락 로그는 메인 저장소에 쌓인다(refail 모드가 common dir 앵커로 쓴다) —
  # 워크트리에서 돌아도 같은 파일을 보도록 여기도 같은 앵커로 찾는다.
  croot=$(git rev-parse --git-common-dir 2>/dev/null)
  croot=${croot%.git}; croot=${croot%/}
  croot=${croot:-$root}

  base=${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects
  [ -d "$base" ] || {
    echo "중단: 트랜스크립트 디렉터리가 없다 — $base" >&2
    exit 1
  }

  slug=$(printf '%s' "$root" | tr '/\\:' '-')
  dir=$base/$slug
  if [ ! -d "$dir" ]; then
    want=$(printf '%s' "$slug" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '-')
    for c in "$base"/*/; do
      [ -d "$c" ] || continue
      cn=${c%/}; cn=${cn##*/}
      have=$(printf '%s' "$cn" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '-')
      if [ "$have" = "$want" ]; then dir=$base/$cn; break; fi
    done
  fi
  [ -d "$dir" ] || {
    echo "중단: 이 저장소의 트랜스크립트를 찾지 못했다 — $base/$slug" >&2
    echo "  이 프로젝트 디렉터리에서 Claude Code 세션을 연 적이 없으면 정상이다." >&2
    exit 1
  }

  if [ -n "$want_sid" ]; then
    # 지정 세션: 정확 일치 우선, 없으면 접두 일치 중 가장 최근 것.
    if [ -f "$dir/$want_sid.jsonl" ]; then
      files=$want_sid.jsonl
    else
      # shellcheck disable=SC2012
      files=$(cd "$dir" && ls -t -- "$want_sid"*.jsonl 2>/dev/null | head -n 1)
    fi
    [ -n "$files" ] || {
      echo "중단: 지정한 세션의 트랜스크립트가 없다 — $dir/$want_sid.jsonl" >&2
      exit 1
    }
    hdr="세션 ${want_sid}"
  else
    # shellcheck disable=SC2012
    files=$(cd "$dir" && ls -t -- *.jsonl 2>/dev/null | head -n "$n")
    [ -n "$files" ] || {
      echo "중단: 세션 파일(*.jsonl)이 없다 — $dir" >&2
      exit 1
    }
    hdr="최근 ${n}개 세션"
  fi

  echo "# 세션 병목·관측 리포트 · ${hdr} · gap=${gap}s"
  echo
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    sid=${f%.jsonl}
    report_transcript "$dir/$f" "$sid"
    hooklog=$root/.claude/perf/$sid.jsonl
    if [ -s "$hooklog" ]; then
      report_hooklog "$hooklog"
    else
      echo
      echo "_훅 로그 없음(.claude/perf/${sid}.jsonl) — transcript만으로 분석했다. 훅 수집기는 .claude/settings.json 반영 후 Claude 재시작부터 쌓인다._"
    fi
    echo
  done <<EOF
$files
EOF
  refails=$croot/.claude/perf/refails.jsonl
  [ -s "$refails" ] && report_refails "$refails"
  echo "---"
  echo "구간은 서로 겹칠 수 있어(도구 실행 중 사용자 공백 등) 합이 벽시계와 정확히 일치하지 않을 수 있다."
  echo "서브에이전트 내부 소요·토큰은 transcript에 남지 않는다 — 훅 로그 섹션이 그 사각을 보완한다."
}

main "$@"
