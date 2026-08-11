#!/usr/bin/env bash
# INHERIT AutoCert — 설치 마법사 터미널 UI 공용 함수.
# scripts/install.sh가 source 해서 쓴다. 단독 실행 대상이 아니다.
#
# 제공하는 것
#   tui_init / tui_cleanup              초기화·복구(커서 표시 되돌리기)
#   ui_title ui_step ui_info ui_ok ui_warn ui_err ui_hr ui_row   출력 헬퍼
#   tui_select   화살표 단일 선택       → TUI_INDEX / TUI_VALUE
#   tui_multi    화살표 다중 선택       → TUI_SELECTED (줄바꿈 구분) / TUI_SELECTED_IDX
#   tui_confirm  예/아니오              → 종료코드 0(예)/1(아니오)
#   tui_input    한 줄 입력             → TUI_ANSWER
#   tui_browse   디렉터리 트리 탐색     → TUI_PATH (취소는 1)
#
# 입력 모드는 3단계다.
#   raw   — 제어 터미널이 있고 ANSI/1문자 읽기가 되는 경우. 화살표·스페이스로 조작.
#   plain — 터미널은 있으나 raw 입력이 안 되는 경우. 번호를 입력받는다.
#   none  — 제어 터미널 자체가 없는 경우(파이프·cron). 묻지 않고 기본값을 쓴다.
# TUI_ASSUME_YES=1 이면 모드와 무관하게 항상 기본값을 쓴다.
#
# 모든 UI 출력은 fd 3(터미널), 입력은 fd 4(터미널)로 간다 — 스크립트의
# stdout을 파일로 넘겨도 화면 조작이 깨지지 않게 하려는 것이다.

# shellcheck shell=bash

TUI_MODE="none"
TUI_TTY=""
TUI_ASSUME_YES="${TUI_ASSUME_YES:-}"
TUI_INDEX=0
TUI_VALUE=""
TUI_SELECTED=""
TUI_SELECTED_IDX=""
TUI_ANSWER=""
TUI_PATH=""

# --- 초기화 --------------------------------------------------------------

tui_init() {
	if ( exec 3>/dev/tty ) 2>/dev/null && ( exec 4</dev/tty ) 2>/dev/null; then
		TUI_TTY=/dev/tty
	fi

	if [ -n "$TUI_TTY" ]; then
		exec 3>"$TUI_TTY"
		exec 4<"$TUI_TTY"
		TUI_MODE="plain"
	else
		# 터미널이 없으면 안내만이라도 보이게 stderr로 흘린다.
		exec 3>&2
		TUI_MODE="none"
	fi

	if [ "$TUI_MODE" = "plain" ] && [ -z "${TUI_FORCE_PLAIN:-}" ]; then
		case "${TERM:-dumb}" in
			dumb | unknown | "") : ;;
			*) if _tui_probe_raw; then TUI_MODE="raw"; fi ;;
		esac
	fi

	if [ -n "$TUI_TTY" ] && [ -z "${NO_COLOR:-}" ] && [ "$TUI_MODE" = "raw" ]; then
		C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'; C_REV=$'\033[7m'
		C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_CYAN=$'\033[36m'
	else
		C_RESET=""; C_BOLD=""; C_DIM=""; C_REV=""
		C_RED=""; C_GREEN=""; C_YELLOW=""; C_CYAN=""
	fi
}

# read -s / -n 이 실제로 먹히는 셸인지 본다. 파이프로 문자를 하나 밀어넣어
# 확인하므로 사용자 입력을 기다리거나 삼키지 않는다.
# shellcheck disable=SC2034 # probe는 옵션 파싱 가능 여부만 보는 버림값이다
_tui_probe_raw() {
	local probe=""
	if printf 'x' | { read -r -s -n 1 -t 1 probe; } 2>/dev/null; then
		return 0
	fi
	return 1
}

tui_cleanup() {
	if [ "$TUI_MODE" = "raw" ]; then
		printf '\033[?25h' >&3 2>/dev/null || true
	fi
}

# --- 출력 헬퍼 -----------------------------------------------------------

