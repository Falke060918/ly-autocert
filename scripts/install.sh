#!/usr/bin/env bash
# INHERIT AutoCert 서버 설치 마법사 — 대상: 고객사 서버(Linux/macOS).
# 실행:  bash scripts/install.sh
#
# 하는 일: 사전 점검 → (필요하면) 오프라인 번들 적재 → .env 자동 구성 →
#          docker compose 기동 → /healthz 확인 → 접속 안내.
# 사용자가 직접 타이핑할 것은 사실상 없다. 비밀값(DB 비밀번호·센서 CA 봉인 키)은
# 스크립트가 만들어 .env(600)에만 넣고 화면에는 절대 출력하지 않는다.
#
# 자세한 배경·수동 절차는 docs/manual/INSTALL.md.

# 이 스크립트는 bash 문법(배열·${!var}·pipefail)을 쓴다. `sh install.sh` 로 실행되어도
# 동작하도록 bash로 다시 띄운다 — set -o pipefail 앞이어야 dash가 여기서 죽지 않는다.
if [ -z "${BASH_VERSION:-}" ]; then
	if command -v bash >/dev/null 2>&1; then exec bash "$0" "$@"; fi
	printf '이 스크립트는 bash가 필요하다 (bash install.sh).\n' >&2
	exit 1
fi

set -euo pipefail

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
PARENT_DIR=$(CDPATH='' cd -- "$SCRIPT_DIR/.." && pwd)

# 배치 두 가지를 여기서 가른다.
#   저장소   : scripts/install.sh          → 설치 루트는 저장소 루트(한 단계 위)
#   반입 패키지: main_<버전>/install.sh    → 패키지 루트가 곧 설치 루트다
# 셔뱅·실행 인자·현재 디렉터리는 믿지 않는다 — **마커 파일 존재**로만 가른다.
# 저장소의 scripts/ 에도 load-offline.sh 사본이 있어서 그 파일 하나로는 구분되지 않는다.
root_markers_ok() {
	# 설치 루트에는 compose 파일이 반드시 있고(스택 정의), 그 옆에 버전·설정 표본이 붙는다.
	[ -f "$1/docker-compose.yml" ] || return 1
	[ -f "$1/VERSION" ] || [ -f "$1/.env.example" ] || [ -f "$1/MANIFEST.txt" ] || return 1
	return 0
}

if root_markers_ok "$SCRIPT_DIR"; then
	PACKAGE_MODE=1
	ROOT_DIR=$SCRIPT_DIR
elif root_markers_ok "$PARENT_DIR"; then
	PACKAGE_MODE=0
	ROOT_DIR=$PARENT_DIR
else
	# 여기서 조용히 진행하면 .env·images/·source/ 를 엉뚱한 디렉터리에서 찾게 된다.
	printf '설치 루트를 찾지 못했다 — docker-compose.yml 이 있는 디렉터리가 아니다.\n' >&2
	printf '  확인한 위치: %s\n' "$SCRIPT_DIR" >&2
	printf '              %s\n' "$PARENT_DIR" >&2
	printf '실행 위치를 확인한다.\n' >&2
	printf '  저장소   : 저장소 루트에서  bash scripts/install.sh\n' >&2
	printf '  반입 패키지: main_<버전>/ 안에서  bash install.sh\n' >&2
	printf '패키지를 푼 디렉터리가 통째로 옮겨졌거나 일부 파일만 복사된 것은 아닌지 확인한다.\n' >&2
	exit 1
fi

# shellcheck source=scripts/lib/tui.sh
. "$SCRIPT_DIR/lib/tui.sh"

ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/docker-compose.yml"
COMPOSE_HTTPS_FILE="$ROOT_DIR/docker-compose.https.yml"
VERSION_FILE="$ROOT_DIR/VERSION"
# 서버 인증서·개인키. 어떤 패키지에도 들어 있지 않은 **고객사 파일**이라
# 신버전 디렉터리에는 기본적으로 없다(업그레이드 때 승계해야 한다).
TLS_DIR="$ROOT_DIR/deploy/tls"
TLS_CRT="$TLS_DIR/server.crt"
TLS_KEY="$TLS_DIR/server.key"

DEFAULT_FRONTEND_PORT=3000
DEFAULT_BACKEND_PORT=8080
DEFAULT_SENSOR_PORT=8443
DEFAULT_TRUSTED_PROXIES="172.16.0.0/12"   # Docker 사용자 정의 네트워크 기본 풀
HEALTH_TIMEOUT=120
COMPOSE_HTTPS_MIN="2.24.4"                # ports: !override 지원 하한
DEFAULT_PROJECT_NAME="autocert"           # compose 프로젝트명 고정값 — 볼륨 이름의 접두어다

ASSUME_YES=""
NON_INTERACTIVE=""
CHECK_ONLY=""
SKIP_LOAD=""
FORCE_BUILD=""
REQUESTED_MODE=""
REQUESTED_SCHEME=""

ENV_TMP=""
COMPOSE_VERSION=""
COMPOSE_HTTPS_OK=""
IMAGES_PRESENT=""
BUNDLE_DIR=""
PRIMARY_IP="127.0.0.1"
COMPOSE_PROJECT=""

# 최종 안내에 쓰는 확정값
CFG_FRONTEND_PORT=""
CFG_BACKEND_PORT=""
CFG_SENSOR_PORT=""
CFG_SENSOR_ENABLED=0
# 접속 방식의 정본. http | https 둘 중 하나다 — "둘 다 동시 서비스"는 없다
# (HTTPS 구성은 80→443 리다이렉트를 이미 포함한다).
CFG_SCHEME="http"
CFG_HTTPS=0                # CFG_SCHEME의 내부 호환 미러(0|1). 판정은 CFG_SCHEME으로만 한다.
CFG_PUBLIC_HOST=""         # 접속 FQDN(HTTPS에서 묻는다). 비면 PRIMARY_IP를 쓴다.
COMPOSE_OVERLAYS=()        # dc()가 base 위에 얹을 오버레이 파일 목록

usage() {
	cat <<'EOF'
INHERIT AutoCert 설치 마법사

사용법:
  bash scripts/install.sh [옵션]

옵션:
  -y, --yes             전부 기본값으로 진행한다(질문 없음)
      --non-interactive 화면 조작 없이 진행한다(-y 포함, 판단이 필요하면 실패)
      --mode <모드>     install | https | reconfigure | status
      --check-only      사전 점검만 하고 끝낸다(아무것도 바꾸지 않는다)
      --skip-load       오프라인 번들 자동 적재를 건너뛴다
      --build           적재된 이미지가 없을 때 소스에서 빌드한다
  -h, --help            이 도움말

환경변수 오버라이드(설정하면 묻지 않는다):
  FRONTEND_PORT  BACKEND_PORT  SENSOR_GATEWAY_PORT
  SENSOR_ENABLED(1|0)  SENSOR_ADVERTISED_HOSTS  ADMIN_IP_TRUSTED_PROXIES
  POSTGRES_PASSWORD  (미설정이면 자동 생성)
  COMPOSE_PROJECT_NAME  (미설정이면 autocert 로 고정 — DB 볼륨 이름의 접두어다)

예:
  bash scripts/install.sh                          대화형 설치
  bash scripts/install.sh --check-only             설치 가능 여부만 확인
  bash scripts/install.sh --yes --mode install     무인 설치(전부 기본값)
EOF
}

die() {
	ui_err "$*"
	exit 1
}

cleanup() {
	tui_cleanup
	if [ -n "$ENV_TMP" ] && [ -f "$ENV_TMP" ]; then rm -f "$ENV_TMP"; fi
}

# --- 인자 --------------------------------------------------------------------

while [ $# -gt 0 ]; do
	case "$1" in
		-y | --yes) ASSUME_YES=1; shift ;;
		--non-interactive) ASSUME_YES=1; NON_INTERACTIVE=1; shift ;;
		--check-only) CHECK_ONLY=1; shift ;;
		--skip-load) SKIP_LOAD=1; shift ;;
		--build) FORCE_BUILD=1; shift ;;
		--mode)
			[ $# -ge 2 ] || { usage >&2; exit 2; }
			REQUESTED_MODE=$2; shift 2
			;;
		--mode=*) REQUESTED_MODE=${1#--mode=}; shift ;;
		-h | --help) usage; exit 0 ;;
		*) usage >&2; printf '알 수 없는 옵션: %s\n' "$1" >&2; exit 2 ;;
	esac
done

if [ -n "$ASSUME_YES" ]; then TUI_ASSUME_YES=1; fi
tui_init
trap cleanup EXIT

if [ -n "$NON_INTERACTIVE" ] && [ -z "$REQUESTED_MODE" ]; then
	REQUESTED_MODE="install"
fi

# --- 작은 도구들 --------------------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }

# ver_ge A B — A >= B 인가 (점 구분 3자리까지)
ver_ge() {
	local a b i xi yi
	a=$(printf '%s' "$1" | sed 's/^v//; s/[^0-9.].*$//')
	b=$(printf '%s' "$2" | sed 's/^v//; s/[^0-9.].*$//')
	local IFS=.
	# shellcheck disable=SC2206 # 점 분리가 목적이라 의도적인 분할이다
	local x=($a)
	# shellcheck disable=SC2206
	local y=($b)
	unset IFS
	for i in 0 1 2; do
		xi=${x[$i]:-0}; yi=${y[$i]:-0}
		xi=$((10#0$xi)); yi=$((10#0$yi))
		if [ "$xi" -gt "$yi" ]; then return 0; fi
		if [ "$xi" -lt "$yi" ]; then return 1; fi
	done
	return 0
}

# CFG_SCHEME을 세우는 유일한 통로다. 오버레이 목록도 여기서만 바뀐다 —
# 스킴 판정과 compose 인자가 어긋나는 것이 그 자체로 사고이기 때문이다.
set_scheme() {
	# shellcheck disable=SC2034 # CFG_HTTPS는 판정에 쓰지 않는 호환용 미러다(로그·디버깅용)
	case "$1" in
		https) CFG_SCHEME="https"; CFG_HTTPS=1 ;;
		*)     CFG_SCHEME="http";  CFG_HTTPS=0 ;;
	esac
	COMPOSE_OVERLAYS=()
	if [ "$CFG_SCHEME" = "https" ]; then COMPOSE_OVERLAYS+=("$COMPOSE_HTTPS_FILE"); fi
}

