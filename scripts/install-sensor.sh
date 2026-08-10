#!/usr/bin/env bash
# INHERIT AutoCert 센서 설치·등록 마법사 — 대상: 격리 세그먼트의 센서 호스트(Linux x86_64).
# 실행: sudo bash install-sensor.sh
#
# 하는 일: 사전 점검(권한·NTP·패키지/바이너리 무결성·게이트웨이 도달성) → 바이너리 배치 →
#          enroll → systemd 유닛 등록·기동 → 상태 확인까지 한 번에 끝낸다.
# 절차 원문과 트러블슈팅은 manual/SENSOR.md.
#
# 보안: 등록 토큰은 화면에 에코하지 않고 로그·출력 어디에도 남기지 않는다.
#       개인키는 센서 호스트에서 생성되어 호스트를 떠나지 않는다(이 스크립트도 다루지 않는다).

# `sh install-sensor.sh` 로 실행되어도 동작하게 bash로 다시 띄운다 — 이 스크립트는
# bash 문법(배열·BASH_SOURCE·pipefail)을 쓴다. set -o pipefail 앞이어야 dash가 안 죽는다.
if [ -z "${BASH_VERSION:-}" ]; then
	if command -v bash >/dev/null 2>&1; then exec bash "$0" "$@"; fi
	printf '이 스크립트는 bash가 필요하다 (sudo bash install-sensor.sh).\n' >&2
	exit 1
fi

set -euo pipefail

SERVICE_NAME="autocert-sensor"
INSTALL_PATH="/usr/local/bin/autocert-sensor"
DEFAULT_STATE_DIR="/var/lib/autocert-sensor"
UNIT_DIR="/etc/systemd/system"
DIST_NAME="autocert-sensor-linux-amd64"

state_dir="$DEFAULT_STATE_DIR"
opt_server=""
opt_token=""
opt_fingerprint=""
opt_binary=""
opt_token_file=""
assume_yes=0
force=0
mode=""       # install | service-only | status
no_tui=0

# ──────────────────────────────────────────────────────────────────────────────
# 출력 유틸
# ──────────────────────────────────────────────────────────────────────────────
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	C_RESET=$'\033[0m'; C_DIM=$'\033[2m'; C_BOLD=$'\033[1m'
	C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_CYAN=$'\033[36m'
	C_INV=$'\033[7m'
else
	C_RESET=''; C_DIM=''; C_BOLD=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_CYAN=''; C_INV=''
fi

say()  { printf '%s\n' "$*"; }
head1() { printf '\n%s%s%s\n' "$C_BOLD" "$*" "$C_RESET"; }
die()  { printf '\n%s오류:%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; exit 1; }
hint() { printf '        %s└ %s%s\n' "$C_DIM" "$*" "$C_RESET"; }

# 점검 결과 누적 (하드 실패 개수 / 경고 개수)
check_fail=0
check_warn=0
row_ok()   { printf '  %s[ OK ]%s  %s\n' "$C_GREEN" "$C_RESET" "$*"; }
row_warn() { printf '  %s[경고]%s  %s\n' "$C_YELLOW" "$C_RESET" "$*"; check_warn=$((check_warn + 1)); }
row_fail() { printf '  %s[실패]%s  %s\n' "$C_RED" "$C_RESET" "$*"; check_fail=$((check_fail + 1)); }

usage() {
	cat <<'EOF'
사용법: sudo bash install-sensor.sh [옵션]

옵션 없이 실행하면 방향키 메뉴로 안내한다(권장). 웹 콘솔 [센서] 화면이 보여주는
enroll 명령을 통째로 붙여넣으면 서버 주소·토큰·CA fingerprint를 알아서 뽑아낸다.

비대화 모드 (값 3개를 인자로):
  sudo bash install-sensor.sh \
    --server https://10.20.0.5:8443 \
    --token-file /root/enroll-token.txt \
    --ca-fingerprint <sha256 hex 64자>

옵션:
  --server <URL>            AutoCert 서버 주소 (https://호스트:포트)
  --token <토큰>            1회용 등록 토큰. ps에 노출되므로 --token-file 권장
  --token-file <경로>       토큰이 든 파일 (첫 줄만 읽는다)
  --ca-fingerprint <hex>    서버 CA 인증서 SHA-256 지문
  --binary <경로>           센서 바이너리 경로 (기본: 스크립트 주변에서 자동 탐색)
  --state-dir <경로>        상태 디렉터리 (기본: /var/lib/autocert-sensor)
  --service-name <이름>     systemd 서비스 이름 (기본: autocert-sensor)
  --skip-enroll             enroll 없이 서비스만 재설치 (이미 등록된 센서용)
  --status                  설치·등록·서비스 상태만 확인하고 종료
  --force                   사전 점검 경고를 무시하고 진행
  -y, --yes                 모든 확인을 자동 승인 (비대화)
  --no-tui                  방향키 메뉴 대신 번호 입력만 사용
  -h, --help                이 도움말

환경변수 AUTOCERT_ENROLL_TOKEN 으로도 토큰을 넘길 수 있다.
EOF
}

# ──────────────────────────────────────────────────────────────────────────────
# 방향키 선택 메뉴 (미지원 터미널이면 번호 입력으로 자동 폴백)
# ──────────────────────────────────────────────────────────────────────────────
TUI=1
tui_detect() {
	if [ "$no_tui" -eq 1 ] || [ "$assume_yes" -eq 1 ]; then TUI=0; return; fi
	if [ ! -t 1 ] || [ ! -r /dev/tty ]; then TUI=0; return; fi
	case "${TERM:-}" in
		''|dumb) TUI=0 ;;
	esac
}

MENU_CHOICE=0
# menu_select "제목" "선택지1" "선택지2" ...  → MENU_CHOICE에 1-기반 인덱스
menu_select() {
	local title="$1"; shift
	local opts=("$@")
	local n=${#opts[@]}
	[ "$n" -gt 0 ] || return 1

	printf '\n%s%s%s\n' "$C_BOLD" "$title" "$C_RESET"

	if [ "$TUI" -eq 0 ]; then
		local i ans
		i=0
		while [ "$i" -lt "$n" ]; do
			printf '  %d) %s\n' "$((i + 1))" "${opts[$i]}"
			i=$((i + 1))
		done
		while :; do
			printf '  번호 선택 [1-%d]: ' "$n"
			ans=''
			read -r ans || true
			case "$ans" in
				''|*[!0-9]*) ;;
				*) if [ "$ans" -ge 1 ] && [ "$ans" -le "$n" ]; then MENU_CHOICE=$ans; return 0; fi ;;
			esac
			printf '  1~%d 사이의 번호를 입력한다.\n' "$n"
		done
	fi

	printf '  %s↑/↓ 이동 · Enter 선택%s\n' "$C_DIM" "$C_RESET"
	local sel=0 key rest i
	while :; do
		i=0
		while [ "$i" -lt "$n" ]; do
			if [ "$i" -eq "$sel" ]; then
				printf '\033[K  %s ▸ %s %s\n' "$C_INV" "${opts[$i]}" "$C_RESET"
			else
				printf '\033[K    %s\n' "${opts[$i]}"
			fi
			i=$((i + 1))
		done
		key=''
		IFS= read -rsn1 key </dev/tty || key=''
		case "$key" in
			$'\033')
				rest=''
				IFS= read -rsn2 -t 0.2 rest </dev/tty || rest=''
				case "$rest" in
					'[A') sel=$(((sel - 1 + n) % n)) ;;
					'[B') sel=$(((sel + 1) % n)) ;;
				esac
				;;
			'')
				MENU_CHOICE=$((sel + 1))
				printf '\n'
				return 0
				;;
			k) sel=$(((sel - 1 + n) % n)) ;;
			j) sel=$(((sel + 1) % n)) ;;
			[1-9])
				if [ "$key" -le "$n" ]; then
					MENU_CHOICE=$key
					printf '\n'
					return 0
				fi
				;;
		esac
		printf '\033[%dA' "$n"
	done
}