ui_hr() { printf '%s\n' "  ────────────────────────────────────────────────────────────" >&3; }
ui_blank() { printf '\n' >&3; }
ui_title() {
	printf '\n%s\n' "  ${C_BOLD}${C_CYAN}== $* ==${C_RESET}" >&3
	ui_hr
}
ui_step() { printf '%s\n' "  ${C_BOLD}$*${C_RESET}" >&3; }
ui_info() { printf '%s\n' "         $*" >&3; }
ui_ok()   { printf '%s\n' "  ${C_GREEN}[ OK ]${C_RESET} $*" >&3; }
ui_warn() { printf '%s\n' "  ${C_YELLOW}[경고]${C_RESET} $*" >&3; }
ui_err()  { printf '%s\n' "  ${C_RED}[실패]${C_RESET} $*" >&3; }
ui_note() { printf '%s\n' "  ${C_DIM}$*${C_RESET}" >&3; }

# ui_row STATUS LABEL VALUE — 사전 점검 표의 한 줄.
# STATUS는 ok|warn|fail|info. 상태 칸만 폭이 고정이라 로케일과 무관하게 정렬된다.
ui_row() {
	local status=$1 label=$2 value=${3:-}
	local tag
	case "$status" in
		ok)   tag="${C_GREEN}[ OK ]${C_RESET}" ;;
		warn) tag="${C_YELLOW}[주의]${C_RESET}" ;;
		fail) tag="${C_RED}[실패]${C_RESET}" ;;
		*)    tag="${C_DIM}[정보]${C_RESET}" ;;
	esac
	if [ -n "$value" ]; then
		printf '  %s %s · %s\n' "$tag" "$label" "$value" >&3
	else
		printf '  %s %s\n' "$tag" "$label" >&3
	fi
}

# --- 단일 선택 -----------------------------------------------------------

# tui_select TITLE DEFAULT_INDEX ITEM...
tui_select() {
	local title=$1 def=$2
	shift 2
	local n=$#
	local items=("$@")

	TUI_INDEX=$def
	TUI_VALUE=${items[def]}

	if [ "$n" -le 0 ]; then return 1; fi
	if [ "$TUI_MODE" = "none" ] || [ -n "$TUI_ASSUME_YES" ]; then
		printf '\n  %s\n' "${C_BOLD}${title}${C_RESET}" >&3
		printf '  %s\n' "${C_DIM}→ 기본값 사용: ${TUI_VALUE}${C_RESET}" >&3
		return 0
	fi

	if [ "$TUI_MODE" = "raw" ]; then
		_tui_select_raw "$title" "$n" "${items[@]}"
	else
		_tui_select_plain "$title" "$n" "${items[@]}"
	fi
}

_tui_select_raw() {
	local title=$1 n=$2
	shift 2
	local items=("$@")
	local sel=$TUI_INDEX i key rest

	printf '\n  %s\n' "${C_BOLD}${title}${C_RESET}" >&3
	printf '  %s\n' "${C_DIM}↑/↓ 이동 · Enter 선택${C_RESET}" >&3
	printf '\033[?25l' >&3
	for ((i = 0; i < n; i++)); do printf '\n' >&3; done

	while :; do
		printf '\033[%dA' "$n" >&3
		for ((i = 0; i < n; i++)); do
			if [ "$i" -eq "$sel" ]; then
				printf '\033[2K  %s\n' "${C_REV}${C_BOLD} ▸ ${items[i]} ${C_RESET}" >&3
			else
				printf '\033[2K  %s\n' "   ${items[i]}" >&3
			fi
		done

		if ! IFS= read -r -s -n 1 key <&4; then
			# 읽기가 깨지면 번호 입력으로 내려간다.
			printf '\033[?25h' >&3
			TUI_MODE="plain"
			_tui_select_plain "$title" "$n" "${items[@]}"
			return $?
		fi

		case "$key" in
			$'\033')
				rest=""
				IFS= read -r -s -n 2 -t 1 rest <&4 || rest=""
				case "$rest" in
					'[A') if [ "$sel" -gt 0 ]; then sel=$((sel - 1)); else sel=$((n - 1)); fi ;;
					'[B') if [ "$sel" -lt $((n - 1)) ]; then sel=$((sel + 1)); else sel=0; fi ;;
				esac
				;;
			k | K) if [ "$sel" -gt 0 ]; then sel=$((sel - 1)); else sel=$((n - 1)); fi ;;
			j | J) if [ "$sel" -lt $((n - 1)) ]; then sel=$((sel + 1)); else sel=0; fi ;;
			[1-9])
				if [ "$key" -le "$n" ]; then sel=$((key - 1)); fi
				;;
			"")
				printf '\033[?25h' >&3
				TUI_INDEX=$sel
				TUI_VALUE=${items[sel]}
				return 0
				;;
		esac
	done
}