scheme_is_https() { [ "$CFG_SCHEME" = "https" ]; }

dc() {
	# 프로젝트명을 명시한다 — .env의 COMPOSE_PROJECT_NAME과 같은 값이지만, .env를 쓰기
	# 전(사전 점검·기존 스택 감지) 단계에서도 같은 프로젝트를 봐야 한다.
	local pargs=() fargs=(-f "$COMPOSE_FILE") f
	if [ -n "$COMPOSE_PROJECT" ]; then pargs=(-p "$COMPOSE_PROJECT"); fi
	# ${a[@]+"${a[@]}"} — 빈 배열을 set -u 에서 참조해도 안전한 형태(bash 4.2 대응).
	for f in ${COMPOSE_OVERLAYS[@]+"${COMPOSE_OVERLAYS[@]}"}; do
		fargs+=(-f "$f")
	done
	docker compose ${pargs[@]+"${pargs[@]}"} --project-directory "$ROOT_DIR" "${fargs[@]}" "$@"
}

# compose v2의 기본 프로젝트명 규칙: 소문자화 → [a-z0-9_-] 밖의 문자 제거 → 앞쪽 _- 제거.
compose_default_project_name() {
	basename -- "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9_-]//g; s/^[_-]*//'
}

# 이 프로젝트명으로 만들어진 DB 볼륨이나 컨테이너가 이미 있는가.
project_has_state() {
	local p=$1
	have docker || return 1
	docker info >/dev/null 2>&1 || return 1
	if docker volume ls -q 2>/dev/null | grep -qx "${p}_pgdata"; then return 0; fi
	if docker ps -aq --filter "label=com.docker.compose.project=$p" 2>/dev/null | grep -q .; then return 0; fi
	return 1
}

# Docker 볼륨 이름은 compose 프로젝트명이 접두어이고, 프로젝트명의 기본값은
# 디렉터리명에서 나온다. 패키지 루트가 곧 설치 루트라 신버전은 main_1.0.1/ 같은
# **다른 디렉터리**에 깔린다 — 고정하지 않으면 프로젝트명이 바뀌어 기존 pgdata 볼륨을
# 못 찾고 빈 DB로 뜬다. 그래서 .env에 고정값을 박는다.
resolve_project_name() {
	local cur derived
	cur=$(value_for COMPOSE_PROJECT_NAME "")
	if [ -n "$cur" ]; then
		# 이미 정해진 값은 절대 바꾸지 않는다 — 여기서 바꾸면 그게 곧 빈 DB 사고다.
		COMPOSE_PROJECT=$cur
		return 0
	fi
	derived=$(compose_default_project_name "$ROOT_DIR")
	if [ -n "$derived" ] && [ "$derived" != "$DEFAULT_PROJECT_NAME" ] && project_has_state "$derived"; then
		COMPOSE_PROJECT=$derived
		return 0
	fi
	COMPOSE_PROJECT=$DEFAULT_PROJECT_NAME
	return 0
}

# .env에 기록한다. resolve_project_name이 정한 값을 그대로 쓴다(재계산하지 않는다).
persist_project_name() {
	[ -n "$COMPOSE_PROJECT" ] || resolve_project_name
	env_set COMPOSE_PROJECT_NAME "$COMPOSE_PROJECT"
	ui_ok "compose 프로젝트명 고정: $COMPOSE_PROJECT (DB 볼륨 ${COMPOSE_PROJECT}_pgdata — 디렉터리를 옮겨도 유지된다)"
}

gen_secret_b64() {
	# URL 안전 문자만 쓴다 — DATABASE_URL의 userinfo에 그대로 들어가므로
	# '/'나 '+'가 섞이면 접속 문자열이 깨진다. '$'도 compose 보간에 걸린다.
	# \r까지 지우는 이유: 일부 환경의 openssl이 CRLF로 출력해 값 끝에 제어문자가 남는다.
	if have openssl; then
		openssl rand -base64 24 | tr '+/' '-_' | tr -d '=\n\r'
		return 0
	fi
	if [ -r /dev/urandom ]; then
		# head가 먼저 닫히면 tr이 SIGPIPE로 죽는다 — pipefail 때문에 실패로 잡히지 않게 격리한다.
		( set +o pipefail; LC_ALL=C tr -dc 'A-Za-z0-9_-' < /dev/urandom | head -c 32 )
		return 0
	fi
	return 1
}

gen_secret_hex32() {
	if have openssl; then
		openssl rand -hex 32 | tr -d '\n\r'
		return 0
	fi
	if have od && [ -r /dev/urandom ]; then
		od -An -tx1 -N32 /dev/urandom | tr -d ' \n\r'
		return 0
	fi
	return 1
}

port_valid() {
	case "$1" in
		"" | *[!0-9]*) ui_warn "포트는 숫자여야 한다: $1"; return 1 ;;
	esac
	if [ "$1" -lt 1 ] || [ "$1" -gt 65535 ]; then
		ui_warn "포트 범위는 1..65535 다: $1"
		return 1
	fi
	return 0
}

# 호스트에서 이미 LISTEN 중인 포트인가
port_in_use() {
	local p=$1
	if have ss; then
		if ss -ltnH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${p}\$"; then return 0; fi
		return 1
	fi
	if have netstat; then
		if netstat -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${p}\$"; then return 0; fi
		return 1
	fi
	# fd 3·4는 UI 전용이라 쓰지 않는다.
	if (exec 6<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null; then
		return 0
	fi
	return 1
}

# 우리 스택이 이미 잡고 있는 포트면 충돌로 보지 않는다.
port_ours() {
	local p=$1
	case " ${OUR_PORTS:-} " in
		*" $p "*) return 0 ;;
	esac
	return 1
}

# 호스트의 NIC IPv4 — 루프백과 docker/bridge/veth 계열은 뺀다.
detect_ips() {
	if have ip; then
		ip -4 -o addr show scope global 2>/dev/null |
			awk '{split($4,a,"/"); print $2" "a[1]}' |
			grep -vE '^(docker|br-|veth|virbr|cni|flannel|kube)' |
			awk '{print $2}'
		return 0
	fi
	if have ifconfig; then
		ifconfig 2>/dev/null | awk '/inet /{gsub(/addr:/,"",$2); print $2}' | grep -v '^127\.'
		return 0
	fi
	if have hostname; then
		hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+\.' || true
	fi
}

detect_primary_ip() {
	local ip=""
	if have ip; then
		ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -1)
	fi
	if [ -z "$ip" ]; then ip=$(detect_ips | head -1); fi
	if [ -z "$ip" ]; then ip="127.0.0.1"; fi
	printf '%s' "$ip"
}

http_ok() {
	local url=$1
	if have curl; then
		curl -fsS -o /dev/null --max-time 3 "$url" 2>/dev/null
		return $?
	fi
	if have wget; then
		wget -q -O /dev/null -T 3 "$url" 2>/dev/null
		return $?
	fi
	_http_ok_bash "$url"
}

_http_ok_bash() {
	local url=$1 hostport path host port line
	url=${url#http://}
	hostport=${url%%/*}
	case "$url" in
		*/*) path="/${url#*/}" ;;
		*) path="/" ;;
	esac
	host=${hostport%%:*}
	port=${hostport##*:}
	if [ "$port" = "$host" ]; then port=80; fi
	if ! (exec 5<>"/dev/tcp/$host/$port") 2>/dev/null; then return 1; fi
	exec 5<>"/dev/tcp/$host/$port"
	printf 'GET %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n' "$path" "$host" >&5
	line=""
	IFS= read -r line <&5 || true
	exec 5>&- 5<&- 2>/dev/null || true
	case "$line" in
		*" 200"*) return 0 ;;
	esac
	return 1
}

# --- .env 편집 ---------------------------------------------------------------

# env_open [시드파일] — 작업용 사본을 연다.
# 시드를 생략하면 설치 루트의 .env 다. 패치 모드는 **구버전 .env**를 시드로 넘긴다.
env_open() {
	local seed=${1:-$ENV_FILE}
	if [ -n "$ENV_TMP" ] && [ -f "$ENV_TMP" ]; then rm -f "$ENV_TMP"; fi
	ENV_TMP=$(mktemp "${TMPDIR:-/tmp}/autocert-env.XXXXXX")
	chmod 600 "$ENV_TMP"
	if [ -f "$seed" ]; then
		cat "$seed" > "$ENV_TMP"
	else
		{
			printf '# INHERIT AutoCert 설정 — scripts/install.sh 가 생성했다.\n'
			printf '# 변수 설명은 docs/manual/INSTALL.md 5.2절.\n'
			printf '# 이 파일에는 비밀값이 들어 있다. 권한 600을 유지하고 저장소에 커밋하지 않는다.\n'
		} > "$ENV_TMP"
	fi
}