confirm() {
	# confirm "질문"  → 예면 0
	[ "$assume_yes" -eq 1 ] && return 0
	menu_select "$1" "예, 계속한다" "아니오, 중단한다"
	[ "$MENU_CHOICE" -eq 1 ]
}

# ──────────────────────────────────────────────────────────────────────────────
# 인자 파싱
# ──────────────────────────────────────────────────────────────────────────────
need_value() { [ $# -ge 2 ] || die "옵션 $1 에 값이 필요하다"; }

while [ $# -gt 0 ]; do
	case "$1" in
		--server) need_value "$@"; opt_server="$2"; shift 2 ;;
		--server=*) opt_server="${1#--server=}"; shift ;;
		--token) need_value "$@"; opt_token="$2"; shift 2 ;;
		--token=*) opt_token="${1#--token=}"; shift ;;
		--token-file) need_value "$@"; opt_token_file="$2"; shift 2 ;;
		--token-file=*) opt_token_file="${1#--token-file=}"; shift ;;
		--ca-fingerprint) need_value "$@"; opt_fingerprint="$2"; shift 2 ;;
		--ca-fingerprint=*) opt_fingerprint="${1#--ca-fingerprint=}"; shift ;;
		--binary) need_value "$@"; opt_binary="$2"; shift 2 ;;
		--binary=*) opt_binary="${1#--binary=}"; shift ;;
		--state-dir) need_value "$@"; state_dir="$2"; shift 2 ;;
		--state-dir=*) state_dir="${1#--state-dir=}"; shift ;;
		--service-name) need_value "$@"; SERVICE_NAME="$2"; shift 2 ;;
		--service-name=*) SERVICE_NAME="${1#--service-name=}"; shift ;;
		--skip-enroll) mode="service-only"; shift ;;
		--status) mode="status"; shift ;;
		--force) force=1; shift ;;
		-y|--yes) assume_yes=1; shift ;;
		--no-tui) no_tui=1; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; die "알 수 없는 옵션: $1" ;;
	esac
done

if [ -n "$opt_token_file" ]; then
	[ -r "$opt_token_file" ] || die "토큰 파일을 읽을 수 없다: $opt_token_file"
	opt_token=$(head -n 1 -- "$opt_token_file" | tr -d '\r\n[:space:]')
fi
if [ -z "$opt_token" ] && [ -n "${AUTOCERT_ENROLL_TOKEN:-}" ]; then
	opt_token="$AUTOCERT_ENROLL_TOKEN"
fi

tui_detect
UNIT_PATH="$UNIT_DIR/$SERVICE_NAME.service"