_tui_select_plain() {
	local title=$1 n=$2
	shift 2
	local items=("$@")
	local i ans

	while :; do
		printf '\n  %s\n' "${C_BOLD}${title}${C_RESET}" >&3
		for ((i = 0; i < n; i++)); do
			if [ "$i" -eq "$TUI_INDEX" ]; then
				printf '   %d) %s  (기본)\n' "$((i + 1))" "${items[i]}" >&3
			else
				printf '   %d) %s\n' "$((i + 1))" "${items[i]}" >&3
			fi
		done
		printf '  번호 입력 [기본 %d]: ' "$((TUI_INDEX + 1))" >&3
		ans=""
		IFS= read -r ans <&4 || ans=""
		if [ -z "$ans" ]; then return 0; fi
		case "$ans" in
			*[!0-9]*) ui_warn "숫자를 입력한다." ;;
			*)
				if [ "$ans" -ge 1 ] && [ "$ans" -le "$n" ]; then
					TUI_INDEX=$((ans - 1))
					TUI_VALUE=${items[TUI_INDEX]}
					return 0
				fi
				ui_warn "1..${n} 범위로 입력한다."
				;;
		esac
	done
}

# --- 다중 선택 -----------------------------------------------------------

# tui_multi TITLE PRESELECT ITEM...
#   PRESELECT: "all" | "none" | "0 2 3" (인덱스 목록)
# 결과: TUI_SELECTED(줄바꿈 구분 값) / TUI_SELECTED_IDX(공백 구분 인덱스)
tui_multi() {
	local title=$1 preselect=$2
	shift 2
	local n=$#
	local items=("$@")
	local marks=() i

	for ((i = 0; i < n; i++)); do marks[i]=0; done
	case "$preselect" in
		all) for ((i = 0; i < n; i++)); do marks[i]=1; done ;;
		none | "") : ;;
		*) for i in $preselect; do if [ "$i" -lt "$n" ]; then marks[i]=1; fi; done ;;
	esac

	if [ "$n" -le 0 ]; then
		TUI_SELECTED=""; TUI_SELECTED_IDX=""
		return 0
	fi

	if [ "$TUI_MODE" = "none" ] || [ -n "$TUI_ASSUME_YES" ]; then
		_tui_multi_commit "$n" "${marks[@]}" "${items[@]}"
		printf '\n  %s\n' "${C_BOLD}${title}${C_RESET}" >&3
		printf '  %s\n' "${C_DIM}→ 기본 선택 사용: ${TUI_SELECTED//$'\n'/, }${C_RESET}" >&3
		return 0
	fi

	if [ "$TUI_MODE" = "raw" ]; then
		_tui_multi_raw "$title" "$n" "${marks[@]}" "${items[@]}"
	else
		_tui_multi_plain "$title" "$n" "${marks[@]}" "${items[@]}"
	fi
}

# 내부 규약: $1=n, 다음 n개=marks, 그 다음 n개=items
_tui_multi_commit() {
	local n=$1
	shift
	local marks=("${@:1:$n}")
	local items=("${@:$n+1:$n}")
	local i
	TUI_SELECTED=""
	TUI_SELECTED_IDX=""
	for ((i = 0; i < n; i++)); do
		if [ "${marks[i]}" -eq 1 ]; then
			if [ -z "$TUI_SELECTED" ]; then TUI_SELECTED="${items[i]}"; else TUI_SELECTED="${TUI_SELECTED}"$'\n'"${items[i]}"; fi
			TUI_SELECTED_IDX="${TUI_SELECTED_IDX}${TUI_SELECTED_IDX:+ }$i"
		fi
	done
}

