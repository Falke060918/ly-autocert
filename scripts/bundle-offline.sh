#!/usr/bin/env sh
# INHERIT AutoCert 릴리즈 패키지 생성 — 대상: 개발측(인터넷 연결 가능 환경).
# 실행: sh scripts/bundle-offline.sh [옵션]
#
# 버전별·환경별로 3종을 따로 만든다. 통합 번들(autocert-offline-<버전>.tar)은 폐기됐다.
#   <출력>/main_<버전>.tar.gz           서버(관리 콘솔): 이미지·소스·매뉴얼·install.sh
#   <출력>/sensor_linux_<버전>.tar.gz   Linux 센서
#   <출력>/sensor_windows_<버전>.zip    Windows 센서
# 각 산출물에 <파일명>.sha256 이 따라붙는다. 3종을 모두 만들 때만 <출력>/SHA256SUMS-<버전>
# (3줄 집계)이 추가로 나온다 — 부분 빌드의 집계 파일은 완본으로 오인되므로 만들지 않는다.
#
# 필요 도구
#   공통          : git, tar, sha256sum 또는 shasum
#   main·센서     : docker(+compose v2), gzip
#   sensor-windows: zip 경로 하나 (zip / python3 / bsdtar / docker 중 첫 번째로 가능한 것)
#
# 비밀값은 담지 않는다 — 소스는 git archive(=.env 등 gitignore 대상 제외)로만 수집한다.
set -eu

usage() {
	cat <<'EOF'
사용법: sh scripts/bundle-offline.sh [옵션]

옵션:
  --target <대상>     만들 패키지. all | main | sensor-linux | sensor-windows
                      반복 지정과 콤마 구분 모두 허용 (기본: all)
  --version <버전>    제품 버전 (기본: 루트 VERSION 파일, 환경변수 AUTOCERT_VERSION도 가능)
  --output <디렉터리> 산출물 상위 디렉터리 (기본: dist/)
  --skip-build        이미지 빌드를 건너뛰고 이미 로컬에 있는 이미지를 담는다 (main 전용)
  --skip-sensor       [deprecated] --target main 과 같다
  --no-archive        패키지 디렉터리만 만들고 아카이브로 묶지 않는다
  -h, --help          이 도움말

산출물 (버전당 6개 파일):
  <출력>/main_<버전>.tar.gz          + .sha256
  <출력>/sensor_linux_<버전>.tar.gz  + .sha256
  <출력>/sensor_windows_<버전>.zip   + .sha256
  <출력>/SHA256SUMS-<버전>           3종을 모두 만들 때만
EOF
}

log() { printf '[bundle] %s\n' "$*"; }
warn() { printf '[bundle] 경고: %s\n' "$*" >&2; }
die() { printf '[bundle] 오류: %s\n' "$*" >&2; exit 1; }

GO_IMAGE="${GO_IMAGE:-golang:1.25-alpine}"

# --- 인자 ---
version=""
output_dir=""
skip_build=0
make_archive=1
targets_raw=""
target_opt_seen=0
skip_sensor_seen=0

add_target() {
	# 콤마 구분을 공백으로 펴서 하나씩 검증한다.
	_list=$(printf '%s' "$1" | tr ',' ' ')
	for _t in $_list; do
		case "$_t" in
			all) targets_raw="$targets_raw main sensor-linux sensor-windows" ;;
			main|sensor-linux|sensor-windows) targets_raw="$targets_raw $_t" ;;
			*) die "알 수 없는 --target 값: $_t (유효값: all, main, sensor-linux, sensor-windows)" ;;
		esac
	done
}

while [ $# -gt 0 ]; do
	case "$1" in
		--target) [ $# -ge 2 ] || die "--target 값이 없다"; target_opt_seen=1; add_target "$2"; shift 2 ;;
		--version) [ $# -ge 2 ] || die "--version 값이 없다"; version="$2"; shift 2 ;;
		--output) [ $# -ge 2 ] || die "--output 값이 없다"; output_dir="$2"; shift 2 ;;
		--skip-build) skip_build=1; shift ;;
		--skip-sensor) skip_sensor_seen=1; shift ;;
		--no-archive) make_archive=0; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; die "알 수 없는 옵션: $1" ;;
	esac
done

if [ "$skip_sensor_seen" -eq 1 ]; then
	[ "$target_opt_seen" -eq 0 ] || die "--skip-sensor 와 --target 은 함께 쓸 수 없다 — --target 하나로 지정한다"
	warn "--skip-sensor 는 deprecated 다 — 앞으로는 --target main 을 쓴다"
	add_target main
fi

[ -n "$targets_raw" ] || add_target all