# ──────────────────────────────────────────────────────────────────────────────
# 값 검증 · 파싱
# ──────────────────────────────────────────────────────────────────────────────
strip_quotes() {
	local v="$1"
	v="${v%$'\r'}"
	case "$v" in
		\"*\") v="${v#\"}"; v="${v%\"}" ;;
		\'*\') v="${v#\'}"; v="${v%\'}" ;;
	esac
	printf '%s' "$v"
}

validate_server() {
	local v="$1"
	case "$v" in
		https://*) ;;
		http://*) printf '서버 주소는 https 여야 한다 (센서 게이트웨이는 mTLS 전용): %s' "$v"; return 1 ;;
		*) printf '서버 주소는 https://호스트:포트 형식이어야 한다: %s' "$v"; return 1 ;;
	esac
	local rest="${v#https://}"
	rest="${rest%%/*}"
	[ -n "$rest" ] || { printf '서버 주소에 호스트가 없다'; return 1; }
	case "$rest" in
		*' '*) printf '서버 주소에 공백이 들어 있다'; return 1 ;;
	esac
	return 0
}

# 정규화된 지문을 표준출력으로 (실패 시 비어 있음)
normalize_fingerprint() {
	local v="$1"
	v=$(printf '%s' "$v" | tr -d ': \t' | tr 'A-F' 'a-f')
	case "$v" in
		sha256:*) v="${v#sha256:}" ;;
	esac
	if [ ${#v} -ne 64 ]; then return 1; fi
	case "$v" in
		*[!0-9a-f]*) return 1 ;;
	esac
	printf '%s' "$v"
}

P_SERVER=''; P_TOKEN=''; P_FP=''
parse_enroll_command() {
	P_SERVER=''; P_TOKEN=''; P_FP=''
	# 붙여넣은 명령을 단어 단위로 훑는다 — 의도적 단어 분리.
	# shellcheck disable=SC2086
	set -- $1
	while [ $# -gt 0 ]; do
		case "$1" in
			--server)
				if [ $# -ge 2 ]; then P_SERVER=$(strip_quotes "$2"); shift; fi ;;
			--server=*)
				P_SERVER=$(strip_quotes "${1#--server=}") ;;
			--token)
				if [ $# -ge 2 ]; then P_TOKEN=$(strip_quotes "$2"); shift; fi ;;
			--token=*)
				P_TOKEN=$(strip_quotes "${1#--token=}") ;;
			--ca-fingerprint)
				if [ $# -ge 2 ]; then P_FP=$(strip_quotes "$2"); shift; fi ;;
			--ca-fingerprint=*)
				P_FP=$(strip_quotes "${1#--ca-fingerprint=}") ;;
		esac
		shift
	done
}

# ──────────────────────────────────────────────────────────────────────────────
# 사전 점검
# ──────────────────────────────────────────────────────────────────────────────
binary_src=''
binary_sha=''

# 반입 패키지에서는 install-sensor.sh 가 패키지 루트에 있다(SHA256SUMS·MANIFEST.txt 와 같은 자리).
script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

find_binary() {
	if [ -n "$opt_binary" ]; then
		[ -f "$opt_binary" ] || return 1
		binary_src="$opt_binary"
		return 0
	fi
	local cand
	for cand in \
		"./$DIST_NAME" \
		"./autocert-sensor" \
		"$script_dir/$DIST_NAME" \
		"$script_dir/autocert-sensor" \
		"$script_dir/../sensor/$DIST_NAME" \
		"$script_dir/../backend/dist/$DIST_NAME" \
		"$script_dir/sensor/$DIST_NAME" \
		"$script_dir/../$DIST_NAME"
	do
		if [ -f "$cand" ]; then
			binary_src=$(CDPATH='' cd -- "$(dirname -- "$cand")" && pwd)/$(basename -- "$cand")
			return 0
		fi
	done
	return 1
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -- "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 -- "$1" | awk '{print $1}'
	else
		printf ''
	fi
}

check_root() {
	if [ "$(id -u)" -eq 0 ]; then
		row_ok "관리자 권한 — root로 실행 중"
	else
		row_fail "관리자 권한 — root가 아니다"
		hint "고치는 법: sudo bash $0 로 다시 실행한다"
	fi
}

check_ntp() {
	# mTLS 인증서 유효기간 검증 때문에 시계가 어긋나면 enroll 자체가 실패한다 (SENSOR.md 2장).
	local synced='' src=''
	if command -v timedatectl >/dev/null 2>&1; then
		synced=$(timedatectl show -p NTPSynchronized --value 2>/dev/null || true)
		src='timedatectl'
	fi
	if [ -z "$synced" ] && command -v chronyc >/dev/null 2>&1; then
		if chronyc tracking 2>/dev/null | grep -q '^Leap status *: *Normal'; then
			synced='yes'
		else
			synced='no'
		fi
		src='chronyc'
	fi
	if [ -z "$synced" ] && command -v ntpstat >/dev/null 2>&1; then
		if ntpstat >/dev/null 2>&1; then synced='yes'; else synced='no'; fi
		src='ntpstat'
	fi

	case "$synced" in
		yes)
			row_ok "시각 동기(NTP) — 동기됨 ($src) · 현재 $(date '+%Y-%m-%d %H:%M:%S %Z')"
			;;
		no)
			row_fail "시각 동기(NTP) — 동기되지 않았다 ($src) · 현재 $(date '+%Y-%m-%d %H:%M:%S %Z')"
			hint "mTLS 인증서 유효기간 검증 때문에 시계가 어긋나면 enroll이 실패한다"
			hint "고치는 법: 폐쇄망 내부 NTP 서버를 지정하고 동기한다 —"
			hint "  timedatectl set-ntp true   또는   chronyc makestep && systemctl enable --now chronyd"
			;;
		*)
			row_warn "시각 동기(NTP) — 상태를 확인할 수 없다 (timedatectl·chronyc·ntpstat 없음)"
			hint "현재 시각: $(date '+%Y-%m-%d %H:%M:%S %Z') — 서버 시계와 수 분 이내인지 눈으로 대조한다"
			hint "어긋나 있으면 enroll이 인증서 유효기간 오류로 실패한다"
			;;
	esac
}