_tui_multi_raw() {
	local title=$1 n=$2
	shift 2
	local marks=("${@:1:$n}")
	local items=("${@:$n+1:$n}")
	local sel=0 i key rest box

	printf '\n  %s\n' "${C_BOLD}${title}${C_RESET}" >&3
	printf '  %s\n' "${C_DIM}↑/↓ 이동 · 스페이스 토글 · a 전체 · n 해제 · Enter 확정${C_RESET}" >&3
	printf '\033[?25l' >&3
	for ((i = 0; i < n; i++)); do printf '\n' >&3; done

	while :; do
		printf '\033[%dA' "$n" >&3
		for ((i = 0; i < n; i++)); do
			if [ "${marks[i]}" -eq 1 ]; then box="[x]"; else box="[ ]"; fi
			if [ "$i" -eq "$sel" ]; then
				printf '\033[2K  %s\n' "${C_REV}${C_BOLD} ▸ ${box} ${items[i]} ${C_RESET}" >&3
			else
				printf '\033[2K  %s\n' "   ${box} ${items[i]}" >&3
			fi
		done

		if ! IFS= read -r -s -n 1 key <&4; then
			printf '\033[?25h' >&3
			TUI_MODE="plain"
			_tui_multi_plain "$title" "$n" "${marks[@]}" "${items[@]}"
			return $?
		fi

		case "$key" in
			$'\033')
				rest=""
				IFS= read -r -s -n 2 -t 1 rest <&4 || rest=""
				case "$rest" in
					'[A') if [ "$sel" -gt 0 ]; then sel=$((sel - 1)); else sel=$((n - 1)); fi ;;
					'[B') if [ "$sel" -lt $((n - 1)) ]; then sel=$((sel + 1)); else sel=0; fi ;;
				esac
				;;
			k | K) if [ "$sel" -gt 0 ]; then sel=$((sel - 1)); else sel=$((n - 1)); fi ;;
			j | J) if [ "$sel" -lt $((n - 1)) ]; then sel=$((sel + 1)); else sel=0; fi ;;
			' ')
				if [ "${marks[sel]}" -eq 1 ]; then marks[sel]=0; else marks[sel]=1; fi
				;;
			a | A) for ((i = 0; i < n; i++)); do marks[i]=1; done ;;
			n | N) for ((i = 0; i < n; i++)); do marks[i]=0; done ;;
			"")
				printf '\033[?25h' >&3
				_tui_multi_commit "$n" "${marks[@]}" "${items[@]}"
				return 0
				;;
		esac
	done
}

_tui_multi_plain() {
	local title=$1 n=$2
	shift 2
	local marks=("${@:1:$n}")
	local items=("${@:$n+1:$n}")
	local i ans tok box

	while :; do
		printf '\n  %s\n' "${C_BOLD}${title}${C_RESET}" >&3
		for ((i = 0; i < n; i++)); do
			if [ "${marks[i]}" -eq 1 ]; then box="[x]"; else box="[ ]"; fi
			printf '   %d) %s %s\n' "$((i + 1))" "$box" "${items[i]}" >&3
		done
		printf '  %s\n' "${C_DIM}번호를 공백으로 구분해 입력하면 토글된다. a=전체, n=해제, Enter=확정${C_RESET}" >&3
		printf '  입력: ' >&3
		ans=""
		IFS= read -r ans <&4 || ans=""
		case "$ans" in
			"")
				_tui_multi_commit "$n" "${marks[@]}" "${items[@]}"
				return 0
				;;
			a | A) for ((i = 0; i < n; i++)); do marks[i]=1; done ;;
			n | N) for ((i = 0; i < n; i++)); do marks[i]=0; done ;;
			*)
				for tok in $ans; do
					case "$tok" in
						*[!0-9]*) ui_warn "무시: $tok" ;;
						*)
							if [ "$tok" -ge 1 ] && [ "$tok" -le "$n" ]; then
								i=$((tok - 1))
								if [ "${marks[i]}" -eq 1 ]; then marks[i]=0; else marks[i]=1; fi
							fi
							;;
					esac
				done
				;;
		esac
	done
}

# --- 예/아니오 -----------------------------------------------------------

# tui_confirm QUESTION DEFAULT   (DEFAULT: yes|no)
tui_confirm() {
	local q=$1 def=${2:-yes} idx=0
	if [ "$def" = "no" ]; then idx=1; fi
	tui_select "$q" "$idx" "예" "아니오"
	if [ "$TUI_INDEX" -eq 0 ]; then return 0; fi
	return 1
}

# --- 한 줄 입력 ----------------------------------------------------------

