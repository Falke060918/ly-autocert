#!/usr/bin/env bash
# fan-out 계측 — 서브에이전트가 실제로 병렬 실행되는지를 숫자로 본다.
# 규칙(오케스트레이터 분해 의무화 등)을 바꿨을 때 A/B 판정용 지표를 낸다.
#
# Usage: bash scripts/fanout.sh [세션 개수] [--all] [--gap 초]
#   세션 개수  기본 5, 최근 세션부터
#   --all      형제 프로젝트를 한 번에 — 프로젝트당 1행으로 집계
#   --gap N    직렬 체인 판정 임계값(초). 기본 180
#
# 읽는 대상: Claude Code 세션 트랜스크립트
#   <config>/projects/<프로젝트 경로의 / \ : 를 -로 치환한 이름>/<세션UUID>.jsonl
# 경로는 절대 하드코딩하지 않는다 — HOME(또는 CLAUDE_CONFIG_DIR)과 현재 저장소
# 최상위 경로에서 런타임에 유도한다.
#
# !! 핵심 함정 1 — 병렬 폭 !!
# 트랜스크립트 JSONL은 assistant 메시지의 content block 1개당 1줄이다. 따라서
# "한 줄에 Agent 호출이 2개"인 줄은 존재하지 않고, 그런 걸 세는 grep은 항상 0을
# 준다. 병렬 폭은 반드시 message.id로 그룹핑해서 재야 한다 — 같은 message.id를
# 공유하는 "name":"Agent" tool_use 개수가 그 턴의 fan-out 폭이다.
#
# !! 핵심 함정 2 — run_in_background는 지표가 아니다 !!
# 이 버전의 Agent 툴은 플래그와 무관하게 사실상 전부 비동기다. tool_result로
# "status":"async_launched"가 돌아오고 실제 완료는 나중에 <task-notification>으로
# 온다(전 코퍼스 39세션 계측: Agent tool_result의 85%가 async_launched).
# 그래서 run_in_background 기반 "foreground 비율"은 의미가 없어 폐기했다.
# 대신 async%(실제 비동기 발사 비율)와 아래 동시 실행 지표를 본다.
#
# !! 핵심 함정 3 — 폭 1도 겹칠 수 있다 !!
# 비동기라서 fan-out 폭이 1이어도 시간상 겹칠 수 있고, 폭이 2여도 앞 에이전트를
# 기다렸다 띄웠으면 직렬이다. 진짜 지표는 "동시에 몇 개가 살아 있었는가"다.
# 수명 구간 = async_launched 시각 -> 대응하는 <task-notification> 시각
#             (동기 완료 건은 tool_use -> tool_result)
#
# 참고: isSidechain 필터는 두지 않는다. 전 코퍼스 39파일에서 true가 0건이다 —
# 서브에이전트 내부 대화는 메인 트랜스크립트에 남지 않는다.
#
# jq 의존성 없음: 트랜스크립트는 한 줄짜리 compact JSON이고, 프롬프트 문자열
# 안에 같은 키가 들어가도 그쪽은 \" 로 이스케이프되므로 아래 부분 문자열 패턴에
# 걸리지 않는다. 파싱은 awk 한 번에 끝낸다.

usage() {
  echo "Usage: bash scripts/fanout.sh [세션 개수] [--all] [--gap 초]   # 기본 5, gap 180"
}