# 반입 패키지(sensor_linux_<버전>/)의 패키지 레벨 SHA256SUMS. 설치 스크립트 자신과
# lib/tui.sh·매뉴얼까지 포함하므로 바이너리 단위 대조보다 넓다.
# build-sensor-dist.sh 가 만드는 backend/dist/SHA256SUMS 는 바이너리 2줄뿐이고
# MANIFEST.txt 가 없다 — 그 차이로 두 경로를 가른다(패키지 레벨이 있으면 우선).
package_dir=''
package_verified=0

find_package_dir() {
	local c
	for c in "$script_dir" "$PWD"; do
		[ -n "$c" ] || continue
		if [ -f "$c/SHA256SUMS" ] && [ -f "$c/MANIFEST.txt" ]; then
			(CDPATH='' cd -- "$c" && pwd)
			return 0
		fi
	done
	return 1
}

check_package() {
	local dir out rc mismatch missing okc sha_check
	if ! dir=$(find_package_dir); then
		# 패키지가 아니라 바이너리만 반입한 경우다 — check_binary 가 바이너리 단위로 대조한다.
		return
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha_check='sha256sum -c'
	elif command -v shasum >/dev/null 2>&1; then
		sha_check='shasum -a 256 -c'
	else
		row_warn "패키지 무결성 — sha256sum·shasum이 없어 대조 불가 ($dir)"
		hint "무결성을 확인하지 못한 채로 설치하게 된다 — coreutils 를 설치한 뒤 다시 실행하는 편이 낫다"
		return
	fi

	rc=0
	# shellcheck disable=SC2086 # sha_check 는 "shasum -a 256 -c" 처럼 인자를 포함해 분리가 의도적이다
	out=$( (cd "$dir" && $sha_check SHA256SUMS) 2>&1 ) || rc=$?
	if [ "$rc" -eq 0 ]; then
		package_dir="$dir"
		package_verified=1
		okc=$(printf '%s\n' "$out" | grep -c ': OK$' || true)
		row_ok "패키지 무결성 — 확인됨 (SHA256SUMS ${okc}건) · $dir"
		return
	fi

	mismatch=$(printf '%s\n' "$out" | grep ': FAILED$' || true)
	missing=$(printf '%s\n' "$out" | grep ': FAILED open or read$' || true)

	if [ -n "$mismatch" ]; then
		# 내용이 다르다 = 매체 손상 또는 변조. 바이너리를 시스템 경로에 배치하기 전에 끊는다.
		# --force 로도 넘기지 않는다 — 여기서 넘기면 검증이 있으나 마나다.
		row_fail "패키지 무결성 — 파일 내용이 SHA256SUMS와 다르다 · $dir"
		printf '%s\n' "$mismatch" | while IFS= read -r l; do
			[ -n "$l" ] || continue
			hint "불일치: ${l%%:*}"
		done
		hint "매체 손상 또는 변조다. 패키지를 다시 반입해 재시도한다 (SENSOR.md 4.2)"
		die "패키지 체크섬 불일치 — 바이너리를 배치하지 않고 중단했다"
	fi

	if [ -n "$missing" ]; then
		row_fail "패키지 무결성 — SHA256SUMS에 적힌 파일이 없다 · $dir"
		printf '%s\n' "$missing" | while IFS= read -r l; do
			[ -n "$l" ] || continue
			hint "없음: ${l%%:*}"
		done
		hint "고치는 법: 압축을 다시 풀어 패키지를 온전한 상태로 만든 뒤 실행한다"
		return
	fi

	row_fail "패키지 무결성 — 대조에 실패했다 · $dir"
	printf '%s\n' "$out" | tail -n 3 | while IFS= read -r l; do
		[ -n "$l" ] || continue
		hint "$l"
	done
}