# 중복 제거 + 고정 순서(main → sensor-linux → sensor-windows).
targets=""
for t in main sensor-linux sensor-windows; do
	case " $targets_raw " in *" $t "*) targets="$targets $t " ;; esac
done

has_target() {
	case "$targets" in *" $1 "*) return 0 ;; esac
	return 1
}

if [ "$skip_build" -eq 1 ] && ! has_target main; then
	warn "--skip-build 는 main 패키지에만 유효하다 — 이번 실행에서는 무시한다"
	skip_build=0
fi

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cd "$root_dir"

require_file() {
	[ -f "$1" ] || die "필요한 파일이 없다: ${1#"$root_dir"/} — 이 파일 없이는 패키지를 만들 수 없다"
}

# BRE 메타문자를 통째로 이스케이프한다(버전 문자열을 grep 패턴에 넣을 때 쓴다).
bre_escape() { printf '%s' "$1" | sed 's/[^A-Za-z0-9_-]/\\&/g'; }

# ---------------------------------------------------------------------------
# 버전 정합 검사 — --target 과 무관하게 여기서 1회. 잘못된 릴리즈에서는
# 센서 패키지 하나도 나오면 안 되므로 어떤 빌드보다 먼저 돈다.
# (검사 5·6은 성격상 사후 assert 라 각 패키지 생성 직후에 있다.)
# ---------------------------------------------------------------------------

# [검사 1/7] VERSION 파일이 비어있지 않고 SemVer 형태인가
[ -f VERSION ] || die "[검사 1/7] VERSION 파일이 없다"
file_version=$(tr -d ' \t\r\n' < VERSION)
[ -n "$file_version" ] || die "[검사 1/7] VERSION 파일이 비었다"
printf '%s\n' "$file_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)*$' \
	|| die "[검사 1/7] VERSION 이 SemVer(MAJOR.MINOR.PATCH) 형태가 아니다: $file_version"

# docker-compose.yml 의 ${AUTOCERT_VERSION:-X} 기본값.
compose_raw=$(sed -n 's/.*AUTOCERT_VERSION:-\([^}]*\)}.*/\1/p' docker-compose.yml)
compose_count=$(printf '%s\n' "$compose_raw" | grep -c . || true)

# [검사 3/7] 원시 매칭 수 ≥ 2 — 유니크 값 검사만으로는 backend·frontend 중 한쪽에서
# 변수가 통째로 사라져도(남은 하나의 유니크 값이 여전히 1개라) 통과해버린다.
[ "$compose_count" -ge 2 ] \
	|| die "[검사 3/7] docker-compose.yml 의 \${AUTOCERT_VERSION:-…} 참조가 ${compose_count}개다 — backend·frontend 두 서비스 모두에 있어야 한다"

# [검사 2/7] 기본값이 서비스마다 같고 VERSION 과 일치하는가
compose_version=$(printf '%s\n' "$compose_raw" | sort -u)
[ "$(printf '%s\n' "$compose_version" | wc -l)" -eq 1 ] \
	|| die "[검사 2/7] docker-compose.yml 의 AUTOCERT_VERSION 기본값이 서비스마다 다르다"
[ "$compose_version" = "$file_version" ] \
	|| die "[검사 2/7] VERSION($file_version) != docker-compose.yml 기본 태그($compose_version) — 릴리즈 시 VERSION·docker-compose.yml·CHANGELOG.md 를 함께 올린다"

# [검사 7/7] frontend 의 package.json·package-lock.json version = VERSION
# 이미지 안에 그대로 실려 나가는 값이라 어긋나면 화면·번들이 다른 버전을 주장하게 된다.
# 의존성 트리에도 "version" 키가 수천 개라 최상위 블록만 본다(jq 없이 들여쓰기로 가른다).
[ -f frontend/package.json ] || die "[검사 7/7] frontend/package.json 이 없다"
[ -f frontend/package-lock.json ] || die "[검사 7/7] frontend/package-lock.json 이 없다"