# tui_input PROMPT DEFAULT [VALIDATOR_FN]
# VALIDATOR_FN은 값을 인자로 받아 0을 반환하면 통과. 실패 사유는 스스로 출력한다.
# shellcheck disable=SC2034 # TUI_ANSWER는 source 하는 쪽(install.sh)이 읽는다
tui_input() {
	local prompt=$1 def=${2:-} validator=${3:-}
	local ans

	TUI_ANSWER=$def
	if [ "$TUI_MODE" = "none" ] || [ -n "$TUI_ASSUME_YES" ]; then
		printf '  %s\n' "${C_DIM}${prompt} → 기본값: ${def}${C_RESET}" >&3
		return 0
	fi

	while :; do
		if [ -n "$def" ]; then
			printf '\n  %s [%s]: ' "${C_BOLD}${prompt}${C_RESET}" "$def" >&3
		else
			printf '\n  %s: ' "${C_BOLD}${prompt}${C_RESET}" >&3
		fi
		ans=""
		IFS= read -r ans <&4 || ans=""
		if [ -z "$ans" ]; then ans=$def; fi
		if [ -z "$validator" ]; then
			TUI_ANSWER=$ans
			return 0
		fi
		if "$validator" "$ans"; then
			TUI_ANSWER=$ans
			return 0
		fi
	done
}

# --- 디렉터리 탐색 -------------------------------------------------------

# 디렉터리를 절대경로로 정규화해 출력한다. 들어갈 수 없으면 1.
_tui_browse_dir_abs() {
	local p=$1
	[ -d "$p" ] || return 1
	( cd -- "$p" 2>/dev/null && pwd ) || return 1
}