check_binary() {
	if ! find_binary; then
		row_fail "센서 바이너리 — 찾지 못했다 ($DIST_NAME)"
		hint "고치는 법: 반입한 바이너리와 같은 디렉터리에서 실행하거나 --binary <경로> 로 지정한다"
		hint "번들을 쓴다면 번들의 sensor/ 디렉터리에 있다 (SENSOR.md 4장)"
		return
	fi

	binary_sha=$(sha256_of "$binary_src")

	# 패키지 레벨 대조가 이미 이 바이너리를 포함해 통과했으면 다시 보지 않는다.
	if [ "$package_verified" -eq 1 ] && [ -n "$package_dir" ]; then
		case "$binary_src" in
			"$package_dir"/*)
				row_ok "센서 바이너리 — 무결성 확인됨 (패키지 SHA256SUMS 대조) · $binary_src"
				return
				;;
		esac
	fi

	local sums_dir sums_file
	sums_dir=$(dirname -- "$binary_src")
	sums_file="$sums_dir/SHA256SUMS"

	if [ ! -f "$sums_file" ]; then
		row_warn "센서 바이너리 — $binary_src (SHA256SUMS가 옆에 없어 대조 생략)"
		[ -n "$binary_sha" ] && hint "실측 SHA256: $binary_sha — 배포처 값과 눈으로 대조한다"
		return
	fi

	# 항목 경로는 "name" · "*name" · "./name" 세 형태가 다 나온다(패키지 SHA256SUMS는 ./ 접두).
	local want
	want=$(awk -v f="$(basename -- "$binary_src")" '
		{ p = $2; sub(/^\*/, "", p); sub(/^.*\//, "", p); if (p == f) { print $1; exit } }
	' "$sums_file")
	if [ -z "$want" ]; then
		row_warn "센서 바이너리 — $binary_src (SHA256SUMS에 이 파일 항목이 없다)"
		return
	fi
	if [ -z "$binary_sha" ]; then
		row_warn "센서 바이너리 — $binary_src (sha256sum·shasum이 없어 대조 불가)"
		return
	fi
	if [ "$binary_sha" = "$want" ]; then
		row_ok "센서 바이너리 — 무결성 확인됨 (SHA256SUMS 대조) · $binary_src"
	else
		row_fail "센서 바이너리 — SHA256 불일치 · $binary_src"
		hint "매체 손상 또는 변조다. 설치를 중단하고 바이너리를 다시 반입한다 (SENSOR.md 4.2)"
	fi
}

# host:port 를 SERVER_HOST / SERVER_PORT 로
SERVER_HOST=''; SERVER_PORT=''
split_server() {
	local rest="${1#https://}"
	rest="${rest%%/*}"
	case "$rest" in
		\[*\]:*) SERVER_HOST="${rest%]:*}"; SERVER_HOST="${SERVER_HOST#[}"; SERVER_PORT="${rest##*]:}" ;;
		\[*\])   SERVER_HOST="${rest#[}"; SERVER_HOST="${SERVER_HOST%]}"; SERVER_PORT=443 ;;
		*:*)     SERVER_HOST="${rest%:*}"; SERVER_PORT="${rest##*:}" ;;
		*)       SERVER_HOST="$rest"; SERVER_PORT=443 ;;
	esac
}

tcp_probe() {
	# tcp_probe host port → 도달하면 0
	local host="$1" port="$2"
	if command -v timeout >/dev/null 2>&1; then
		timeout 6 bash -c "exec 3<>/dev/tcp/$host/$port" >/dev/null 2>&1
	else
		bash -c "exec 3<>/dev/tcp/$host/$port" >/dev/null 2>&1
	fi
}

check_reachable() {
	split_server "$1"
	if tcp_probe "$SERVER_HOST" "$SERVER_PORT"; then
		row_ok "서버 게이트웨이 도달성 — $SERVER_HOST:$SERVER_PORT TCP 연결됨"
	else
		row_fail "서버 게이트웨이 도달성 — $SERVER_HOST:$SERVER_PORT 에 연결하지 못했다"
		hint "고치는 법 ①: 센서 호스트 → 서버 $SERVER_PORT/TCP 아웃바운드 방화벽을 연다 (SENSOR.md 3.2)"
		hint "고치는 법 ②: 서버에서 게이트웨이가 떴는지 확인 — backend 로그의 'sensor gateway listening'"
		hint "  없으면 .env의 SENSOR_CA_ENC_KEY·SENSOR_ADVERTISED_HOSTS를 세트로 확인 후 재기동 (SENSOR.md 3.1)"
	fi
}

# ──────────────────────────────────────────────────────────────────────────────
# 현재 설치 상태
# ──────────────────────────────────────────────────────────────────────────────
installed_sensor_id() {
	[ -f "$state_dir/sensor_id" ] || return 1
	tr -d '\r\n' < "$state_dir/sensor_id"
}

unit_server_url() {
	[ -f "$UNIT_PATH" ] || return 1
	sed -n 's/.*--server[= ]\([^ ]*\).*/\1/p' "$UNIT_PATH" | head -n 1
}

service_state() {
	command -v systemctl >/dev/null 2>&1 || { printf 'systemd 없음'; return; }
	systemctl is-active "$SERVICE_NAME" 2>/dev/null || true
}