count_file() {
  # 파일 1개 -> 한 줄:
  #   spawn msgs max async iso live c1 c2 c3 busy maxcc loss chains drop
  awk -v gap="$2" '
    # -- ISO8601(UTC) -> epoch 초. date(1) 플래그가 GNU/BSD에서 갈리므로 직접 센다.
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
    function tsof(line,   s) {
      # "timestamp":" 는 13자. 뒤 19자가 2026-08-05T02:02:34 형태다.
      if (!match(line, /"timestamp":"[0-9][0-9-]*T[0-9][0-9:]*/)) return -1
      s = substr(line, RSTART + 13, RLENGTH - 13)
      return ts2s(s)
    }
    # 정규식은 반드시 문자열로 넘긴다 — awk에서 /re/ 를 인자로 주면 정규식이
    # 아니라 $0 매칭 결과(0/1)가 전달된다.
    function grabtu(line, re,   s) {
      if (!match(line, re)) return ""
      s = substr(line, RSTART, RLENGTH)
      if (!match(s, /toolu_[A-Za-z0-9_-]+/)) return ""
      return substr(s, RSTART, RLENGTH)
    }
    # a[] 오름차순(동률이면 b[] 오름차순)으로 a,b를 함께 정렬. 구간 수가 작아
    # 삽입 정렬로 충분하고, asort는 gawk 전용이라 쓰지 않는다.
    function isort(a, b, cnt,   i, j, ta, tb) {
      for (i = 2; i <= cnt; i++) {
        ta = a[i]; tb = b[i]; j = i - 1
        while (j >= 1 && (a[j] > ta || (a[j] == ta && b[j] > tb))) {
          a[j + 1] = a[j]; b[j + 1] = b[j]; j--
        }
        a[j + 1] = ta; b[j + 1] = tb
      }
    }

    /"type":"tool_use"/ && /"name"[[:space:]]*:[[:space:]]*"Agent"/ {
      spawn++
      key = NR
      if (match($0, /"id":"msg_[A-Za-z0-9_-]+"/)) key = substr($0, RSTART, RLENGTH)
      n[key]++
      if ($0 ~ /"isolation"[[:space:]]*:/) iso++
      tu = grabtu($0, "\"id\":\"toolu_[A-Za-z0-9_-]+\"")
      if (tu != "") { seen[tu] = 1; st[tu] = tsof($0) }
      next
    }

    /"tool_use_id":"toolu_/ {
      tu = grabtu($0, "\"tool_use_id\":\"toolu_[A-Za-z0-9_-]+\"")
      if (tu == "" || !(tu in seen)) next
      if ($0 ~ /"status"[[:space:]]*:[[:space:]]*"async_launched"/) {
        async++
        st[tu] = tsof($0)   # 수명 시작 = 비동기 발사 시각
        pend[tu] = 1
        next
      }
      # 동기 완료(tool_use -> tool_result)
      if (!(tu in pend) && !(tu in en)) en[tu] = tsof($0)
      next
    }

    /<tool-use-id>toolu_/ {
      # 같은 통지가 queue-operation(발생 시각)과 user(전달 시각)로 두 번 남고,
      # 재개된 에이전트는 여러 번 통지된다. 가장 이른 시각 = 첫 완료다.
      tu = grabtu($0, "<tool-use-id>toolu_[A-Za-z0-9_-]+")
      if (tu == "" || !(tu in seen)) next
      t = tsof($0)
      if (t < 0) next
      if (!(tu in en) || t < en[tu]) en[tu] = t
    }

    END {
      msgs = 0; max = 0
      for (k in n) { msgs++; if (n[k] > max) max = n[k] }

      m = 0; drop = 0
      for (tu in seen) {
        if (!(tu in en) || st[tu] < 0 || en[tu] < 0 || en[tu] < st[tu]) { drop++; continue }
        m++; S[m] = st[tu]; E[m] = en[tu]
      }

      # -- 동시 실행 스윕. 끝 이벤트를 시작보다 먼저 처리해 접점 겹침을 배제한다.
      ne = 0
      for (i = 1; i <= m; i++) {
        ne++; ET[ne] = S[i]; ED[ne] = 1
        ne++; ET[ne] = E[i]; ED[ne] = -1
      }
      isort(ET, ED, ne)
      cur = 0; maxcc = 0; busy = 0; c1 = 0; c2 = 0; c3 = 0
      for (i = 1; i <= ne; i++) {
        if (i > 1 && cur > 0) {
          dt = ET[i] - ET[i - 1]
          busy += dt
          if (cur == 1) c1 += dt; else if (cur == 2) c2 += dt; else c3 += dt
        }
        cur += ED[i]
        if (cur > maxcc) maxcc = cur
      }

      # -- 직렬 손실: 앞이 끝난 뒤 gap초 이내에 다음이 시작하면 "기다린 체인".
      #    손실 = 체인 구간 합 - 최대 구간 (병렬이었다면 줄었을 시간).
      isort(S, E, m)
      loss = 0; chains = 0; cn = 0; csum = 0; cmax = 0; cend = -1
      for (i = 1; i <= m; i++) {
        dur = E[i] - S[i]
        if (cend >= 0 && S[i] >= cend && S[i] - cend <= gap) {
          cn++; csum += dur
          if (dur > cmax) cmax = dur
          if (E[i] > cend) cend = E[i]
        } else {
          if (cn >= 2) { loss += csum - cmax; chains++ }
          cn = 1; csum = dur; cmax = dur; cend = E[i]
        }
      }
      if (cn >= 2) { loss += csum - cmax; chains++ }

      printf "%d %d %d %d %d %d %d %d %d %d %d %d %d %d\n", \
        spawn, msgs, max, async, iso, m, c1, c2, c3, busy, maxcc, loss, chains, drop
    }
  ' "$1"
}

# 디렉터리 1개(최근 n개 세션)를 집계한다. 결과는 d_* 에 남기고 전역 a_* 에 더한다.
# 세션별 행이 필요하면 want_rows=1로 부른다.
a_spawn=0; a_msgs=0; a_max=0; a_async=0; a_iso=0
a_c1=0; a_c2=0; a_c3=0; a_busy=0; a_maxcc=0; a_loss=0; a_chains=0; a_drop=0
rows=''
scan_dir() {
  d=$1; want_rows=$2
  d_spawn=0; d_msgs=0; d_max=0; d_async=0; d_iso=0
  d_c1=0; d_c2=0; d_c3=0; d_busy=0; d_maxcc=0; d_loss=0; d_chains=0; d_drop=0
  # 파일명이 <UUID>.jsonl 뿐이라 공백/개행이 낄 수 없고, mtime 정렬은 ls -t가
  # 가장 이식성 있다(find -printf는 GNU 전용, stat 플래그는 BSD/GNU가 다르다).
  # shellcheck disable=SC2012
  files=$(cd "$d" && ls -t -- *.jsonl 2>/dev/null | head -n "$n")
  [ -n "$files" ] || return 1
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    read -r spawn msgs max async iso live c1 c2 c3 busy maxcc loss chains drop <<EOF
$(count_file "$d/$f" "$gap")
EOF
    : "$live"
    d_spawn=$((d_spawn + spawn)); d_msgs=$((d_msgs + msgs)); d_async=$((d_async + async))
    d_iso=$((d_iso + iso)); d_drop=$((d_drop + drop))
    d_c1=$((d_c1 + c1)); d_c2=$((d_c2 + c2)); d_c3=$((d_c3 + c3)); d_busy=$((d_busy + busy))
    d_loss=$((d_loss + loss)); d_chains=$((d_chains + chains))
    [ "$max" -gt "$d_max" ] && d_max=$max
    [ "$maxcc" -gt "$d_maxcc" ] && d_maxcc=$maxcc
    if [ "$want_rows" = 1 ]; then
      sid=${f%.jsonl}; sid=${sid%%-*}
      add_row "$sid" "$spawn" "$msgs" "$max" "$async" "$iso" "$c1" "$c2" "$c3" "$busy" "$maxcc" "$loss" "$drop"
    fi
  done <<EOF
$files
EOF
  a_spawn=$((a_spawn + d_spawn)); a_msgs=$((a_msgs + d_msgs)); a_async=$((a_async + d_async))
  a_iso=$((a_iso + d_iso)); a_drop=$((a_drop + d_drop))
  a_c1=$((a_c1 + d_c1)); a_c2=$((a_c2 + d_c2)); a_c3=$((a_c3 + d_c3)); a_busy=$((a_busy + d_busy))
  a_loss=$((a_loss + d_loss)); a_chains=$((a_chains + d_chains))
  [ "$d_max" -gt "$a_max" ] && a_max=$d_max
  [ "$d_maxcc" -gt "$a_maxcc" ] && a_maxcc=$d_maxcc
  return 0
}

pct() { # pct 부분 전체 -> 정수 %
  [ "$2" -gt 0 ] && echo $(( $1 * 100 / $2 )) || echo 0
}

add_row() { # 라벨 spawn msgs max async iso c1 c2 c3 busy maxcc loss drop
  _busy=${10}
  rows=$rows$(printf '%-13s %5s %4s %4s %5s %4s %5s %5s %5s %4s %6s %5s' \
    "$1" "$2" "$3" "$4" "$(pct "$5" "$2")%" "$6" \
    "$(pct "$7" "$_busy")%" "$(pct "$8" "$_busy")%" "$(pct "$9" "$_busy")%" \
    "${11}" "$((${12} / 60))m" "${13}")$'\n'
}

main() {
  n=5; gap=180; all=0; got_n=0
  while [ $# -gt 0 ]; do
    case "$1" in
      -h|--help) usage; exit 0 ;;
      --all) all=1 ;;
      --gap) shift; gap=${1:-} ;;
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

  # -- 트랜스크립트 디렉터리 유도. --------------------------------------------
  # 우선순위: CLAUDE_PROJECT_DIR -> git 최상위 -> PWD.
  # git 최상위를 PWD보다 앞에 두는 이유: Windows git-bash의 PWD는 /c/Users/...
  # (MSYS 형식)이라 치환하면 '-c-Users-...'가 나와 실제 디렉터리명과 어긋난다.
  # git rev-parse는 C:/Users/... 를 주므로 'C--Users-...'로 정확히 맞는다.
  root=${CLAUDE_PROJECT_DIR:-}
  [ -n "$root" ] || root=$(git rev-parse --show-toplevel 2>/dev/null)
  [ -n "$root" ] || root=$PWD

  base=${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects
  [ -d "$base" ] || {
    echo "중단: 트랜스크립트 디렉터리가 없다 — $base" >&2
    exit 1
  }

  slug=$(printf '%s' "$root" | tr '/\\:' '-')

  if [ "$all" = 1 ]; then
    # 형제 프로젝트 = 저장소 상위 디렉터리를 접두사로 갖는 트랜스크립트 폴더.
    # 절대 경로를 박지 않고 현재 저장소 위치에서 유도한다.
    parent=$(dirname "$root")
    pslug=$(printf '%s' "$parent" | tr '/\\:' '-')
    found=0
    for c in "$base"/*/; do
      [ -d "$c" ] || continue
      cn=${c%/}; cn=${cn##*/}
      case "$cn" in "$pslug"*) ;; *) continue ;; esac
      label=${cn#"$pslug"}; label=${label#-}
      [ -n "$label" ] || label=${cn##*-}
      scan_dir "$base/$cn" 0 || continue
      found=1
      add_row "$label" "$d_spawn" "$d_msgs" "$d_max" "$d_async" "$d_iso" \
        "$d_c1" "$d_c2" "$d_c3" "$d_busy" "$d_maxcc" "$d_loss" "$d_drop"
    done
    [ "$found" = 1 ] || {
      echo "중단: '$pslug*' 에 해당하는 트랜스크립트 폴더가 없다 — $base" >&2
      exit 1
    }
    title="fan-out 계측 · 형제 프로젝트별 · 프로젝트당 최근 ${n}개 세션 · gap=${gap}s"
    head1=project
  else
    dir=$base/$slug
    if [ ! -d "$dir" ]; then
      # 느슨한 재시도: 경로에 . _ 같은 문자가 섞이면 Claude Code 쪽 치환 규칙과
      # 어긋날 수 있으므로, 영숫자 외 문자를 모두 -로 눕히고 소문자로 비교한다.
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
    scan_dir "$dir" 1 || {
      echo "중단: 세션 파일(*.jsonl)이 없다 — $dir" >&2
      exit 1
    }
    title="fan-out 계측 · 최근 ${n}개 세션 · gap=${gap}s"
    head1=session
  fi

  echo "── $title ──"
  printf '%-13s %5s %4s %4s %5s %4s %5s %5s %5s %4s %6s %5s\n' \
    "$head1" spawn msg max 'asy%' iso 'cc1%' 'cc2%' 'cc3%' mcc loss drop
  printf '%s' "$rows"
  printf '%-13s %5s %4s %4s %5s %4s %5s %5s %5s %4s %6s %5s\n' TOTAL \
    "$a_spawn" "$a_msgs" "$a_max" "$(pct "$a_async" "$a_spawn")%" "$a_iso" \
    "$(pct "$a_c1" "$a_busy")%" "$(pct "$a_c2" "$a_busy")%" "$(pct "$a_c3" "$a_busy")%" \
    "$a_maxcc" "$((a_loss / 60))m" "$a_drop"
  echo "spawn=Agent 호출 / msg=스폰이 발생한 메시지 수 / max=한 메시지 최대 fan-out 폭"
  echo "asy%=async_launched 비율(툴이 비동기로 발사한 비율) / iso=isolation 사용 횟수"
  echo "cc1/cc2/cc3%=동시 실행 1개/2개/3개 이상이던 시간 비율(분모: 에이전트가 1개라도 살아 있던 시간)"
  echo "mcc=최대 동시 실행 수 / loss=직렬 손실 추정(체인 구간합-최대구간, ${gap}초 이내 연쇄) / drop=완료 통지 없어 제외한 스폰"
  echo "max가 계속 1이고 cc1%가 100%에 가까우면 분해·병렬 위임 규칙이 먹지 않은 것이다."
}

main "$@"