# package.json 은 최상위에만 "version" 이 있다(dependencies 는 "<이름>": "<범위>" 형태).
pkg_version=$(sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' frontend/package.json | head -1)
[ -n "$pkg_version" ] || die "[검사 7/7] frontend/package.json 에서 version 을 읽지 못했다"
[ "$pkg_version" = "$file_version" ] \
	|| die "[검사 7/7] VERSION($file_version) != frontend/package.json version($pkg_version) — 릴리즈 시 함께 올린다"

# lock 최상위 version: 들여쓰기 2칸. 패키지 트리 항목은 6칸 이상이라 섞이지 않는다.
lock_version=$(sed -n 's/^  "version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' frontend/package-lock.json | head -1)
[ -n "$lock_version" ] || die "[검사 7/7] frontend/package-lock.json 에서 최상위 version 을 읽지 못했다"
[ "$lock_version" = "$file_version" ] \
	|| die "[검사 7/7] VERSION($file_version) != frontend/package-lock.json 최상위 version($lock_version) — 릴리즈 시 함께 올린다"

# lock 의 packages[""] 항목(= 루트 패키지 자신). 블록의 첫 version 줄을 읽는다.
lock_root_version=$(sed -n '/^[[:space:]]*"": {/,/^[[:space:]]*},/p' frontend/package-lock.json \
	| sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
[ -n "$lock_root_version" ] || die "[검사 7/7] frontend/package-lock.json 의 packages[\"\"] 에서 version 을 읽지 못했다"
[ "$lock_root_version" = "$file_version" ] \
	|| die "[검사 7/7] VERSION($file_version) != frontend/package-lock.json packages[\"\"] version($lock_root_version) — 릴리즈 시 함께 올린다"

version="${version:-${AUTOCERT_VERSION:-$file_version}}"
version_overridden=0
[ "$version" = "$file_version" ] || version_overridden=1

# 버전 문자열은 파일명에 그대로 들어간다.
[ -n "$version" ] || die "버전 문자열이 비었다"
case "$version" in
	*/*) die "버전 문자열에 '/' 를 쓸 수 없다(파일명에 들어간다): $version" ;;
esac
if printf '%s' "$version" | grep -q '[[:space:]]'; then
	die "버전 문자열에 공백을 쓸 수 없다(파일명에 들어간다): $version"
fi

# [검사 4/7] CHANGELOG.md 에 '## v<버전> (' 헤딩이 있는가
[ -f CHANGELOG.md ] || die "[검사 4/7] CHANGELOG.md 가 없다"
if grep -q "^## v$(bre_escape "$version") (" CHANGELOG.md; then
	:
elif [ "$version_overridden" -eq 1 ]; then
	warn "[검사 4/7] CHANGELOG.md 에 '## v$version (' 헤딩이 없다 — --version 으로 덮어쓴 개발 빌드로 보고 계속한다"
else
	die "[검사 4/7] CHANGELOG.md 에 '## v$version (' 헤딩이 없다 — 릴리즈 시 VERSION·docker-compose.yml·CHANGELOG.md 를 함께 올린다"
fi

if [ "$version_overridden" -eq 1 ]; then
	warn "버전을 $file_version 이 아닌 $version 으로 덮어썼다 — 고객사 .env 에도 AUTOCERT_VERSION=$version 를 넣어야 compose 가 이 이미지를 찾는다"
fi

# ---------------------------------------------------------------------------
# 도구 확인 — 타깃별 지연 검사. --target main 이 zip 부재로 실패하면 안 된다.
# ---------------------------------------------------------------------------
need_tool() { command -v "$1" >/dev/null 2>&1 || die "$1 이(가) 필요하다 (PATH에 없음)"; }

# 공통
need_tool git
need_tool tar
if command -v sha256sum >/dev/null 2>&1; then
	SHA_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
	SHA_CMD="shasum -a 256"
else
	die "sha256sum 또는 shasum 이(가) 필요하다"
fi

# main·센서 공통: 이미지 save 와 센서 크로스빌드 모두 docker 를 쓴다.
if has_target main || has_target sensor-linux || has_target sensor-windows; then
	need_tool docker
	need_tool gzip
	docker info >/dev/null 2>&1 || die "docker 데몬에 연결할 수 없다"
fi

# sensor-windows: zip 폴백 사다리에서 첫 번째로 가능한 것을 고른다.
zip_method=""
resolve_zip_method() {
	if command -v zip >/dev/null 2>&1; then zip_method=zip; return 0; fi
	if command -v python3 >/dev/null 2>&1 && python3 -c 'import zipfile' >/dev/null 2>&1; then zip_method=python3; return 0; fi
	if command -v bsdtar >/dev/null 2>&1; then zip_method=bsdtar; return 0; fi
	# macOS 의 /usr/bin/tar 는 bsdtar 라 --format=zip 을 안다(GNU tar 는 모른다).
	if [ -x /usr/bin/tar ] && /usr/bin/tar --version 2>/dev/null | grep -qi bsdtar; then zip_method=bsdtar-usr; return 0; fi
	# 빌드 머신은 이미 docker 필수 + 인터넷 가능 측이고 이 이미지는 센서 빌드로 로컬에 있다.
	# Git Bash 에 zip·python3 가 모두 없는 케이스를 이 단계가 덮는다.
	if command -v docker >/dev/null 2>&1; then zip_method=docker; return 0; fi
	die "zip 산출물을 만들 방법이 없다 — zip / python3 / bsdtar / docker 중 하나가 필요하다 (.tar.gz 를 .zip 이름으로 내보내지 않는다)"
}
if has_target sensor-windows; then
	resolve_zip_method
	log "sensor-windows zip 생성 방식: $zip_method"
fi

output_dir="${output_dir:-$root_dir/dist}"
mkdir -p "$output_dir"
output_dir=$(CDPATH='' cd -- "$output_dir" && pwd)

tar_is_gnu=0
if tar --version 2>/dev/null | head -1 | grep -q 'GNU tar'; then tar_is_gnu=1; fi

image_prefix="${AUTOCERT_IMAGE_PREFIX:-inherit-autocert}"
git_commit=$(git rev-parse HEAD)
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

log "제품 버전: $version / 대상: $(printf '%s' "$targets" | tr -s ' ' | sed 's/^ //;s/ $//')"
log "산출물 디렉터리: $output_dir"

if [ -n "$(git status --porcelain)" ]; then
	warn "작업 트리에 커밋되지 않은 변경이 있다 — 패키지의 소스는 HEAD 기준이라 이 변경이 빠진다"
fi

# ---------------------------------------------------------------------------
# 공용 헬퍼
# ---------------------------------------------------------------------------

# MANIFEST 공통 머리말. '제품 버전' 줄은 [검사 5/7] 이 grep -Fx 로 대조한다.
manifest_head() {
	printf 'INHERIT AutoCert %s\n' "$1"
	printf '===================================\n'
	printf '제품 버전     : %s\n' "$version"
	printf '생성 일시(UTC): %s\n' "$built_at"
	printf '소스 커밋     : %s\n' "$git_commit"
	printf '빌드 플랫폼   : %s %s\n' "$(uname -s)" "$(uname -m)"
}

# 스테이징 최종 상태에서 계산한다. 이 뒤로는 어떤 파일도 손대지 않는다
# (줄바꿈 변환·chmod 재적용이 체크섬을 깬다).
write_sha256sums() {
	(
		cd "$1"
		# shellcheck disable=SC2086 # SHA_CMD 는 "shasum -a 256" 처럼 인자를 포함해 분리가 의도적이다
		# -path 로 최상위 SHA256SUMS 만 제외한다.
		find . -type f ! -path './SHA256SUMS' ! -path './SHA256SUMS.tmp' -exec $SHA_CMD {} + | sort -k 2 > SHA256SUMS.tmp
		mv SHA256SUMS.tmp SHA256SUMS
	)
}

# [검사 5/7] 생성된 패키지의 VERSION 파일·MANIFEST 버전 문자열 = 결정된 버전
assert_package_version() {
	_dir="$1"
	[ -f "$_dir/VERSION" ] || die "[검사 5/7] $_dir/VERSION 이 없다"
	_pv=$(tr -d ' \t\r\n' < "$_dir/VERSION")
	[ "$_pv" = "$version" ] || die "[검사 5/7] 패키지 VERSION($_pv) != 결정된 버전($version)"
	[ -f "$_dir/MANIFEST.txt" ] || die "[검사 5/7] $_dir/MANIFEST.txt 가 없다"
	grep -Fxq "제품 버전     : $version" "$_dir/MANIFEST.txt" \
		|| die "[검사 5/7] MANIFEST.txt 의 제품 버전 줄이 결정된 버전($version)과 다르다: $_dir"
}

# [검사 6/7] 아카이브 파일명의 버전 = 결정된 버전
assert_archive_name() {
	case "$1" in
		*"_$version".tar.gz|*"_$version".zip) : ;;
		*) die "[검사 6/7] 아카이브 파일명에 결정된 버전($version)이 없다: $1" ;;
	esac
}

human_size() {
	if [ "$1" -ge 1048576 ]; then printf '%s MiB' "$(( $1 / 1048576 ))"
	elif [ "$1" -ge 1024 ]; then printf '%s KiB' "$(( $1 / 1024 ))"
	else printf '%s B' "$1"
	fi
}

artifact_rows=""
record_artifact() {
	_path="$output_dir/$1"
	[ -e "$_path" ] || die "산출물이 없다: $_path"
	if [ -d "$_path" ]; then
		artifact_rows="$artifact_rows$1/|(디렉터리)|-
"
		return 0
	fi
	_bytes=$(wc -c < "$_path" | tr -d ' ')
	# shellcheck disable=SC2086 # 위와 같은 이유
	_h=$($SHA_CMD "$_path" | cut -c1-12)
	artifact_rows="$artifact_rows$1|$(human_size "$_bytes")|$_h
"
}

# tar.gz 아카이브. $3 은 gzip 압축 레벨, $4 는 실행 비트를 줄 파일들(스템 기준 상대경로).
make_targz() {
	_parent="$1"; _stem="$2"; _level="$3"; _execs="$4"
	_out="$_parent/$_stem.tar.gz"
	_tmp="$_parent/.$_stem.tar.part"
	rm -f "$_out" "$_tmp"
	if [ "$tar_is_gnu" -eq 1 ] && [ -n "$_execs" ]; then
		# 실행 비트를 호스트 파일시스템에 맡기지 않는다 — MSYS(Git Bash)는 ELF 파일에
		# chmod +x 를 남기지 못해(PE·셔뱅만 실행으로 인식) Windows 빌드 머신에서 만든
		# 센서 tar 의 바이너리가 0644 로 실려 나간다. 아카이브에 넣을 때 모드를 못박는다.
		_ex=""
		for _e in $_execs; do _ex="$_ex --exclude=$_stem/$_e"; done
		# shellcheck disable=SC2086 # _ex 는 --exclude 인자 여러 개라 분리가 의도적이다
		tar -cf "$_tmp" --mode='go-w,a+rX' $_ex -C "$_parent" "$_stem"
		for _e in $_execs; do
			tar -rf "$_tmp" --mode=0755 -C "$_parent" "$_stem/$_e"
		done
	else
		# GNU tar 가 아니면 --mode 가 없다. 그런 환경(macOS·BSD)은 chmod 가 제대로 먹으므로
		# 스테이징 단계의 chmod +x 가 그대로 실린다.
		tar -cf "$_tmp" -C "$_parent" "$_stem"
	fi
	gzip "-$_level" -n -c "$_tmp" > "$_out"
	rm -f "$_tmp"
	tar -tzf "$_out" >/dev/null 2>&1 || die "아카이브 생성 실패(스트림 손상): $_stem.tar.gz"
	assert_archive_name "$_stem.tar.gz"
}

# zip 아카이브. 상대경로·슬래시 구분자만 쓰고 심볼릭 링크는 담지 않는다.
# 줄바꿈은 .gitattributes(*.ps1 eol=crlf / *.sh eol=lf)가 보장하므로 추가 변환하지 않는다.
make_zip() {
	_parent="$1"; _stem="$2"
	_zip="$_stem.zip"
	rm -f "$_parent/$_zip"
	case "$zip_method" in
		zip)        ( cd "$_parent" && zip -rXq "$_zip" "$_stem" ) ;;
		python3)    ( cd "$_parent" && python3 -m zipfile -c "$_zip" "$_stem" ) ;;
		bsdtar)     ( cd "$_parent" && bsdtar --format=zip -cf "$_zip" "$_stem" ) ;;
		bsdtar-usr) ( cd "$_parent" && /usr/bin/tar --format=zip -cf "$_zip" "$_stem" ) ;;
		docker)     docker_zip "$_parent" "$_stem" "$_zip" ;;
		*)          die "zip 생성 방식이 정해지지 않았다" ;;
	esac
	[ -s "$_parent/$_zip" ] || die "zip 생성 실패: $_zip"
	assert_archive_name "$_zip"
}

docker_zip() {
	# 서브셸 안에서만 MSYS_NO_PATHCONV 를 켠다 — build-sensor-dist.sh 와 같은 회피책이다.
	# MSYS 는 docker 인자의 `/src` 를 호스트 경로로 자동 변환해 컨테이너 경로를 망가뜨린다.
	(
		_mount="$1"
		case "$(uname -s)" in
			MINGW*|MSYS*)
				_mount=$(CDPATH='' cd -- "$1" && pwd -W)
				MSYS_NO_PATHCONV=1
				export MSYS_NO_PATHCONV
				;;
		esac
		_own="0:0"
		if command -v id >/dev/null 2>&1; then _own="$(id -u):$(id -g)"; fi
		docker run --rm -v "$_mount":/src -w /src "$GO_IMAGE" \
			sh -ec 'apk add --no-cache zip >/dev/null; zip -rXq "$1" "$2"; chown "$3" "$1" 2>/dev/null || true' \
			_ "$3" "$2" "$_own"
	)
}

sensor_built=0
ensure_sensor_binaries() {
	[ "$sensor_built" -eq 0 ] || return 0
	log "센서 바이너리 빌드 (scripts/build-sensor-dist.sh)"
	require_file "$script_dir/build-sensor-dist.sh"
	sh "$script_dir/build-sensor-dist.sh"
	sensor_built=1
}

# ---------------------------------------------------------------------------
# main 패키지 — 서버(관리 콘솔). 센서 바이너리는 넣지 않는다(분리의 목적).
# ---------------------------------------------------------------------------
build_main() {
	stem="main_$version"
	stage="$output_dir/$stem"
	log "=== main 패키지: $stem ==="

	require_file "$script_dir/install.sh"
	require_file "$script_dir/load-offline.sh"
	require_file "$script_dir/lib/tui.sh"
	for m in INSTALL OPERATIONS SENSOR TLS; do
		require_file "$root_dir/docs/manual/$m.md"
	done

	# db 이미지는 compose 에서 읽어 태그 변경이 조용히 어긋나지 않게 한다.
	db_image=$(sed -n 's/^[[:space:]]*image:[[:space:]]*\(postgres:[^[:space:]]*\)[[:space:]]*$/\1/p' docker-compose.yml | head -1)
	[ -n "$db_image" ] || die "docker-compose.yml 에서 postgres 이미지를 찾지 못했다"
	# 빌드 단계 전용 베이스(golang·node)는 담지 않는다 — 두 Dockerfile 모두 멀티스테이지라
	# 최종 이미지(distroless·node 런타임)에 필요한 레이어가 이미 포함되어 있다.
	images="$db_image $image_prefix/backend:$version $image_prefix/frontend:$version"
	log "  담을 이미지: $images"

	if [ "$skip_build" -eq 1 ]; then
		log "  [1/5] 이미지 빌드 건너뜀 (--skip-build)"
	else
		log "  [1/5] 이미지 빌드 (backend·frontend)"
		# POSTGRES_PASSWORD 는 compose 파일 파싱을 통과시키기 위한 자리값이다(db 는 빌드
		# 대상이 아니고 이 값은 어떤 산출물에도 남지 않는다). 실제 비밀번호는 고객사 .env 에서 정한다.
		AUTOCERT_VERSION="$version" \
		AUTOCERT_IMAGE_PREFIX="$image_prefix" \
		POSTGRES_PASSWORD=unused-at-build-time \
			docker compose build backend frontend
	fi

	log "  [2/5] db 이미지 확보: $db_image"
	if ! docker image inspect "$db_image" >/dev/null 2>&1; then
		docker pull "$db_image"
	fi
	for img in $images; do
		docker image inspect "$img" >/dev/null 2>&1 || die "이미지가 로컬에 없다: $img (--skip-build 를 뺐는지 확인)"
	done

	rm -rf "$stage"
	mkdir -p "$stage/images" "$stage/source" "$stage/manual" "$stage/lib"

	log "  [3/5] docker save"
	image_rows=""
	for img in $images; do
		fname=$(printf '%s' "$img" | tr '/:' '--')
		docker save "$img" -o "$stage/images/$fname.tar"
		gzip -n -f "$stage/images/$fname.tar"
		img_id=$(docker image inspect -f '{{.Id}}' "$img")
		image_rows="$image_rows$img|images/$fname.tar.gz|$img_id
"
		log "    저장: $img -> images/$fname.tar.gz"
	done

	log "  [4/5] 소스·매뉴얼·설치 스크립트 수집"
	# git archive 는 추적 중인 파일만 담는다 — .env 등 gitignore 대상은 원천적으로 빠진다.
	git archive --format=tar.gz --prefix="autocert-$version/" \
		-o "$stage/source/autocert-source-$version.tar.gz" HEAD
	# 반입 심사 사전 열람용 사본. 소스 아카이브에서 그대로 꺼내므로 install.sh 가
	# --strip-components=1 로 패키지 루트에 전개하면 동일 바이트로 덮인다 — 사본 드리프트가
	# 구조적으로 불가능하다(작업 트리에서 cp 하면 줄바꿈·미커밋 변경으로 어긋날 수 있다).
	tar -xzf "$stage/source/autocert-source-$version.tar.gz" -C "$stage" --strip-components=1 \
		"autocert-$version/docker-compose.yml" "autocert-$version/.env.example"
	[ -f "$stage/docker-compose.yml" ] || die "소스 아카이브에서 docker-compose.yml 을 꺼내지 못했다"
	[ -f "$stage/.env.example" ] || die "소스 아카이브에서 .env.example 을 꺼내지 못했다"

	for m in INSTALL OPERATIONS SENSOR TLS; do
		cp "$root_dir/docs/manual/$m.md" "$stage/manual/$m.md"
	done
	cp "$script_dir/install.sh" "$stage/install.sh"
	cp "$script_dir/load-offline.sh" "$stage/load-offline.sh"
	cp "$script_dir/lib/tui.sh" "$stage/lib/tui.sh"
	chmod +x "$stage/install.sh" "$stage/load-offline.sh"
	printf '%s\n' "$version" > "$stage/VERSION"

	log "  [5/5] 매니페스트·체크섬"
	{
		manifest_head "서버 패키지 (main)"
		echo ""
		echo "포함 이미지 (docker load 후 이 태그로 존재해야 한다)"
		# 아래 "  - <태그>" 줄은 load-offline.sh 의 파싱 계약이다. 포맷을 바꾸지 않는다.
		printf '%s' "$image_rows" | while IFS='|' read -r tag path id; do
			[ -n "$tag" ] || continue
			echo "  - $tag"
			echo "      파일: $path"
			echo "      이미지 ID: $id"
		done
		echo ""
		echo "포함 내용"
		echo "  install.sh          설치 진입점 — 이 파일 하나만 실행하면 된다"
		echo "  load-offline.sh     체크섬 검증 + docker load (install.sh 가 호출한다)"
		echo "  lib/tui.sh          설치 스크립트 공용 출력 라이브러리"
		echo "  images/             컨테이너 이미지 (docker save, gzip)"
		echo "  source/             제품 소스 아카이브 (git archive HEAD — 비밀값 파일 미포함)"
		echo "                      install.sh 가 이 디렉터리에 전개한다(패키지 루트 = 설치 루트)"
		echo "  manual/             설치·운영·센서·TLS 매뉴얼"
		echo "  docker-compose.yml  반입 심사 사전 열람용 (소스 아카이브와 동일 바이트)"
		echo "  .env.example        동상"
		echo "  SHA256SUMS          자기 자신을 제외한 전 파일의 SHA-256"
		echo ""
		echo "고객사 절차"
		echo "  1) 이 디렉터리를 설치할 위치에 그대로 둔다 (여기가 설치 루트가 된다)"
		echo "  2) sha256sum -c SHA256SUMS   (install.sh 가 대신 수행한다)"
		echo "  3) bash install.sh"
		echo "     소스 전개·이미지 적재·.env 생성·기동까지 이 스크립트 하나가 끝낸다."
		echo "  자세한 절차는 manual/INSTALL.md 5장."
		echo ""
		echo "센서는 별도 패키지다: sensor_linux_$version.tar.gz / sensor_windows_$version.zip"
		echo "  이 패키지에는 센서 바이너리가 들어 있지 않다 — 센서 호스트에 따로 반입한다."
	} > "$stage/MANIFEST.txt"

	write_sha256sums "$stage"
	assert_package_version "$stage"

	if [ "$make_archive" -eq 1 ]; then
		# 내용물 대부분이 이미 압축된 이미지라 높은 레벨은 시간 대비 이득이 없다.
		make_targz "$output_dir" "$stem" 1 "install.sh load-offline.sh"
		finish_archive "$stem.tar.gz"
	else
		record_artifact "$stem"
	fi
}

# ---------------------------------------------------------------------------
# sensor_linux 패키지
# ---------------------------------------------------------------------------
build_sensor_linux() {
	stem="sensor_linux_$version"
	stage="$output_dir/$stem"
	log "=== Linux 센서 패키지: $stem ==="

	require_file "$script_dir/install-sensor.sh"
	# tui.sh 는 조건 없이 항상 넣는다 — 없으면 센서 패키지가 main 에 의존하게 되어 분리가 무너진다.
	require_file "$script_dir/lib/tui.sh"
	require_file "$root_dir/docs/manual/SENSOR.md"

	ensure_sensor_binaries
	bin_src="$root_dir/backend/dist/autocert-sensor-linux-amd64"
	require_file "$bin_src"

	rm -rf "$stage"
	mkdir -p "$stage/lib" "$stage/manual"
	cp "$script_dir/install-sensor.sh" "$stage/install-sensor.sh"
	cp "$script_dir/lib/tui.sh" "$stage/lib/tui.sh"
	cp "$bin_src" "$stage/autocert-sensor-linux-amd64"
	cp "$root_dir/docs/manual/SENSOR.md" "$stage/manual/SENSOR.md"
	chmod +x "$stage/install-sensor.sh" "$stage/autocert-sensor-linux-amd64"
	printf '%s\n' "$version" > "$stage/VERSION"
	# backend/dist/SHA256SUMS 는 넣지 않는다 — 패키지 레벨 SHA256SUMS 와 겹쳐
	# "어느 걸 검증하나" 가 생긴다.

	{
		manifest_head "Linux 센서 패키지 (sensor_linux)"
		echo "대상 플랫폼   : linux/amd64"
		echo ""
		echo "포함 내용"
		echo "  install-sensor.sh              설치 진입점"
		echo "  lib/tui.sh                     설치 스크립트 공용 출력 라이브러리"
		echo "  autocert-sensor-linux-amd64    센서 바이너리 (정적, CGO 비활성)"
		echo "  manual/SENSOR.md               센서 설치·운영 매뉴얼"
		echo "  SHA256SUMS                     자기 자신을 제외한 전 파일의 SHA-256"
		echo ""
		echo "고객사 절차"
		echo "  1) sha256sum -c SHA256SUMS   (install-sensor.sh 가 대신 수행한다)"
		echo "  2) sudo bash install-sensor.sh"
		echo "  자세한 절차는 manual/SENSOR.md."
	} > "$stage/MANIFEST.txt"

	write_sha256sums "$stage"
	assert_package_version "$stage"

	if [ "$make_archive" -eq 1 ]; then
		make_targz "$output_dir" "$stem" 6 "install-sensor.sh autocert-sensor-linux-amd64"
		finish_archive "$stem.tar.gz"
	else
		record_artifact "$stem"
	fi
}

# ---------------------------------------------------------------------------
# sensor_windows 패키지
# ---------------------------------------------------------------------------
build_sensor_windows() {
	stem="sensor_windows_$version"
	stage="$output_dir/$stem"
	log "=== Windows 센서 패키지: $stem ==="

	require_file "$script_dir/install-sensor.ps1"
	require_file "$root_dir/docs/manual/SENSOR.md"

	ensure_sensor_binaries
	bin_src="$root_dir/backend/dist/autocert-sensor-windows-amd64.exe"
	require_file "$bin_src"

	rm -rf "$stage"
	mkdir -p "$stage/manual"
	cp "$script_dir/install-sensor.ps1" "$stage/install-sensor.ps1"
	cp "$bin_src" "$stage/autocert-sensor-windows-amd64.exe"
	cp "$root_dir/docs/manual/SENSOR.md" "$stage/manual/SENSOR.md"
	printf '%s\n' "$version" > "$stage/VERSION"

	{
		manifest_head "Windows 센서 패키지 (sensor_windows)"
		echo "대상 플랫폼   : windows/amd64"
		echo ""
		echo "포함 내용"
		echo "  install-sensor.ps1                  설치 진입점 (관리자 PowerShell)"
		echo "  autocert-sensor-windows-amd64.exe   센서 바이너리 (정적, CGO 비활성)"
		echo "  manual\\SENSOR.md                    센서 설치·운영 매뉴얼"
		echo "  SHA256SUMS                          자기 자신을 제외한 전 파일의 SHA-256"
		echo ""
		echo "고객사 절차"
		echo "  1) Get-FileHash 로 SHA256SUMS 대조 (install-sensor.ps1 가 대신 수행한다)"
		echo "  2) 관리자 PowerShell 에서  .\\install-sensor.ps1"
		echo "  자세한 절차는 manual\\SENSOR.md."
	} > "$stage/MANIFEST.txt"

	write_sha256sums "$stage"
	assert_package_version "$stage"

	if [ "$make_archive" -eq 1 ]; then
		make_zip "$output_dir" "$stem"
		finish_archive "$stem.zip"
	else
		record_artifact "$stem"
	fi
}

# 아카이브 하나에 대한 .sha256 사이드카 생성 + 산출물 표 등재.
finish_archive() {
	(
		cd "$output_dir"
		# shellcheck disable=SC2086 # SHA_CMD 는 인자를 포함해 분리가 의도적이다
		$SHA_CMD "$1" > "$1.sha256"
	)
	record_artifact "$1"
	record_artifact "$1.sha256"
}

# ---------------------------------------------------------------------------
# 실행
# ---------------------------------------------------------------------------
if has_target main; then build_main; fi
if has_target sensor-linux; then build_sensor_linux; fi
if has_target sensor-windows; then build_sensor_windows; fi

# 집계 SHA256SUMS 는 3종을 모두 만들었을 때만 낸다 — 부분 빌드의 불완전한 목록이
# 완본으로 오인되는 것을 막는다.
if [ "$make_archive" -eq 1 ] && has_target main && has_target sensor-linux && has_target sensor-windows; then
	sums_name="SHA256SUMS-$version"
	(
		cd "$output_dir"
		# shellcheck disable=SC2086 # 위와 같은 이유
		$SHA_CMD "main_$version.tar.gz" "sensor_linux_$version.tar.gz" "sensor_windows_$version.zip" > "$sums_name"
	)
	record_artifact "$sums_name"
	log "집계 체크섬: $sums_name (3줄)"
fi

log "완료 — 산출물 (${output_dir})"
printf '%s' "$artifact_rows" | while IFS='|' read -r name size hash; do
	[ -n "$name" ] || continue
	printf '[bundle]   %-38s %10s  %s\n' "$name" "$size" "$hash"
done
if [ "$make_archive" -eq 1 ]; then
	log "반입 매체에는 대상 환경에 맞는 아카이브와 그 .sha256 만 담으면 된다."
else
	log "--no-archive: 패키지 디렉터리만 만들었다."
fi