has_systemd() {
	command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

show_status() {
	head1 "현재 상태"
	local sid
	if sid=$(installed_sensor_id); then
		row_ok "등록 — 완료 · 센서 ID $sid"
	else
		row_warn "등록 — 아직 등록되지 않았다 ($state_dir 에 신원 없음)"
	fi
	if [ -x "$INSTALL_PATH" ]; then
		row_ok "바이너리 — $INSTALL_PATH"
	else
		row_warn "바이너리 — $INSTALL_PATH 없음"
	fi
	if [ -f "$UNIT_PATH" ]; then
		local st
		st=$(service_state)
		if [ "$st" = "active" ]; then
			row_ok "서비스 — $SERVICE_NAME: active (running)"
		else
			row_warn "서비스 — $SERVICE_NAME: ${st:-unknown}"
		fi
		say ""
		say "  로그 확인: journalctl -u $SERVICE_NAME -f"
	else
		row_warn "서비스 — $UNIT_PATH 없음 (상시 실행 미등록)"
	fi
}

# ──────────────────────────────────────────────────────────────────────────────
# 입력 수집
# ──────────────────────────────────────────────────────────────────────────────
in_server=''; in_token=''; in_fp=''

read_secret_lines() {
	# 여러 줄 붙여넣기를 에코 없이 읽는다. 줄 끝 '\'는 이어짐, 빈 줄이면 종료.
	local buf='' line
	while :; do
		line=''
		read -rs line </dev/tty || true
		line="${line%$'\r'}"
		if [ -z "$line" ]; then
			if [ -n "$buf" ]; then break; fi
			continue
		fi
		printf '  %s· 한 줄 입력됨%s\n' "$C_DIM" "$C_RESET" >&2
		case "$line" in
			*\\)
				buf="$buf ${line%\\}"
				continue
				;;
		esac
		buf="$buf $line"
		break
	done
	printf '%s' "$buf"
}

collect_input_paste() {
	head1 "enroll 명령 붙여넣기"
	say "  웹 콘솔 [센서] 화면의 \"enroll 명령 예시\"를 통째로 붙여넣고 Enter."
	say "  ${C_DIM}여러 줄이면 그대로 붙여넣는다. 마지막에 빈 줄 Enter로 끝낸다.${C_RESET}"
	say "  ${C_DIM}토큰이 섞여 있으므로 입력은 화면에 표시하지 않는다.${C_RESET}"
	say ""
	local buf
	buf=$(read_secret_lines)
	parse_enroll_command "$buf"
	in_server="$P_SERVER"; in_token="$P_TOKEN"; in_fp="$P_FP"
}

prompt_plain() {
	# prompt_plain "질문" → 표준출력에 값
	local q="$1" v=''
	printf '  %s: ' "$q" >&2
	read -r v </dev/tty || true
	printf '%s' "$(strip_quotes "${v%$'\r'}")"
}

prompt_secret() {
	local q="$1" v=''
	printf '  %s (화면에 표시되지 않는다): ' "$q" >&2
	read -rs v </dev/tty || true
	printf '\n' >&2
	printf '%s' "$(strip_quotes "${v%$'\r'}")"
}

collect_input_fields() {
	head1 "값 3개 입력"
	in_server=$(prompt_plain "서버 주소 (예: https://10.20.0.5:8443)")
	in_token=$(prompt_secret "등록 토큰")
	in_fp=$(prompt_plain "CA fingerprint (sha256 hex 64자)")
}

validate_or_refill() {
	# 값 3개가 채워지고 형식이 맞을 때까지 반복. 비대화면 즉시 중단.
	while :; do
		local err=''
		if [ -z "$in_server" ]; then
			err="서버 주소를 읽지 못했다"
		elif ! err=$(validate_server "$in_server"); then
			:
		elif [ -z "$in_token" ]; then
			err="등록 토큰을 읽지 못했다"
		else
			local norm
			if norm=$(normalize_fingerprint "$in_fp"); then
				in_fp="$norm"
				err=''
			else
				err="CA fingerprint 형식이 아니다 (hex 64자여야 한다)"
			fi
		fi
		[ -z "$err" ] && return 0

		printf '\n  %s입력을 확인해야 한다:%s %s\n' "$C_YELLOW" "$C_RESET" "$err"
		if [ "$assume_yes" -eq 1 ]; then
			die "$err"
		fi
		menu_select "어떻게 할까" "값 3개를 다시 입력한다" "enroll 명령을 다시 붙여넣는다" "중단한다"
		case "$MENU_CHOICE" in
			1) collect_input_fields ;;
			2) collect_input_paste ;;
			*) die "사용자가 중단했다" ;;
		esac
	done
}

collect_input() {
	in_server="$opt_server"; in_token="$opt_token"; in_fp="$opt_fingerprint"
	if [ -n "$in_server" ] && [ -n "$in_token" ] && [ -n "$in_fp" ]; then
		validate_or_refill
		return
	fi
	if [ "$assume_yes" -eq 1 ]; then
		die "비대화 모드에는 --server, --token(또는 --token-file), --ca-fingerprint 가 모두 필요하다"
	fi
	menu_select "등록 정보를 어떻게 넣을까" \
		"enroll 명령을 통째로 붙여넣는다 (권장 · 값 3개를 자동 추출)" \
		"서버 주소 / 토큰 / fingerprint 를 따로 입력한다"
	if [ "$MENU_CHOICE" -eq 1 ]; then collect_input_paste; else collect_input_fields; fi
	validate_or_refill
}

# ──────────────────────────────────────────────────────────────────────────────
# 설치 단계
# ──────────────────────────────────────────────────────────────────────────────
install_binary() {
	head1 "[1/5] 바이너리 배치"
	if [ -z "$binary_src" ]; then
		if [ -x "$INSTALL_PATH" ]; then
			say "  반입 바이너리를 못 찾았지만 $INSTALL_PATH 가 이미 있다 — 그대로 쓴다."
			return
		fi
		die "설치할 센서 바이너리가 없다 (--binary 로 지정한다)"
	fi
	if [ "$binary_src" = "$INSTALL_PATH" ]; then
		say "  이미 $INSTALL_PATH 에 있다 — 복사를 건너뛴다."
	else
		if [ -x "$INSTALL_PATH" ] && [ -n "$binary_sha" ] && [ "$(sha256_of "$INSTALL_PATH")" = "$binary_sha" ]; then
			say "  $INSTALL_PATH 가 같은 바이너리다 — 복사를 건너뛴다."
		else
			install -m 0755 -- "$binary_src" "$INSTALL_PATH"
			say "  $binary_src → $INSTALL_PATH (0755, root:root)"
		fi
	fi
	chmod 0755 "$INSTALL_PATH"
	chown root:root "$INSTALL_PATH" 2>/dev/null || true
}