# file_env_get 파일 키 — 임의의 .env 파일에서 값 하나를 읽는다(작업 사본과 무관).
file_env_get() {
	local f=$1 k=$2 line
	[ -f "$f" ] || return 0
	line=$(grep -m1 "^${k}=" "$f" 2>/dev/null || true)
	line=${line#*=}
	line=${line%$'\r'}   # Windows에서 편집된 .env 대비
	# 값이 따옴표로 감싸여 있으면 벗긴다.
	case "$line" in
		\"*\") line=${line#\"}; line=${line%\"} ;;
		\'*\') line=${line#\'}; line=${line%\'} ;;
	esac
	printf '%s' "$line"
}

env_get() {
	[ -n "$ENV_TMP" ] || return 0
	file_env_get "$ENV_TMP" "$1"
}

env_set() {
	local k=$1 v=$2
	if grep -q "^${k}=" "$ENV_TMP" 2>/dev/null; then
		awk -v k="$k" -v v="$v" '
			BEGIN { done = 0 }
			{
				if (!done && index($0, k "=") == 1) { print k "=" v; done = 1 }
				else { print }
			}
		' "$ENV_TMP" > "$ENV_TMP.new"
		mv "$ENV_TMP.new" "$ENV_TMP"
		chmod 600 "$ENV_TMP"
	else
		printf '%s=%s\n' "$k" "$v" >> "$ENV_TMP"
	fi
}

# 키의 **행 자체**를 지운다. 빈 값으로 두는 것과 다르다 — 예를 들어
# AUTOCERT_VERSION은 값이 남아 있으면 신버전 이미지 태그가 구버전으로 고정된다.
env_unset() {
	local k=$1
	[ -f "$ENV_TMP" ] || return 0
	grep -q "^${k}=" "$ENV_TMP" 2>/dev/null || return 0
	awk -v k="$k" 'index($0, k "=") == 1 { next } { print }' "$ENV_TMP" > "$ENV_TMP.new"
	mv "$ENV_TMP.new" "$ENV_TMP"
	chmod 600 "$ENV_TMP"
}

# Windows에서 편집·전송된 .env는 값 끝에 CR이 남아 컨테이너가 "8080\r" 같은 값을 본다.
env_normalize_crlf() {
	[ -f "$ENV_TMP" ] || return 0
	grep -q $'\r' "$ENV_TMP" 2>/dev/null || return 0
	sed 's/\r$//' "$ENV_TMP" > "$ENV_TMP.new"
	mv "$ENV_TMP.new" "$ENV_TMP"
	chmod 600 "$ENV_TMP"
	ui_note ".env의 CRLF 줄끝을 정규화했다."
}

env_commit() {
	if [ -f "$ENV_FILE" ]; then
		local backup
		backup="$ENV_FILE.bak.$(date +%Y%m%d-%H%M%S)"
		cp "$ENV_FILE" "$backup"
		chmod 600 "$backup"
		ui_ok ".env 백업: $(basename "$backup")"
	fi
	cat "$ENV_TMP" > "$ENV_FILE"
	chmod 600 "$ENV_FILE"
	ui_ok ".env 기록 완료 (권한 600)"

	if have git && git -C "$ROOT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
		if ! git -C "$ROOT_DIR" check-ignore -q .env 2>/dev/null; then
			ui_warn ".env 가 .gitignore에 없다 — 커밋되지 않도록 직접 확인한다."
		fi
	fi
}

# 환경변수 오버라이드 > 기존 .env > 기본값
value_for() {
	local key=$1 fallback=$2 cur
	cur=${!key:-}
	if [ -n "$cur" ]; then printf '%s' "$cur"; return 0; fi
	cur=$(env_get "$key")
	if [ -n "$cur" ]; then printf '%s' "$cur"; return 0; fi
	printf '%s' "$fallback"
}

overridden() {
	local key=$1 cur
	cur=${!key:-}
	[ -n "$cur" ]
}

# --- 사전 점검 ---------------------------------------------------------------

PRE_FATAL=0
PRE_HINTS=()

pre_hint() { PRE_HINTS+=("$1"); }

preflight() {
	ui_title "사전 점검"
	PRE_FATAL=0
	PRE_HINTS=()

	# 1) 소스 트리
	if [ -f "$COMPOSE_FILE" ]; then
		ui_row ok "compose 파일" "$(basename "$COMPOSE_FILE")"
	else
		ui_row fail "compose 파일" "없음"
		pre_hint "docker-compose.yml 이 없다. 번들의 source/autocert-source-*.tar.gz 를 푼 디렉터리에서 실행한다."
		PRE_FATAL=1
	fi

	# 1-2) 제품 소스 (패키지 배치에서는 install.sh가 전개한다)
	if source_tree_present; then
		ui_row ok "제품 소스" "전개됨 (backend/ · frontend/)"
	elif [ "$PACKAGE_MODE" -eq 1 ] && source_archive_path >/dev/null; then
		ui_row info "제품 소스" "아카이브 있음 — 설치 시 이 디렉터리에 전개한다"
	elif [ "$PACKAGE_MODE" -eq 1 ]; then
		ui_row warn "제품 소스" "source/autocert-source-*.tar.gz 없음"
		pre_hint "소스 아카이브가 없으면 --build 와 HTTPS 오버레이(deploy/tls)를 쓸 수 없다. 패키지를 다시 반입한다."
	fi

	# 1-3) compose 프로젝트명 — 볼륨(DB) 이름의 접두어라 버전 올릴 때 바뀌면 안 된다.
	ui_row info "compose 프로젝트" "$COMPOSE_PROJECT"

	# 2) docker
	if have docker; then
		ui_row ok "docker" "$(docker --version 2>/dev/null | head -1)"
	else
		ui_row fail "docker" "PATH에 없음"
		pre_hint "docker 를 설치한다. 폐쇄망이면 배포판 오프라인 패키지(docker-ce)로 설치한다."
		PRE_FATAL=1
	fi

	# 3) docker 데몬
	if have docker && docker info >/dev/null 2>&1; then
		ui_row ok "docker 데몬" "연결됨"
	elif have docker; then
		ui_row fail "docker 데몬" "연결 불가"
		pre_hint "데몬을 켜거나 권한을 준다: sudo systemctl start docker / sudo usermod -aG docker \$USER (재로그인 필요)."
		pre_hint "docker 그룹은 사실상 root 권한이다 — 아무에게나 주지 않는다."
		PRE_FATAL=1
	fi

	# 4) docker compose v2
	COMPOSE_VERSION=""
	if have docker && docker compose version >/dev/null 2>&1; then
		COMPOSE_VERSION=$(docker compose version --short 2>/dev/null | tr -d '\r')
		ui_row ok "docker compose v2" "${COMPOSE_VERSION:-unknown}"
	else
		ui_row fail "docker compose v2" "없음"
		if have docker-compose; then
			pre_hint "docker-compose(v1)만 있다. 이 제품은 compose v2 플러그인이 필요하다 — docker-compose-plugin 을 설치한다."
		else
			pre_hint "docker compose v2 플러그인을 설치한다(docker-compose-plugin)."
		fi
		PRE_FATAL=1
	fi

	# 5) HTTPS 오버레이 지원 여부
	COMPOSE_HTTPS_OK=""
	if [ -n "$COMPOSE_VERSION" ]; then
		if ver_ge "$COMPOSE_VERSION" "$COMPOSE_HTTPS_MIN"; then
			COMPOSE_HTTPS_OK=1
			ui_row ok "HTTPS 오버레이 지원" "compose >= $COMPOSE_HTTPS_MIN"
		else
			ui_row warn "HTTPS 오버레이 지원" "compose < $COMPOSE_HTTPS_MIN — ports: !override 불가"
			pre_hint "HTTPS를 쓰려면 compose를 ${COMPOSE_HTTPS_MIN} 이상으로 올린다(HTTP 설치 자체는 지장 없다)."
		fi
	fi

	# 6) 디스크
	local target free_gb
	target="$ROOT_DIR"
	if have docker && docker info >/dev/null 2>&1; then
		target=$(docker info -f '{{.DockerRootDir}}' 2>/dev/null || printf '%s' "$ROOT_DIR")
		[ -d "$target" ] || target="$ROOT_DIR"
	fi
	free_gb=$(df -Pk "$target" 2>/dev/null | awk 'NR==2 {printf "%d", $4/1048576}')
	if [ -z "$free_gb" ]; then
		ui_row info "디스크 여유" "확인 불가 ($target)"
	elif [ "$free_gb" -ge 20 ]; then
		ui_row ok "디스크 여유" "${free_gb}GB ($target)"
	else
		ui_row warn "디스크 여유" "${free_gb}GB — 권장 20GB ($target)"
		pre_hint "이미지 적재와 DB 증가분까지 20GB를 권장한다. 부족하면 docker system prune 또는 볼륨 이전을 검토한다."
	fi

	# 7) 메모리
	local mem_mb=""
	if [ -r /proc/meminfo ]; then
		mem_mb=$(awk '/^MemTotal:/ {printf "%d", $2/1024}' /proc/meminfo)
	elif have sysctl; then
		mem_mb=$(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%d", $1/1048576}')
	fi
	if [ -z "$mem_mb" ]; then
		ui_row info "메모리" "확인 불가"
	elif [ "$mem_mb" -ge 3800 ]; then
		ui_row ok "메모리" "$((mem_mb / 1024))GB"
	else
		ui_row warn "메모리" "${mem_mb}MB — 권장 4GB"
		pre_hint "메모리가 4GB 미만이면 스캔·DB가 동시에 돌 때 OOM 위험이 있다."
	fi

	# 8) 이미지 적재 여부
	IMAGES_PRESENT=""
	if have docker && docker info >/dev/null 2>&1; then
		if images_present; then
			IMAGES_PRESENT=1
			ui_row ok "제품 이미지" "적재됨 ($(image_tag backend) 외 1)"
		else
			BUNDLE_DIR=$(find_bundle_dir || true)
			if [ -n "$BUNDLE_DIR" ]; then
				ui_row info "제품 이미지" "미적재 — 번들 발견: $BUNDLE_DIR (설치 중 자동 적재)"
			else
				ui_row warn "제품 이미지" "미적재 — 번들도 찾지 못했다"
				pre_hint "오프라인 번들 디렉터리(SHA256SUMS·MANIFEST.txt가 있는 곳)에서 sh load-offline.sh 를 먼저 실행하거나, --build 로 소스 빌드를 지시한다."
			fi
		fi
	fi

	ui_hr
	local h
	if [ ${#PRE_HINTS[@]} -gt 0 ]; then
		ui_step "조치 안내"
		for h in "${PRE_HINTS[@]}"; do ui_info "- $h"; done
	fi

	if [ "$PRE_FATAL" -eq 1 ]; then
		ui_blank
		ui_err "치명적 항목이 있어 진행할 수 없다. 위 안내를 처리하고 다시 실행한다."
		return 1
	fi
	ui_ok "필수 조건 충족"
	return 0
}

image_tag() {
	local svc=$1 prefix version
	prefix=$(value_for AUTOCERT_IMAGE_PREFIX "inherit-autocert")
	version=$(value_for AUTOCERT_VERSION "$(cat "$VERSION_FILE" 2>/dev/null || printf 'latest')")
	printf '%s/%s:%s' "$prefix" "$svc" "$version"
}

images_present() {
	docker image inspect "$(image_tag backend)" >/dev/null 2>&1 &&
		docker image inspect "$(image_tag frontend)" >/dev/null 2>&1
}

# --- 소스 전개 (패키지 배치 전용) --------------------------------------------
#
# 반입 패키지는 이미지·설치 스크립트만 최상위에 두고 제품 소스는
# source/autocert-source-<버전>.tar.gz 하나로 담는다(반입 심사 열람용으로
# docker-compose.yml·.env.example 사본만 미리 꺼내 둔다). 여기서 그 아카이브를
# **패키지 루트에** 전개하면 두 사본이 같은 바이트로 덮이고, compose의
# `build: ./backend` · deploy/tls 마운트 컨텍스트가 실재하게 된다.

source_archive_path() {
	local v c
	v=$(tr -d ' \t\r\n' < "$VERSION_FILE" 2>/dev/null || printf '')
	if [ -n "$v" ] && [ -f "$ROOT_DIR/source/autocert-source-$v.tar.gz" ]; then
		printf '%s' "$ROOT_DIR/source/autocert-source-$v.tar.gz"
		return 0
	fi
	for c in "$ROOT_DIR"/source/autocert-source-*.tar.gz; do
		[ -f "$c" ] || continue
		printf '%s' "$c"
		return 0
	done
	return 1
}

source_tree_present() {
	[ -f "$ROOT_DIR/backend/go.mod" ] && [ -f "$ROOT_DIR/frontend/package.json" ]
}

# 아카이브 최상위는 autocert-<버전>/ 한 겹이다. 그 한 겹을 벗겨 ROOT_DIR에 푼다.
extract_source_archive() {
	local archive=$1 tmp top
	if tar xzf "$archive" -C "$ROOT_DIR" --strip-components=1 2>/dev/null; then
		return 0
	fi
	# busybox tar 등 --strip-components 미지원 환경 폴백: 임시 디렉터리에 풀고 옮긴다.
	ui_note "tar가 --strip-components를 지원하지 않는다 — 임시 디렉터리를 경유해 전개한다."
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/autocert-src.XXXXXX") || return 1
	if ! tar xzf "$archive" -C "$tmp"; then rm -rf "$tmp"; return 1; fi
	top=$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | head -1)
	if [ -z "$top" ]; then rm -rf "$tmp"; return 1; fi
	# "$top/." 로 복사하면 점 파일(.env.example 등)까지 한 번에 옮겨진다.
	if ! cp -R "$top/." "$ROOT_DIR/"; then rm -rf "$tmp"; return 1; fi
	rm -rf "$tmp"
	return 0
}

ensure_source_tree() {
	[ "$PACKAGE_MODE" -eq 1 ] || return 0

	local archive save
	if ! archive=$(source_archive_path); then
		if source_tree_present; then return 0; fi
		ui_warn "source/autocert-source-*.tar.gz 를 찾지 못했다 — 소스 전개를 건너뛴다."
		ui_info "적재된 이미지만으로 기동은 되지만, --build 와 HTTPS 오버레이는 소스가 있어야 한다."
		return 0
	fi

	if source_tree_present; then
		ui_ok "소스 트리 확인됨 — 이미 전개되어 있어 다시 풀지 않는다"
		return 0
	fi

	ui_step "제품 소스를 설치 루트에 전개한다: source/$(basename "$archive")"
	# 패키지의 VERSION은 실제로 담긴 이미지 태그의 정본이다(--version 으로 덮어쓴 빌드가
	# 있다). 아카이브 안의 VERSION으로 덮이면 이미지 태그 판정이 어긋나므로 되돌린다.
	save=$(mktemp "${TMPDIR:-/tmp}/autocert-version.XXXXXX") || save=""
	if [ -n "$save" ] && [ -f "$VERSION_FILE" ]; then cp "$VERSION_FILE" "$save"; else save=""; fi

	if ! extract_source_archive "$archive"; then
		[ -n "$save" ] && rm -f "$save"
		die "소스 아카이브 전개에 실패했다: $archive"
	fi
	if [ -n "$save" ]; then
		if ! cmp -s "$save" "$VERSION_FILE" 2>/dev/null; then
			cp "$save" "$VERSION_FILE"
			ui_note "VERSION은 패키지 값을 유지했다(이미지 태그 정본)."
		fi
		rm -f "$save"
	fi

	if [ ! -f "$COMPOSE_FILE" ] || [ ! -d "$ROOT_DIR/backend" ] || [ ! -d "$ROOT_DIR/frontend" ]; then
		die "소스 전개 결과가 온전하지 않다 — docker-compose.yml·backend/·frontend/ 가 모두 있어야 한다."
	fi
	ui_ok "소스 전개 완료 (backend/ · frontend/ · deploy/ · docker-compose.yml)"
}

# 번들 디렉터리(SHA256SUMS·MANIFEST.txt·load-offline.sh가 함께 있는 곳)를 찾는다.
find_bundle_dir() {
	local c
	# 패키지 배치에서는 설치 루트가 곧 번들 디렉터리다.
	if [ "$PACKAGE_MODE" -eq 1 ] && [ -f "$ROOT_DIR/load-offline.sh" ]; then
		printf '%s' "$ROOT_DIR"
		return 0
	fi
	for c in \
		"$PWD" "$PWD"/* \
		"$ROOT_DIR" "$ROOT_DIR"/.. "$ROOT_DIR"/../* \
		"$ROOT_DIR"/../.. "$ROOT_DIR"/../../*; do
		[ -d "$c" ] || continue
		if [ -f "$c/SHA256SUMS" ] && [ -f "$c/MANIFEST.txt" ] && [ -f "$c/load-offline.sh" ]; then
			(CDPATH='' cd -- "$c" && pwd)
			return 0
		fi
	done
	return 1
}

ensure_images() {
	if [ -n "$IMAGES_PRESENT" ] || images_present; then
		ui_ok "제품 이미지 확인됨"
		return 0
	fi

	if [ -z "$SKIP_LOAD" ]; then
		[ -n "$BUNDLE_DIR" ] || BUNDLE_DIR=$(find_bundle_dir || true)
		if [ -n "$BUNDLE_DIR" ]; then
			ui_step "오프라인 번들을 적재한다: $BUNDLE_DIR"
			# --no-next-steps: 번들의 "다음 단계" 안내가 마법사 진행 중간에 끼어들지 않게 한다.
			if sh "$BUNDLE_DIR/load-offline.sh" --no-next-steps >&3 2>&3; then
				if images_present; then
					ui_ok "이미지 적재 완료"
					return 0
				fi
				ui_warn "적재는 끝났지만 기대한 태그를 찾지 못했다: $(image_tag backend)"
			else
				ui_err "load-offline.sh 실패 — 체크섬 불일치라면 매체를 다시 받아 재시도한다."
			fi
		fi
	fi

	if [ -n "$FORCE_BUILD" ]; then
		ui_step "소스에서 이미지를 빌드한다 (시간이 오래 걸린다)"
		dc build
		return 0
	fi

	if [ -d "$ROOT_DIR/backend" ] && [ -d "$ROOT_DIR/frontend" ]; then
		if tui_confirm "적재된 이미지가 없다. 지금 소스에서 빌드할까? (오래 걸리고 인터넷이 필요하다)" no; then
			dc build
			return 0
		fi
	fi

	die "실행할 이미지가 없다. 번들 디렉터리에서 'sh load-offline.sh'를 먼저 돌리거나 --build 를 준다."
}

# --- 설정 문답 ---------------------------------------------------------------

ask_ports() {
	local fp bp cur_fp cur_bp
	cur_fp=$(value_for FRONTEND_PORT "$DEFAULT_FRONTEND_PORT")
	cur_bp=$(value_for BACKEND_PORT "$DEFAULT_BACKEND_PORT")

	if overridden FRONTEND_PORT && overridden BACKEND_PORT; then
		fp=$cur_fp; bp=$cur_bp
	else
		tui_select "호스트 포트를 어떻게 할까?" 0 \
			"기본값 사용 (웹 ${cur_fp} / API ${cur_bp})" \
			"변경한다"
		if [ "$TUI_INDEX" -eq 0 ]; then
			fp=$cur_fp; bp=$cur_bp
		else
			tui_input "웹 콘솔 포트" "$cur_fp" port_valid; fp=$TUI_ANSWER
			tui_input "API 포트" "$cur_bp" port_valid; bp=$TUI_ANSWER
		fi
	fi

	fp=$(resolve_port "웹 콘솔" "$fp" "$cur_fp")
	bp=$(resolve_port "API" "$bp" "$cur_bp")
	if [ "$fp" = "$bp" ]; then
		ui_warn "웹과 API 포트가 같다 — API를 $((bp + 1)) 로 옮긴다."
		bp=$((bp + 1))
	fi

	env_set FRONTEND_PORT "$fp"
	env_set BACKEND_PORT "$bp"
	CFG_FRONTEND_PORT=$fp
	CFG_BACKEND_PORT=$bp
}

# 포트가 이미 LISTEN 중이면 경고하고 다시 묻는다. 우리 스택이 쓰던 포트는 통과.
resolve_port() {
	local label=$1 port=$2 fallback=$3
	while port_in_use "$port"; do
		if port_ours "$port"; then break; fi
		ui_warn "${label} 포트 ${port} 는 이미 다른 프로세스가 LISTEN 중이다."
		if [ "$TUI_MODE" = "none" ] || [ -n "$ASSUME_YES" ]; then
			ui_warn "비대화 모드라 그대로 진행한다 — 기동에 실패하면 포트를 바꿔 다시 실행한다."
			break
		fi
		tui_input "${label}에 쓸 다른 포트" "$((port + 1))" port_valid
		port=$TUI_ANSWER
		if [ -z "$port" ]; then port=$fallback; fi
	done
	printf '%s' "$port"
}

ask_sensor() {
	local enabled=0 cur_key cur_hosts cur_port sp saved
	cur_key=$(env_get SENSOR_CA_ENC_KEY)
	saved=$(env_get SENSOR_CA_ENC_KEY_DISABLED)
	cur_hosts=$(value_for SENSOR_ADVERTISED_HOSTS "")
	cur_port=$(value_for SENSOR_GATEWAY_PORT "$DEFAULT_SENSOR_PORT")

	if overridden SENSOR_ENABLED; then
		case "${SENSOR_ENABLED:-}" in
			1 | true | yes) enabled=1 ;;
			*) enabled=0 ;;
		esac
	else
		local def=1
		if [ -n "$cur_key" ]; then def=0; fi
		tui_select "센서(에이전트) 게이트웨이를 쓸까? — 서버가 직접 못 닿는 망의 인증서를 센서가 보고한다" "$def" \
			"예 — 게이트웨이를 연다 (포트 ${cur_port}/mTLS)" \
			"아니오 — 네트워크 스캔만 쓴다"
		if [ "$TUI_INDEX" -eq 0 ]; then enabled=1; fi
	fi

	if [ "$enabled" -eq 0 ]; then
		if [ -n "$cur_key" ]; then
			# 봉인 키를 잃으면 등록된 센서가 전부 재등록 대상이 된다 — 지우지 않고 보관한다.
			env_set SENSOR_CA_ENC_KEY_DISABLED "$cur_key"
			env_set SENSOR_CA_ENC_KEY ""
			ui_warn "센서 게이트웨이를 껐다. 기존 봉인 키는 .env의 SENSOR_CA_ENC_KEY_DISABLED에 보관했다(다시 켜면 자동 복구)."
		else
			env_set SENSOR_CA_ENC_KEY ""
		fi
		CFG_SENSOR_ENABLED=0
		return 0
	fi

	# 키: 기존 → 보관본 → 신규 생성
	if [ -z "$cur_key" ] && [ -n "$saved" ]; then
		cur_key=$saved
		ui_ok "이전에 쓰던 센서 CA 봉인 키를 복구했다 — 등록된 센서는 재등록이 필요 없다."
	fi
	if [ -z "$cur_key" ]; then
		cur_key=$(gen_secret_hex32) || die "난수 생성 도구가 없다(openssl 또는 /dev/urandom 필요)."
		ui_ok "센서 CA 봉인 키 자동 생성됨 (저장 위치: .env — 화면에 출력하지 않는다)"
	fi
	env_set SENSOR_CA_ENC_KEY "$cur_key"

	sp=$(resolve_port "센서 게이트웨이" "$cur_port" "$DEFAULT_SENSOR_PORT")
	env_set SENSOR_GATEWAY_PORT "$sp"
	CFG_SENSOR_PORT=$sp
	CFG_SENSOR_ENABLED=1

	ask_advertised_hosts "$cur_hosts"
}

# SENSOR_ADVERTISED_HOSTS — 센서가 접속할 주소(서버 인증서 SAN). 포트는 붙이지 않는다.
ask_advertised_hosts() {
	local cur=$1 hosts="" ips item
	if overridden SENSOR_ADVERTISED_HOSTS; then
		env_set SENSOR_ADVERTISED_HOSTS "${SENSOR_ADVERTISED_HOSTS}"
		ui_ok "센서 접속 주소(환경변수 지정): ${SENSOR_ADVERTISED_HOSTS}"
		return 0
	fi

	ips=$(detect_ips || true)
	local items=()
	for item in $ips; do items+=("$item"); done
	# 기존 값 중 목록에 없는 것(호스트명 등)도 후보로 올린다.
	local old
	old=$(printf '%s' "$cur" | tr ',' ' ')
	for item in $old; do
		case " ${items[*]:-} " in
			*" $item "*) : ;;
			*) items+=("$item") ;;
		esac
	done
	items+=("직접 입력…")

	if [ ${#items[@]} -eq 1 ]; then
		ui_warn "NIC IP를 자동으로 찾지 못했다."
		tui_input "센서가 접속할 서버 주소(콤마 구분, 포트 없이)" "${cur:-$PRIMARY_IP}"
		hosts=$TUI_ANSWER
	else
		tui_multi "센서가 접속할 이 서버의 주소를 고른다 (서버 인증서 SAN에 들어간다 · 포트는 붙이지 않는다)" \
			"$(preselect_indices "$cur" "${items[@]}")" "${items[@]}"
		local line
		while IFS= read -r line; do
			[ -n "$line" ] || continue
			if [ "$line" = "직접 입력…" ]; then
				tui_input "추가로 넣을 주소(콤마 구분)" ""
				line=$TUI_ANSWER
				[ -n "$line" ] || continue
			fi
			hosts="${hosts}${hosts:+,}${line}"
		done <<EOF
$TUI_SELECTED
EOF
	fi

	# 포트가 붙은 항목은 백엔드가 경고와 함께 버린다 — 여기서 미리 떼어낸다.
	hosts=$(printf '%s' "$hosts" | tr ',' '\n' | sed 's/[[:space:]]//g; s/:[0-9][0-9]*$//' | grep -v '^$' | paste -sd, - 2>/dev/null || printf '%s' "$hosts")
	env_set SENSOR_ADVERTISED_HOSTS "$hosts"
	if [ -n "$hosts" ]; then
		ui_ok "센서 접속 주소: $hosts"
	else
		ui_warn "센서 접속 주소가 비었다 — 센서 등록 시 서버 인증서 검증에 실패할 수 있다."
	fi
}

# 기존 값에 들어 있던 항목을 체크박스 기본 선택으로 만든다. 없으면 전체 선택.
preselect_indices() {
	local cur=$1
	shift
	local items=("$@") i out=""
	if [ -z "$cur" ]; then
		for ((i = 0; i < ${#items[@]} - 1; i++)); do out="${out}${out:+ }$i"; done
		printf '%s' "${out:-none}"
		return 0
	fi
	for ((i = 0; i < ${#items[@]}; i++)); do
		case ",$cur," in
			*",${items[$i]},"*) out="${out}${out:+ }$i" ;;
		esac
	done
	printf '%s' "${out:-none}"
}

# 브라우저 → frontend → backend 중계라, 백엔드가 보는 출발지는 frontend 컨테이너다.
# 이 값을 안 넣으면 관리 콘솔 접근 IP 제한이 브라우저 IP 기준으로 동작하지 않는다.
inject_trusted_proxies() {
	local cur
	cur=$(value_for ADMIN_IP_TRUSTED_PROXIES "")
	if [ -n "$cur" ]; then
		env_set ADMIN_IP_TRUSTED_PROXIES "$cur"
		ui_ok "신뢰 프록시 대역 유지: $cur"
		return 0
	fi
	env_set ADMIN_IP_TRUSTED_PROXIES "$DEFAULT_TRUSTED_PROXIES"
	ui_ok "신뢰 프록시 대역 자동 설정: $DEFAULT_TRUSTED_PROXIES (compose 네트워크 — 접근 IP 제한이 브라우저 IP로 동작하게 한다)"
}

# 기동 후 실제 compose 네트워크 대역을 확인해 필요하면 보정한다.
fix_trusted_proxies_after_up() {
	local cid net subnet cur
	cid=$(dc ps -q backend 2>/dev/null | head -1)
	[ -n "$cid" ] || return 0
	net=$(docker inspect "$cid" -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' 2>/dev/null | awk '{print $1}')
	[ -n "$net" ] || return 0
	subnet=$(docker network inspect "$net" -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null | awk '{print $1}')
	[ -n "$subnet" ] || return 0

	case "$subnet" in
		172.1[6-9].* | 172.2[0-9].* | 172.3[01].*) return 0 ;;  # 기본값에 포함된다
	esac
	cur=$(env_get ADMIN_IP_TRUSTED_PROXIES)
	case ",$cur," in
		*",$subnet,"*) return 0 ;;
	esac

	ui_warn "compose 네트워크 대역이 기본 풀 밖이다($subnet) — 신뢰 프록시 목록에 추가하고 백엔드를 재기동한다."
	env_open
	env_set ADMIN_IP_TRUSTED_PROXIES "${cur}${cur:+,}${subnet}"
	env_commit
	dc up -d backend >&3 2>&3 || true
}

# --- 기동/확인 ---------------------------------------------------------------

stack_running() {
	local ids
	ids=$(dc ps -q 2>/dev/null || true)
	[ -n "$ids" ]
}

collect_our_ports() {
	OUR_PORTS=""
	local p
	for p in $(env_get FRONTEND_PORT) $(env_get BACKEND_PORT) $(env_get SENSOR_GATEWAY_PORT); do
		[ -n "$p" ] || continue
		OUR_PORTS="${OUR_PORTS}${OUR_PORTS:+ }$p"
	done
	if stack_running; then :; else OUR_PORTS=""; fi
}

compose_up() {
	ui_step "컨테이너를 기동한다 (docker compose up -d)"
	if ! dc up -d >&3 2>&3; then
		ui_err "docker compose up 이 실패했다."
		show_failure_logs
		exit 1
	fi
	ui_ok "컨테이너 기동 명령 완료"
}

wait_health() {
	local url="http://127.0.0.1:${CFG_BACKEND_PORT}/healthz"
	local weburl="http://127.0.0.1:${CFG_FRONTEND_PORT}/"
	local waited=0 ok_api=0 ok_web=0

	ui_step "기동 확인 (최대 ${HEALTH_TIMEOUT}초)"
	while [ "$waited" -lt "$HEALTH_TIMEOUT" ]; do
		if [ "$ok_api" -eq 0 ] && http_ok "$url"; then
			ok_api=1
			printf '\r\033[2K' >&3
			ui_ok "백엔드 /healthz 응답 (${waited}초)"
		fi
		if [ "$ok_api" -eq 1 ] && [ "$ok_web" -eq 0 ] && http_ok "$weburl"; then
			ok_web=1
			printf '\r\033[2K' >&3
			ui_ok "웹 콘솔 응답 (${waited}초)"
		fi
		if [ "$ok_api" -eq 1 ] && [ "$ok_web" -eq 1 ]; then return 0; fi
		printf '\r  %s' "대기 중… ${waited}초 (api=$( [ $ok_api -eq 1 ] && printf ok || printf '…') web=$( [ $ok_web -eq 1 ] && printf ok || printf '…'))" >&3
		sleep 3
		waited=$((waited + 3))
	done

	printf '\r\033[2K' >&3
	ui_err "${HEALTH_TIMEOUT}초 안에 기동을 확인하지 못했다 (api=$ok_api web=$ok_web)."
	show_failure_logs
	return 1
}

show_failure_logs() {
	ui_blank
	ui_step "최근 로그에서 실패로 보이는 줄"
	local logs picked
	logs=$(dc logs --tail=50 2>&1 || true)
	picked=$(printf '%s\n' "$logs" | grep -iE 'error|fatal|panic|refused|denied|failed|cannot|no such|bind|exited|unhealthy' | tail -20 || true)
	if [ -n "$picked" ]; then
		printf '%s\n' "$picked" | sed 's/^/    /' >&3
	else
		printf '%s\n' "$logs" | tail -20 | sed 's/^/    /' >&3
	fi
	ui_blank
	ui_info "전체 로그: docker compose logs -f"
	ui_info "상태 확인: docker compose ps"
}

print_status() {
	ui_title "상태"
	dc ps >&3 2>&3 || true
	ui_blank
	local fp bp
	fp=$(env_get FRONTEND_PORT); fp=${fp:-$DEFAULT_FRONTEND_PORT}
	bp=$(env_get BACKEND_PORT); bp=${bp:-$DEFAULT_BACKEND_PORT}
	if http_ok "http://127.0.0.1:${bp}/healthz"; then
		ui_ok "백엔드 /healthz 정상"
	else
		ui_warn "백엔드 /healthz 무응답 (포트 ${bp})"
	fi
	if http_ok "http://127.0.0.1:${fp}/"; then
		ui_ok "웹 콘솔 응답 정상"
	else
		ui_warn "웹 콘솔 무응답 (포트 ${fp})"
	fi
	ui_info "접속: http://${PRIMARY_IP}:${fp}"
}

print_done() {
	local webport="$CFG_FRONTEND_PORT" host
	host=${CFG_PUBLIC_HOST:-$PRIMARY_IP}
	if scheme_is_https; then
		webport=$(env_get HTTPS_PORT); webport=${webport:-443}
	fi

	ui_blank
	ui_title "설치 완료"
	if [ "$webport" = "80" ] || [ "$webport" = "443" ]; then
		ui_step "접속 주소:  ${CFG_SCHEME}://${host}"
	else
		ui_step "접속 주소:  ${CFG_SCHEME}://${host}:${webport}"
	fi
	ui_info "브라우저로 열어 첫 화면에서 관리자 계정을 만든다 (초기 비밀번호 같은 건 없다)."
	if scheme_is_https; then
		ui_note "HSTS는 고객사의 신뢰된 인증서가 설치된 뒤에 켠다 — 자체 서명 상태에서 켜면 콘솔이 잠긴다."
	fi
	ui_blank

	ui_step "방화벽에서 열 포트"
	ui_info "- ${webport}/tcp  관리 콘솔 (필수)"
	if scheme_is_https; then
		local rport
		rport=$(env_get HTTP_REDIRECT_PORT); rport=${rport:-80}
		ui_info "- ${rport}/tcp  HTTPS 리다이렉트 (선택)"
	else
		ui_info "- ${CFG_BACKEND_PORT}/tcp  API 직접 호출이 필요할 때만 (콘솔만 쓰면 열지 않아도 된다)"
	fi
	if [ "$CFG_SENSOR_ENABLED" -eq 1 ]; then
		ui_info "- ${CFG_SENSOR_PORT}/tcp  센서 게이트웨이 (센서가 있는 망에서 들어오는 방향)"
	fi
	ui_blank

	ui_step "다음 할 일"
	ui_info "1. 관리자 계정 생성 후 [발견] 화면에서 스캔 대상 대역을 등록한다."
	ui_info "2. [설정 > 접근 제한]에서 콘솔 접근 허용 IP를 등록한다(비워두면 제한 없음)."
	if [ "$CFG_SENSOR_ENABLED" -eq 1 ]; then
		ui_info "3. [센서] 화면의 등록 명령으로 대상 서버에 센서를 설치한다."
	else
		ui_info "3. 메일 알림이 필요하면 .env의 SMTP_* 를 채우고 다시 기동한다(INSTALL.md 5.2.1)."
	fi

	# 센서 바이너리는 이 서버 패키지에 들어 있지 않다 — 반입 자체를 빠뜨리는 사고를 막는다.
	if [ "$CFG_SENSOR_ENABLED" -eq 1 ]; then
		local pv
		pv=$(tr -d ' \t\r\n' < "$VERSION_FILE" 2>/dev/null || printf '<버전>')
		[ -n "$pv" ] || pv="<버전>"
		ui_blank
		ui_step "센서 패키지 (별도 반입 필요)"
		ui_info "이 서버 패키지에는 센서 바이너리가 없다 — 센서 호스트에는 sensor_linux_${pv}.tar.gz / sensor_windows_${pv}.zip 을 따로 반입한다."
		ui_info "각 패키지의 install-sensor.sh · install-sensor.ps1 이 [센서] 화면의 등록 명령을 받아 설치까지 끝낸다."
	fi

	ui_blank
	ui_note "설정 변경·재기동이 필요하면 이 스크립트를 다시 실행한다(기존 값이 기본값으로 제시된다)."
}

# --- HTTPS 배포 자동 감지 ------------------------------------------------------

# 이 프로젝트에 proxy 컨테이너가 남아 있는가 — proxy는 HTTPS 오버레이에만 있는 서비스다.
https_proxy_present() {
	[ -n "$COMPOSE_PROJECT" ] || return 1
	have docker || return 1
	docker info >/dev/null 2>&1 || return 1
	docker ps -aq \
		--filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
		--filter "label=com.docker.compose.service=proxy" 2>/dev/null | grep -q .
}

# 기존 .env·컨테이너에서 HTTPS 배포 흔적을 찾아 CFG_HTTPS를 세운다.
# install/reconfigure 로 재실행할 때 이게 없으면 dc()가 오버레이 없이 스택을 재생성한다 —
# .env의 SESSION_COOKIE_SECURE=true 는 그대로 남으므로 API는 200인데 브라우저가 Secure
# 쿠키를 버려 **로그인만 조용히 실패**한다(GOTCHAS / docs/manual/TLS.md 7장).
# 반대로 오탐으로 켜면 프록시가 인증서를 못 찾아 기동이 깨진다 — 그래서 .env 흔적 1개 이상과
# **인증서 파일 실존**을 함께 요구한다(server.crt·server.key는 고객사가 직접 둔 파일이다).
detect_https_deployment() {
	# 이미 정해진 값(--scheme https / AUTOCERT_SCHEME)은 덮어쓰지 않는다.
	if scheme_is_https; then return 0; fi

	local traces="" secure
	if [ -n "$(env_get HTTPS_PORT)" ]; then traces="HTTPS_PORT"; fi
	secure=$(env_get SESSION_COOKIE_SECURE)
	case "$secure" in
		[Tt][Rr][Uu][Ee] | 1) traces="${traces}${traces:+ · }SESSION_COOKIE_SECURE=true" ;;
	esac
	if https_proxy_present; then traces="${traces}${traces:+ · }proxy 컨테이너"; fi
	[ -n "$traces" ] || return 0

	if [ ! -f "$ROOT_DIR/deploy/tls/server.crt" ] || [ ! -f "$ROOT_DIR/deploy/tls/server.key" ]; then
		ui_warn "HTTPS 흔적은 있으나(${traces}) deploy/tls/server.crt·server.key 를 찾지 못했다 — HTTP 구성으로 진행한다."
		ui_info "HTTPS 배포라면 인증서를 그 경로에 두고 --mode https 로 실행한다 (절차: docs/manual/TLS.md)."
		return 0
	fi
	if [ ! -f "$COMPOSE_HTTPS_FILE" ]; then
		ui_warn "HTTPS 흔적은 있으나(${traces}) $(basename "$COMPOSE_HTTPS_FILE") 이 없다 — HTTP 구성으로 진행한다."
		ui_info "HTTPS 배포라면 패키지를 다시 반입하고 --mode https 로 실행한다."
		return 0
	fi
	if [ -z "$COMPOSE_HTTPS_OK" ]; then
		ui_warn "HTTPS 흔적은 있으나(${traces}) compose ${COMPOSE_VERSION:-?} 는 ports: !override 를 지원하지 않는다 — HTTP 구성으로 진행한다."
		ui_info "compose를 ${COMPOSE_HTTPS_MIN} 이상으로 올린 뒤 --mode https 로 실행한다 (절차: docs/manual/TLS.md)."
		return 0
	fi

	set_scheme https
	ui_ok "기존 설치가 HTTPS 구성으로 판단되어 TLS 오버레이를 유지한다 (근거: ${traces})"
}

# 이 설치의 접속 방식을 정한다. 우선순위:
#   1) .env의 AUTOCERT_SCHEME — install.sh가 직접 기록하는 정본
#   2) 레거시 휴리스틱(detect_https_deployment) — 그 이전 버전으로 깔린 설치 호환
# 여기서 정해지는 것은 **의도**다. 자산(인증서) 확인은 ensure_tls_assets가 하고,
# 자산이 없으면 거기서 http로 내려온다.
resolve_scheme() {
	local recorded
	CFG_PUBLIC_HOST=$(value_for AUTOCERT_PUBLIC_HOST "")
	recorded=$(value_for AUTOCERT_SCHEME "")
	case "$recorded" in
		https)
			set_scheme https
			ui_ok "기록된 접속 방식: HTTPS (.env AUTOCERT_SCHEME)"
			return 0
			;;
		http)
			set_scheme http
			ui_ok "기록된 접속 방식: HTTP (.env AUTOCERT_SCHEME)"
			return 0
			;;
	esac
	set_scheme http
	detect_https_deployment
}

# 다음 실행이 휴리스틱에 기대지 않도록 확정값을 .env에 남긴다.
persist_scheme() {
	env_set AUTOCERT_SCHEME "$CFG_SCHEME"
	if [ -n "$CFG_PUBLIC_HOST" ]; then
		env_set AUTOCERT_PUBLIC_HOST "$CFG_PUBLIC_HOST"
	fi
}

# --- 접속 방식 문답과 TLS 자산 ------------------------------------------------
#
# 접속 방식은 HTTP / HTTPS **2지선다**다. "둘 다 동시 서비스"는 제공하지 않는다 —
# 평문 경로를 열어 두는 순간 그쪽이 상용 경로가 되어 TLS가 장식이 되기 때문이다.
# HTTPS 구성은 80→443 리다이렉트를 이미 포함하므로 실사용에 불편이 없다.

# HTTPS를 고를 수 있는 환경인가 — 오버레이 파일과 compose 버전 둘 다 필요하다.
https_supported() {
	[ -f "$COMPOSE_HTTPS_FILE" ] || return 1
	[ -n "$COMPOSE_HTTPS_OK" ] || return 1
	return 0
}

tls_assets_present() { [ -s "$TLS_CRT" ] && [ -s "$TLS_KEY" ]; }

# install·patch·reconfigure가 공유하는 접속 방식 문답. 여기서 CFG_SCHEME이 확정되고,
# 그 값이 dc()·ensure_notify_base_url·print_done을 지배한다 — compose_up보다 앞이어야 한다.
ask_scheme() {
	local want=$CFG_SCHEME def=0

	if [ -n "$REQUESTED_SCHEME" ]; then
		want=$REQUESTED_SCHEME
	elif ! https_supported; then
		# 고를 수 없는 선택지는 보여주지 않는다.
		if scheme_is_https; then
			ui_warn "HTTPS 구성이었으나 이 환경에서는 오버레이를 쓸 수 없다(compose ${COMPOSE_VERSION:-?} / 필요: ${COMPOSE_HTTPS_MIN} 이상)."
		fi
		want=http
		ui_note "접속 방식: HTTP (이 환경에서는 HTTPS를 구성할 수 없다)"
	else
		if scheme_is_https; then def=1; fi
		tui_select "콘솔 접속 방식을 정한다 (한 가지만 쓴다)" "$def" \
			"HTTP — 평문. 폐쇄망 내부에서만 쓴다" \
			"HTTPS — nginx가 TLS를 종단한다(80은 443으로 되돌린다). 서버 인증서·개인키가 필요하다"
		if [ "$TUI_INDEX" -eq 1 ]; then want=https; else want=http; fi
	fi

	set_scheme "$want"

	if scheme_is_https && ! ensure_tls_assets; then
		ui_warn "HTTPS에 필요한 인증서를 확보하지 못했다 — HTTP 구성으로 진행한다."
		set_scheme http
	fi

	if scheme_is_https; then
		apply_https_settings
	else
		apply_http_settings
	fi
}

apply_https_settings() {
	local hp rp
	hp=$(value_for HTTPS_PORT "443")
	rp=$(value_for HTTP_REDIRECT_PORT "80")
	if ! overridden HTTPS_PORT; then
		tui_select "HTTPS 포트" 0 "기본값 ${hp}" "변경한다"
		if [ "$TUI_INDEX" -eq 1 ]; then
			tui_input "HTTPS 포트" "$hp" port_valid; hp=$TUI_ANSWER
		fi
	fi
	env_set HTTPS_PORT "$hp"
	env_set HTTP_REDIRECT_PORT "$rp"
	# TLS 종단 뒤에서만 켠다 — 평문 HTTP로 접속하면 브라우저가 Secure 쿠키를 버려
	# 로그인이 조용히 실패한다(GOTCHAS / TLS.md 7장).
	env_set SESSION_COOKIE_SECURE "true"
	ask_public_host
}

apply_http_settings() {
	local secure
	secure=$(env_get SESSION_COOKIE_SECURE)
	case "$secure" in
		[Tt][Rr][Uu][Ee] | 1)
			# 위 주석의 반대 방향이다. true인 채로 HTTP를 띄우면 API는 200인데
			# 브라우저가 쿠키를 버려 로그인 화면만 반복된다.
			env_set SESSION_COOKIE_SECURE "false"
			ui_warn "HTTP 구성이라 SESSION_COOKIE_SECURE를 false로 내렸다 (true로 두면 로그인만 조용히 실패한다)."
			;;
	esac
}

# deploy/tls/server.crt·server.key 를 확보한다. 0=확보, 1=확보 실패(HTTP로 내려간다).
ensure_tls_assets() {
	mkdir -p "$TLS_DIR" 2>/dev/null || true
	while :; do
		if tls_assets_present; then
			if validate_tls_pair "$TLS_CRT" "$TLS_KEY"; then
				chmod 600 "$TLS_KEY" 2>/dev/null || true
				chmod 644 "$TLS_CRT" 2>/dev/null || true
				ui_ok "서버 인증서 확인: deploy/tls/server.crt · server.key (개인키 권한 600)"
				return 0
			fi
		else
			ui_warn "deploy/tls/server.crt · server.key 가 없다 — 이 두 파일이 있어야 HTTPS가 뜬다."
			tls_pfx_hint
		fi

		if [ "$TUI_MODE" = "none" ] || [ -n "$ASSUME_YES" ]; then
			ui_info "비대화 모드에서는 인증서를 물어보지 않는다 — 파일을 두고 다시 실행한다."
			return 1
		fi

		tui_select "서버 인증서를 어떻게 할까?" 0 \
			"지금 경로를 지정한다 — deploy/tls/ 로 복사한다" \
			"직접 파일을 두고 오겠다 — 두고 나서 Enter" \
			"HTTP로 계속한다"
		case "$TUI_INDEX" in
			0) copy_tls_from_paths || true ;;
			1)
				ui_info "다른 터미널에서 아래 두 파일을 만들어 두고 돌아온다:"
				ui_info "  $TLS_CRT   (리프 인증서 + 중간 CA 를 이 순서로 이어붙인 PEM)"
				ui_info "  $TLS_KEY   (개인키 PEM · 암호 없이 · 권한 600)"
				tui_input "두었으면 Enter" ""
				;;
			*) return 1 ;;
		esac
	done
}

tls_pfx_hint() {
	have openssl || return 0
	ui_info ".pfx / .p12 만 있으면 두 줄로 꺼낸다(이 스크립트는 변환하지 않는다):"
	ui_info "  openssl pkcs12 -in <파일>.pfx -clcerts -nokeys -out server.crt"
	ui_info "  openssl pkcs12 -in <파일>.pfx -nocerts -nodes  -out server.key"
}

# 인증서·개인키를 지정 경로에서 deploy/tls/ 로 복사한다.
copy_tls_from_paths() {
	local crt key
	pick_existing_file "서버 인증서 파일(PEM)" "$PWD" '*.crt *.pem *.cer' || return 1
	crt=$PICKED_PATH
	[ -f "$crt" ] || { ui_err "그런 파일이 없다: $crt"; return 1; }
	pick_existing_file "개인키 파일(PEM · 암호 없이)" "$(dirname -- "$crt")" '*.key *.pem' || return 1
	key=$PICKED_PATH
	[ -f "$key" ] || { ui_err "그런 파일이 없다: $key"; return 1; }

	validate_tls_pair "$crt" "$key" || return 1

	mkdir -p "$TLS_DIR" || { ui_err "deploy/tls 를 만들지 못했다."; return 1; }
	cp "$crt" "$TLS_CRT" || { ui_err "인증서 복사에 실패했다."; return 1; }
	cp "$key" "$TLS_KEY" || { ui_err "개인키 복사에 실패했다."; return 1; }
	chmod 644 "$TLS_CRT" 2>/dev/null || true
	chmod 600 "$TLS_KEY" 2>/dev/null || true
	ui_ok "인증서를 deploy/tls/ 에 복사했다 (개인키 권한 600)"
	return 0
}

PICKED_PATH=""

# pick_existing_file 안내문구 시작디렉터리 패턴 → PICKED_PATH
# 탐색기는 lib/tui.sh의 tui_browse 계약을 쓴다(START_DIR PATTERN → TUI_PATH / 0=선택 1=취소).
pick_existing_file() {
	local prompt=$1 start=$2 pattern=$3
	PICKED_PATH=""
	if [ "$TUI_MODE" = "none" ] || [ -n "$ASSUME_YES" ]; then return 1; fi

	if command -v tui_browse >/dev/null 2>&1; then
		tui_select "$prompt" 0 "경로를 직접 입력한다" "파일 목록에서 찾는다"
		if [ "$TUI_INDEX" -eq 1 ]; then
			if tui_browse "$start" "$pattern"; then
				PICKED_PATH=${TUI_PATH:-}
				[ -n "$PICKED_PATH" ] || return 1
				return 0
			fi
			ui_note "탐색을 취소했다."
			return 1
		fi
	fi

	tui_input "${prompt} 경로" ""
	PICKED_PATH=$TUI_ANSWER
	[ -n "$PICKED_PATH" ] || return 1
	return 0
}

# 인증서·개인키 검증. **내용은 어디에도 출력하지 않는다** — 만료일과 판정 결과만 알린다.
validate_tls_pair() {
	local crt=$1 key=$2 pub_crt pub_key ends
	[ -s "$crt" ] || { ui_err "인증서 파일이 비어 있다: $crt"; return 1; }
	[ -s "$key" ] || { ui_err "개인키 파일이 비어 있다: $key"; return 1; }

	# 암호가 걸린 키는 nginx가 기동 중 암호를 물으며 멈춘다 — 컨테이너가 뜨지 않는다.
	if grep -q 'ENCRYPTED' "$key" 2>/dev/null; then
		ui_err "개인키에 암호가 걸려 있다 — nginx가 기동 중 멈춘다."
		ui_info "암호를 푼 사본을 만든다:  openssl rsa -in <키> -out server.key"
		return 1
	fi

	if ! have openssl; then
		ui_warn "openssl 이 없어 인증서 형식·짝 검증을 건너뛴다 — 기동 후 443 응답으로 확인한다."
		return 0
	fi

	if ! openssl x509 -in "$crt" -noout >/dev/null 2>&1; then
		ui_err "인증서가 PEM x509로 읽히지 않는다: $(basename -- "$crt")"
		ui_info "DER(.cer/.der)이면 변환한다:  openssl x509 -inform der -in <파일> -out server.crt"
		return 1
	fi

	ends=$(openssl x509 -in "$crt" -noout -enddate 2>/dev/null | sed 's/^notAfter=//')
	if ! openssl x509 -in "$crt" -noout -checkend 0 >/dev/null 2>&1; then
		ui_warn "인증서가 이미 만료됐다 (만료: ${ends:-?}) — 브라우저가 경고를 띄운다."
	elif ! openssl x509 -in "$crt" -noout -checkend 2592000 >/dev/null 2>&1; then
		ui_warn "인증서가 30일 안에 만료된다 (만료: ${ends:-?})."
	else
		ui_ok "인증서 유효 (만료: ${ends:-?})"
	fi

	# 짝 검증 — 공개키가 다르면 nginx가 SSL_CTX_use_PrivateKey 에서 기동에 실패한다.
	pub_crt=$(openssl x509 -in "$crt" -noout -pubkey 2>/dev/null || true)
	pub_key=$(openssl pkey -in "$key" -pubout 2>/dev/null || true)
	if [ -z "$pub_crt" ] || [ -z "$pub_key" ]; then
		ui_warn "인증서와 개인키의 짝을 확인하지 못했다(openssl이 키를 읽지 못했다) — 기동 후 443 응답으로 확인한다."
		return 0
	fi
	if [ "$pub_crt" != "$pub_key" ]; then
		ui_err "인증서와 개인키가 서로 맞지 않는다(공개키 불일치)."
		ui_info "같은 CSR로 발급받은 짝인지 확인한다."
		return 1
	fi
	ui_ok "인증서와 개인키가 짝이 맞는다"
	return 0
}

# HTTPS 접속 FQDN. 인증서 SAN의 첫 DNS 이름을 기본값으로 제시한다.
ask_public_host() {
	local def
	def=$CFG_PUBLIC_HOST
	[ -n "$def" ] || def=$(tls_default_host "$TLS_CRT")
	[ -n "$def" ] || def=$PRIMARY_IP

	if [ "$TUI_MODE" = "none" ] || [ -n "$ASSUME_YES" ]; then
		CFG_PUBLIC_HOST=$def
		ui_note "콘솔 접속 주소: ${def} (기본값)"
		return 0
	fi
	tui_input "브라우저로 접속할 주소(FQDN — 서버 인증서와 같은 이름이어야 경고가 없다)" "$def"
	CFG_PUBLIC_HOST=${TUI_ANSWER:-$def}
	ui_ok "콘솔 접속 주소: $CFG_PUBLIC_HOST"
}

# 인증서에서 이름 하나를 뽑는다(SAN의 첫 DNS → 없으면 CN). 그 외 내용은 읽지 않는다.
tls_default_host() {
	local crt=$1 name=""
	have openssl || return 0
	[ -s "$crt" ] || return 0
	name=$(openssl x509 -in "$crt" -noout -ext subjectAltName 2>/dev/null |
		tr ',' '\n' | sed -n 's/^[[:space:]]*DNS:[[:space:]]*//p' | head -1)
	if [ -z "$name" ]; then
		name=$(openssl x509 -in "$crt" -noout -subject -nameopt multiline 2>/dev/null |
			sed -n 's/^[[:space:]]*commonName[[:space:]]*=[[:space:]]*//p' | head -1)
	fi
	printf '%s' "$name"
}

# 기동 후 443이 실제로 응답하는지 본다. 이게 없으면 nginx가 인증서 문제로 죽어도
# 백엔드·프론트만 확인하고 "완료"가 나온다.
check_https_endpoint() {
	scheme_is_https || return 0
	local port waited=0 limit=45
	port=$(env_get HTTPS_PORT); port=${port:-443}

	if ! have curl && ! have openssl; then
		ui_warn "curl·openssl 이 없어 HTTPS(${port}) 확인을 건너뛴다 — 브라우저로 직접 확인한다."
		return 0
	fi

	ui_step "HTTPS 응답 확인 (포트 ${port}, 최대 ${limit}초)"
	while [ "$waited" -lt "$limit" ]; do
		if https_probe "$port"; then
			ui_ok "HTTPS 응답 확인 (${waited}초)"
			return 0
		fi
		sleep 3
		waited=$((waited + 3))
	done

	ui_err "HTTPS(${port}) 가 응답하지 않는다 — 프록시가 인증서를 읽지 못했을 가능성이 크다."
	ui_info "로그 확인: docker compose logs proxy --tail=50"
	ui_info "개인키에 암호가 걸려 있으면 nginx는 기동 중 멈춘다(암호 없는 키가 필요하다)."
	ui_info "포트 ${port} 를 다른 프로세스가 이미 쓰고 있는지도 확인한다."
	return 1
}

https_probe() {
	local port=$1
	if have curl; then
		curl -k -fsS -o /dev/null --max-time 5 "https://127.0.0.1:${port}/" >/dev/null 2>&1
		return $?
	fi
	# 인증서 본문이 화면에 나오지 않도록 출력을 전부 버린다.
	printf 'Q\n' | openssl s_client -connect "127.0.0.1:${port}" >/dev/null 2>&1
}

# --- 모드 --------------------------------------------------------------------

mode_install() {
	ui_title "서버 설치"
	ensure_source_tree
	ensure_images

	env_open
	persist_project_name
	ask_scheme
	collect_our_ports
	ask_ports
	ask_sensor
	inject_trusted_proxies
	ensure_postgres_password
	persist_scheme
	ensure_notify_base_url
	env_commit

	compose_up
	fix_trusted_proxies_after_up
	if ! wait_health; then exit 1; fi
	if ! check_https_endpoint; then exit 1; fi
	print_done
}

mode_reconfigure() {
	ui_title "설정 변경·재기동"
	ensure_source_tree
	ensure_images
	env_open
	persist_project_name
	ask_scheme
	collect_our_ports
	ask_ports
	ask_sensor
	inject_trusted_proxies
	ensure_postgres_password
	persist_scheme
	ensure_notify_base_url
	env_commit

	compose_up
	fix_trusted_proxies_after_up
	if ! wait_health; then exit 1; fi
	if ! check_https_endpoint; then exit 1; fi
	ui_ok "재기동 완료"
	print_done
}

# --mode https 는 "설정 변경·재기동 + HTTPS 선택"의 별칭이다.
# 인쇄된 매뉴얼과 패키지 MANIFEST에 이미 나가 있는 인자라 계속 받는다.
mode_https() {
	ui_warn "--mode https 는 이제 '설정 변경·재기동'의 별칭이다 (같은 뜻: --mode reconfigure --scheme https)."
	REQUESTED_SCHEME=https
	mode_reconfigure
}

ensure_postgres_password() {
	local pw
	pw=$(value_for POSTGRES_PASSWORD "")
	if [ -n "$pw" ]; then
		env_set POSTGRES_PASSWORD "$pw"
		ui_ok "DB 비밀번호: 기존 값 유지"
		return 0
	fi
	pw=$(gen_secret_b64) || die "난수 생성 도구가 없다(openssl 또는 /dev/urandom 필요)."
	[ -n "$pw" ] || die "DB 비밀번호 생성에 실패했다."
	env_set POSTGRES_PASSWORD "$pw"
	ui_ok "DB 비밀번호 자동 생성됨 (저장 위치: .env — 화면에 출력하지 않는다)"
}

# 알림 본문 딥링크 기준 URL. 비어 있으면 링크만 빠지므로 자동으로 채워 준다.
# 스킴이 바뀐 경우(HTTP→HTTPS 등)에는 기존 값을 그대로 두면 메일 속 링크가 전부
# 죽은 주소가 되므로 다시 만든다.
ensure_notify_base_url() {
	local cur want
	want=$(console_base_url)
	cur=$(value_for NOTIFY_BASE_URL "")
	if [ -n "$cur" ]; then
		case "$cur" in
			"${CFG_SCHEME}://"*)
				env_set NOTIFY_BASE_URL "$cur"
				return 0
				;;
		esac
		ui_warn "알림 링크 기준 URL의 접속 방식이 현재 구성과 다르다 — ${want} 로 바꾼다."
	fi
	env_set NOTIFY_BASE_URL "$want"
}

# 콘솔의 대외 접속 URL. 안내 문구와 NOTIFY_BASE_URL이 같은 값을 쓰게 한다.
console_base_url() {
	local host port
	host=${CFG_PUBLIC_HOST:-$PRIMARY_IP}
	if scheme_is_https; then
		port=$(env_get HTTPS_PORT); port=${port:-443}
		if [ "$port" = "443" ]; then printf 'https://%s' "$host"; return 0; fi
		printf 'https://%s:%s' "$host" "$port"
		return 0
	fi
	port=${CFG_FRONTEND_PORT:-$DEFAULT_FRONTEND_PORT}
	if [ "$port" = "80" ]; then printf 'http://%s' "$host"; return 0; fi
	printf 'http://%s:%s' "$host" "$port"
}

# --- 진입점 ------------------------------------------------------------------

main() {
	printf '\n' >&3
	ui_step "INHERIT AutoCert 설치 마법사  (버전 $(cat "$VERSION_FILE" 2>/dev/null || printf '?'))"
	ui_note "설치 위치: $ROOT_DIR"
	case "$TUI_MODE" in
		raw) : ;;
		plain) ui_note "이 터미널은 화살표 입력을 받지 못한다 — 번호 입력 방식으로 진행한다." ;;
		none) ui_note "터미널이 없어 비대화 모드로 진행한다 — 전부 기본값을 쓴다." ;;
	esac

	PRIMARY_IP=$(detect_primary_ip)
	# 사전 점검에서도 기존 .env 값(이미지 접두어·버전 등)을 봐야 한다.
	env_open
	# dc()가 -p 로 넘길 프로젝트명. 기존 스택 감지(stack_running)보다 먼저 정해야 한다.
	resolve_project_name

	if ! preflight; then
		exit 1
	fi
	if [ -n "$CHECK_ONLY" ]; then
		ui_blank
		ui_ok "사전 점검만 수행했다. 설치하려면 --check-only 없이 실행한다."
		exit 0
	fi

	# 접속 방식(http|https)은 dc()의 오버레이 목록을 지배하므로 여기서 먼저 정한다.
	# COMPOSE_HTTPS_OK를 보므로 preflight 뒤여야 한다.
	resolve_scheme

	local mode=$REQUESTED_MODE
	if [ -z "$mode" ]; then
		if stack_running; then
			ui_blank
			ui_warn "이 디렉터리의 스택이 이미 실행 중이다."
			dc ps --format 'table {{.Service}}\t{{.Status}}' >&3 2>&3 || dc ps >&3 2>&3 || true
			tui_select "무엇을 할까?" 0 \
				"설정 변경·재기동" \
				"상태 확인" \
				"HTTPS 적용" \
				"서버 설치(신규) — 설정을 다시 만든다" \
				"종료"
			case "$TUI_INDEX" in
				0) mode=reconfigure ;;
				1) mode=status ;;
				2) mode=https ;;
				3) mode=install ;;
				*) mode=quit ;;
			esac
		else
			tui_select "무엇을 할까?" 0 \
				"서버 설치(신규)" \
				"HTTPS 적용" \
				"설정 변경·재기동" \
				"상태 확인" \
				"종료"
			case "$TUI_INDEX" in
				0) mode=install ;;
				1) mode=https ;;
				2) mode=reconfigure ;;
				3) mode=status ;;
				*) mode=quit ;;
			esac
		fi
	fi

	case "$mode" in
		install) mode_install ;;
		reconfigure) mode_reconfigure ;;
		https) mode_https ;;
		status) print_status ;;
		quit) ui_info "종료한다." ;;
		*) die "알 수 없는 모드: $mode (install|https|reconfigure|status)" ;;
	esac
}

main "$@"