# 파일 경로를 절대경로로 정규화해 출력한다. 상위 디렉터리 기준으로만 푼다.
_tui_browse_file_abs() {
	local p=$1 d b
	b=${p##*/}
	d=${p%/*}
	[ -n "$d" ] || d=/
	[ "$d" != "$p" ] || d=.
	d=$( cd -- "$d" 2>/dev/null && pwd ) || return 1
	case "$d" in
		/) printf '/%s\n' "$b" ;;
		*) printf '%s/%s\n' "$d" "$b" ;;
	esac
}

# tui_browse START_DIR PATTERN
#   PATTERN — 선택 대상 파일의 glob(예: .env). 디렉터리는 패턴과 무관하게 보인다.
# 결과: TUI_PATH(선택된 절대경로) / 취소는 TUI_PATH 비우고 return 1.
# 화면 하나가 디렉터리 하나이고, 항목 구성은 tui_select 위에 얹는다.
# shellcheck disable=SC2034 # TUI_PATH는 source 하는 쪽(install.sh)이 읽는다
tui_browse() {
	local start=${1:-} pat=${2:-*}
	local cur base ans next name entry
	local files=() dirs=() labels=() acts=() payloads=()
	local nfiles ndirs nlab total page=0 page_size=40
	local from to i idx steps=0 max_steps=500

	TUI_PATH=""

	# 비대화 모드에서는 탐색기를 띄우지 않는다 — 호출 측이 자동 탐지·플래그로 정한다.
	if [ "$TUI_MODE" = "none" ] || [ -n "$TUI_ASSUME_YES" ]; then
		return 1
	fi

	cur=$(_tui_browse_dir_abs "$start") || cur=""
	if [ -z "$cur" ]; then cur=$(_tui_browse_dir_abs "$PWD") || cur="/"; fi

	while :; do
		# 입력이 EOF면 tui_select가 기다리지 않고 기본값을 돌려주므로 화면 수를
		# 제한하지 않으면 설치가 무한 루프에 빠진다.
		steps=$((steps + 1))
		if [ "$steps" -gt "$max_steps" ]; then
			ui_warn "탐색을 너무 오래 반복해 중단한다."
			TUI_PATH=""
			return 1
		fi

		# 루트에서는 "$cur/*"가 "//*"가 되므로 접두어를 따로 둔다.
		base=$cur
		if [ "$base" = "/" ]; then base=""; fi

		files=(); dirs=(); nfiles=0; ndirs=0
		if [ -r "$cur" ] && [ -x "$cur" ]; then
			for entry in "$base"/* "$base"/.*; do
				[ -e "$entry" ] || continue
				name=${entry##*/}
				case "$name" in . | ..) continue ;; esac
				if [ -d "$entry" ]; then
					dirs[ndirs]=$name
					ndirs=$((ndirs + 1))
				else
					# 패턴은 glob으로 쓰려는 것이라 일부러 따옴표를 두르지 않는다.
					# shellcheck disable=SC2254
					case "$name" in
						$pat)
							files[nfiles]=$name
							nfiles=$((nfiles + 1))
							;;
					esac
				fi
			done
		else
			ui_warn "이 디렉터리는 읽을 수 없다: $cur"
		fi

		labels=(); acts=(); payloads=(); nlab=0
		if [ "$cur" != "/" ]; then
			labels[nlab]="[.. 상위 디렉터리]"; acts[nlab]="up"; payloads[nlab]=""
			nlab=$((nlab + 1))
		fi

		# 매칭 파일이 위, 하위 디렉터리가 아래. 많으면 잘라서 다음 페이지로 넘긴다.
		total=$((nfiles + ndirs))
		from=$((page * page_size))
		if [ "$from" -ge "$total" ]; then page=0; from=0; fi
		to=$((from + page_size))
		if [ "$to" -gt "$total" ]; then to=$total; fi
		for ((i = from; i < to; i++)); do
			if [ "$i" -lt "$nfiles" ]; then
				name=${files[i]}
				labels[nlab]="$name"; acts[nlab]="file"; payloads[nlab]="$name"
			else
				name=${dirs[i - nfiles]}
				payloads[nlab]="$name"
				if [ -r "$base/$name" ] && [ -x "$base/$name" ]; then
					labels[nlab]="$name/"; acts[nlab]="dir"
				else
					labels[nlab]="$name/  [권한 없음]"; acts[nlab]="denied"
				fi
			fi
			nlab=$((nlab + 1))
		done
		if [ "$to" -lt "$total" ]; then
			labels[nlab]="[다음 페이지] ($to/$total)"; acts[nlab]="next"; payloads[nlab]=""
			nlab=$((nlab + 1))
		fi
		# 직접 입력은 항상 마지막에 둔다 — plain 모드에 막다른 길을 만들지 않는다.
		labels[nlab]="[경로 직접 입력…]"; acts[nlab]="manual"; payloads[nlab]=""
		nlab=$((nlab + 1))
		labels[nlab]="[취소]"; acts[nlab]="cancel"; payloads[nlab]=""
		nlab=$((nlab + 1))

		ui_note "선택 대상: $pat · 디렉터리는 이름을 골라 들어간다"
		if ! tui_select "현재 위치: $cur" 0 "${labels[@]}"; then
			return 1
		fi
		idx=$TUI_INDEX

		case "${acts[idx]}" in
			up)
				next=$(_tui_browse_dir_abs "$cur/..") || next=""
				if [ -n "$next" ]; then cur=$next; page=0; fi
				;;
			dir)
				next=$(_tui_browse_dir_abs "$base/${payloads[idx]}") || next=""
				if [ -n "$next" ]; then
					cur=$next; page=0
				else
					ui_warn "들어갈 수 없다: ${payloads[idx]}"
				fi
				;;
			denied)
				ui_warn "읽기 권한이 없다: ${payloads[idx]}"
				;;
			next)
				page=$((page + 1))
				;;
			file)
				TUI_PATH=$(_tui_browse_file_abs "$base/${payloads[idx]}") || TUI_PATH=""
				if [ -n "$TUI_PATH" ]; then return 0; fi
				ui_warn "경로를 확인할 수 없다: ${payloads[idx]}"
				;;
			manual)
				tui_input "경로를 직접 입력한다 (빈 값이면 목록으로)" ""
				ans=$TUI_ANSWER
				if [ -z "$ans" ]; then continue; fi
				# 여기의 ~ 는 확장 대상이 아니라 사용자가 친 글자 그대로다.
				# shellcheck disable=SC2088
				case "$ans" in
					"~") ans=${HOME:-} ;;
					"~/"*) ans="${HOME:-}/${ans#*/}" ;;
				esac
				case "$ans" in
					/*) : ;;
					*) ans="$base/$ans" ;;
				esac
				if [ -d "$ans" ]; then
					next=$(_tui_browse_dir_abs "$ans") || next=""
					if [ -n "$next" ]; then
						cur=$next; page=0
					else
						ui_warn "들어갈 수 없다: $ans"
					fi
				elif [ -e "$ans" ]; then
					TUI_PATH=$(_tui_browse_file_abs "$ans") || TUI_PATH=""
					if [ -n "$TUI_PATH" ]; then return 0; fi
					ui_warn "경로를 확인할 수 없다: $ans"
				else
					ui_warn "그런 경로가 없다: $ans"
				fi
				;;
			cancel)
				TUI_PATH=""
				return 1
				;;
		esac
	done
}