prepare_state_dir() {
	head1 "[2/5] 상태 디렉터리"
	if [ -d "$state_dir" ]; then
		say "  이미 있다: $state_dir (내용은 건드리지 않는다)"
	else
		mkdir -p -- "$state_dir"
		say "  생성: $state_dir"
	fi
	chmod 0700 -- "$state_dir"
	chown root:root -- "$state_dir" 2>/dev/null || true
	say "  권한 0700 root:root — 여기에 센서 개인키가 저장된다. 다른 호스트로 복사하지 않는다."
}

run_enroll() {
	head1 "[3/5] 서버에 등록 (enroll)"
	local existing
	if existing=$(installed_sensor_id); then
		say "  기존 신원을 덮어쓴다 (센서 ID $existing)."
	fi
	# 토큰은 인자로만 전달하고 어디에도 출력하지 않는다.
	say "  실행: $INSTALL_PATH enroll --server $in_server --token *** --ca-fingerprint $in_fp"
	if "$INSTALL_PATH" enroll \
		--server "$in_server" \
		--token "$in_token" \
		--ca-fingerprint "$in_fp" \
		--state-dir "$state_dir"; then
		in_token=''
		say "  ${C_GREEN}등록 성공${C_RESET}"
	else
		in_token=''
		say ""
		say "  ${C_RED}등록 실패${C_RESET} — 자주 나오는 원인:"
		say "    · 401 invalid_token   → 토큰이 이미 쓰였거나 24시간이 지났다. 콘솔에서 새로 발급받는다"
		say "    · fingerprint 불일치  → 서버를 재구축했다면 CA가 바뀌었다. 새로 등록한다"
		say "    · 인증서 유효기간 오류 → 이 호스트의 시계 드리프트. NTP 동기 후 재시도한다"
		die "enroll 실패 — 위 원인과 manual/SENSOR.md 9장 참고"
	fi
}

write_unit() {
	head1 "[4/5] 상시 실행 등록 (systemd)"
	if ! has_systemd; then
		row_warn "systemd를 찾지 못했다 — 서비스 등록을 건너뛴다"
		say ""
		say "  대안 (조직 표준에 맞춰 하나를 고른다):"
		say "    · SysV init / OpenRC: 아래 명령을 감싸는 init 스크립트를 만들고 부팅 시 기동한다"
		say "    · supervisord: program 항목으로 등록 (autorestart=true)"
		say "    · 최후 수단: crontab -e 에 @reboot 항목 추가 (재시작 감시는 안 된다)"
		say ""
		say "  상시 실행할 명령:"
		say "    $INSTALL_PATH run --server $in_server --state-dir $state_dir"
		return 1
	fi

	local extra=''
	[ "$state_dir" != "$DEFAULT_STATE_DIR" ] && extra=" --state-dir $state_dir"

	cat > "$UNIT_PATH" <<EOF
[Unit]
Description=INHERIT AutoCert Sensor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_PATH run --server $in_server$extra
Restart=always
RestartSec=5
# 상태 디렉터리($state_dir, 0700)를 소유한 계정으로 실행한다.
# 전용 계정으로 바꾸려면 디렉터리 소유권도 함께 넘긴다 (chown -R).
User=root
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
EOF
	chmod 0644 "$UNIT_PATH"
	say "  유닛 작성: $UNIT_PATH (Restart=always, RestartSec=5)"
	systemctl daemon-reload
	say "  systemctl daemon-reload"
	return 0
}

start_service() {
	head1 "[5/5] 기동과 상태 확인"
	systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
	if systemctl restart "$SERVICE_NAME"; then
		say "  systemctl enable --now $SERVICE_NAME"
	else
		say "  ${C_RED}기동 실패${C_RESET}"
	fi

	# 기동 직후는 activating일 수 있어 몇 초 확인한다.
	local i st=''
	for i in 1 2 3 4 5 6; do
		st=$(systemctl is-active "$SERVICE_NAME" 2>/dev/null || true)
		[ "$st" = "active" ] && break
		sleep 1
	done

	if [ "$st" = "active" ]; then
		row_ok "서비스 상태 — active (running)"
		return 0
	fi
	row_fail "서비스 상태 — ${st:-unknown}"
	say ""
	say "  최근 로그:"
	journalctl -u "$SERVICE_NAME" -n 15 --no-pager 2>/dev/null | sed 's/^/    /' || true
	return 1
}

summary() {
	local ok="$1" sid
	sid=$(installed_sensor_id || printf '(확인 불가)')
	local st
	st=$(service_state)

	if [ "$ok" -eq 0 ]; then
		printf '\n%s%s설치 완료%s\n' "$C_BOLD" "$C_GREEN" "$C_RESET"
	else
		printf '\n%s%s설치는 됐지만 확인이 필요하다%s\n' "$C_BOLD" "$C_YELLOW" "$C_RESET"
	fi
	say "  센서 호스트   $(hostname 2>/dev/null || printf '?')"
	say "  센서 ID       $sid"
	say "  서버 주소     $in_server"
	say "  상태 디렉터리 $state_dir (0700)"
	say "  서비스        $SERVICE_NAME — ${st:-미등록}"
	say ""
	if has_systemd; then
		say "  로그 확인:  journalctl -u $SERVICE_NAME -f"
	else
		say "  로그 확인:  위 '상시 실행할 명령'을 등록한 감시 도구의 로그를 본다 (센서는 JSON을 표준출력으로 낸다)"
	fi
	say ""
	say "  ${C_DIM}웹 콘솔 [센서] 화면에서 상태가 '활성'으로, 마지막 접속이 수 분 이내로 갱신되면 정상이다.${C_RESET}"
	say "  ${C_DIM}안 붙으면: 위 로그 → manual/SENSOR.md 9장 트러블슈팅 표.${C_RESET}"
}

# ──────────────────────────────────────────────────────────────────────────────
# 메인
# ──────────────────────────────────────────────────────────────────────────────
printf '%s' "$C_CYAN"
cat <<'EOF'
INHERIT AutoCert — 센서 설치·등록
EOF
printf '%s' "$C_RESET"

if [ "$mode" = "status" ]; then
	show_status
	exit 0
fi

# 재실행 안전 — 이미 등록된 센서를 감지하면 무엇을 할지 먼저 묻는다.
if [ -z "$mode" ]; then
	if existing_id=$(installed_sensor_id); then
		head1 "이미 등록된 센서가 있다"
		say "  센서 ID       $existing_id"
		say "  상태 디렉터리 $state_dir"
		say "  서비스        $SERVICE_NAME — $(service_state)"
		say "  ${C_DIM}상태 디렉터리는 이 스크립트가 지우지 않는다.${C_RESET}"
		if [ "$assume_yes" -eq 1 ]; then
			mode="service-only"
			say "  비대화 모드: 서비스만 재설치한다."
		else
			menu_select "무엇을 할까" \
				"재등록 — 새 토큰으로 다시 등록한다 (기존 신원을 덮어쓴다)" \
				"서비스만 재설치 — 등록은 그대로 두고 유닛·기동만 다시 한다" \
				"상태만 확인" \
				"취소"
			case "$MENU_CHOICE" in
				1) mode="install" ;;
				2) mode="service-only" ;;
				3) show_status; exit 0 ;;
				*) say "취소했다."; exit 0 ;;
			esac
		fi
	else
		mode="install"
	fi
fi

# --- 사전 점검 (1/2) ---
head1 "사전 점검"
check_root
check_ntp
# 패키지 대조가 먼저다 — 통과하면 check_binary 가 그 결과를 그대로 쓴다.
check_package
check_binary

if [ "$check_fail" -gt 0 ]; then
	if [ "$force" -eq 1 ]; then
		say ""
		say "  ${C_YELLOW}--force: 실패 $check_fail 건을 무시하고 진행한다.${C_RESET}"
	else
		say ""
		say "  ${C_RED}실패 $check_fail 건.${C_RESET} 위의 '고치는 법'을 처리하고 다시 실행한다."
		say "  ${C_DIM}알면서 강행하려면 --force.${C_RESET}"
		exit 1
	fi
fi

# --- 등록 정보 ---
if [ "$mode" = "service-only" ]; then
	in_server="$opt_server"
	if [ -z "$in_server" ]; then
		in_server=$(unit_server_url || printf '')
	fi
	if [ -z "$in_server" ]; then
		if [ "$assume_yes" -eq 1 ]; then
			die "기존 유닛에서 서버 주소를 찾지 못했다 — --server 로 지정한다"
		fi
		head1 "서버 주소"
		say "  기존 유닛에서 서버 주소를 찾지 못했다."
		in_server=$(prompt_plain "서버 주소 (예: https://10.20.0.5:8443)")
	else
		say ""
		say "  서버 주소는 기존 설정을 그대로 쓴다: $in_server"
	fi
	if ! err=$(validate_server "$in_server"); then
		die "$err"
	fi
else
	collect_input
fi

# --- 사전 점검 (2/2) — 도달성은 주소를 알아야 볼 수 있다 ---
head1 "사전 점검 — 서버 도달성"
check_fail=0
check_reachable "$in_server"
if [ "$check_fail" -gt 0 ]; then
	if [ "$force" -eq 1 ] || [ "$assume_yes" -eq 1 ]; then
		say ""
		say "  ${C_YELLOW}도달하지 못했지만 계속 진행한다.${C_RESET}"
	else
		if ! confirm "서버에 닿지 않는다. 그래도 계속할까? (enroll이 거의 확실히 실패한다)"; then
			in_token=''
			die "중단했다 — 방화벽·게이트웨이 확인 후 다시 실행한다"
		fi
	fi
fi

# --- 설치 ---
install_binary
prepare_state_dir
if [ "$mode" = "service-only" ]; then
	head1 "[3/5] 서버에 등록 (enroll)"
	say "  건너뛴다 — 기존 등록을 그대로 쓴다."
else
	run_enroll
fi

result=0
if write_unit; then
	start_service || result=1
else
	result=1
fi

summary "$result"
exit "$result"
