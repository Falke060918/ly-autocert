#!/usr/bin/env sh
# INHERIT AutoCert 오프라인 반입 번들 생성 — 대상: 개발측(인터넷 연결 가능 환경).
# 실행: sh scripts/bundle-offline.sh [옵션]
#
# 하는 일: 이미지 빌드 → docker save → 센서 바이너리 빌드 → 소스·compose·매뉴얼 수집
#          → SHA256SUMS 생성 → 번들 디렉터리 + 아카이브(.tar) 생성
# 고객사에서는 만들어진 번들 안의 load-offline.sh만 실행한다(scripts/load-offline.sh 사본).
#
# 필요 도구: docker(+compose v2), git, tar, gzip, sha256sum 또는 shasum
# 비밀값은 담지 않는다 — 소스는 git archive(=.env 등 gitignore 대상 제외)로만 수집한다.
set -eu

usage() {
	cat <<'EOF'
사용법: sh scripts/bundle-offline.sh [옵션]

옵션:
  --version <버전>    제품 버전 (기본: 루트 VERSION 파일, 환경변수 AUTOCERT_VERSION도 가능)
  --output <디렉터리> 산출물 상위 디렉터리 (기본: dist/)
  --skip-build        이미지 빌드를 건너뛰고 이미 로컬에 있는 이미지를 담는다
  --skip-sensor       센서 바이너리 빌드를 건너뛴다
  --no-archive        번들 디렉터리만 만들고 .tar로 묶지 않는다
  -h, --help          이 도움말

산출물:
  <출력>/autocert-offline-<버전>/       번들 디렉터리 (MANIFEST.txt·SHA256SUMS·images·sensor·source·manual)
  <출력>/autocert-offline-<버전>.tar    위 디렉터리를 묶은 반입 매체용 아카이브
  <출력>/autocert-offline-<버전>.tar.sha256
EOF
}

log() { printf '[bundle] %s\n' "$*"; }
warn() { printf '[bundle] 경고: %s\n' "$*" >&2; }
die() { printf '[bundle] 오류: %s\n' "$*" >&2; exit 1; }

# --- 인자 ---
version=""
output_dir=""
skip_build=0
skip_sensor=0
make_archive=1

while [ $# -gt 0 ]; do
	case "$1" in
		--version) [ $# -ge 2 ] || die "--version 값이 없다"; version="$2"; shift 2 ;;
		--output) [ $# -ge 2 ] || die "--output 값이 없다"; output_dir="$2"; shift 2 ;;
		--skip-build) skip_build=1; shift ;;
		--skip-sensor) skip_sensor=1; shift ;;
		--no-archive) make_archive=0; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; die "알 수 없는 옵션: $1" ;;
	esac
done

# --- 도구 확인 ---
for tool in docker git tar gzip; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool 이(가) 필요하다 (PATH에 없음)"
done
if command -v sha256sum >/dev/null 2>&1; then
	SHA_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
	SHA_CMD="shasum -a 256"
else
	die "sha256sum 또는 shasum 이(가) 필요하다"
fi
docker info >/dev/null 2>&1 || die "docker 데몬에 연결할 수 없다"

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cd "$root_dir"

# --- 버전 결정과 compose 기본값 정합 검사 ---
[ -f VERSION ] || die "VERSION 파일이 없다"
file_version=$(tr -d ' \t\r\n' < VERSION)
[ -n "$file_version" ] || die "VERSION 파일이 비었다"

# docker-compose.yml의 ${AUTOCERT_VERSION:-X} 기본값. 서비스마다 달라지면 안 된다.
compose_version=$(sed -n 's/.*AUTOCERT_VERSION:-\([^}]*\)}.*/\1/p' docker-compose.yml | sort -u)
[ -n "$compose_version" ] || die "docker-compose.yml에서 AUTOCERT_VERSION 기본값을 찾지 못했다"
[ "$(printf '%s\n' "$compose_version" | wc -l)" -eq 1 ] || die "docker-compose.yml의 AUTOCERT_VERSION 기본값이 서비스마다 다르다"
[ "$compose_version" = "$file_version" ] || die "VERSION($file_version) != docker-compose.yml 기본 태그($compose_version) — 릴리즈 시 VERSION·docker-compose.yml·CHANGELOG.md를 함께 올린다"

version="${version:-${AUTOCERT_VERSION:-$file_version}}"
image_prefix="${AUTOCERT_IMAGE_PREFIX:-inherit-autocert}"
if [ "$version" != "$file_version" ]; then
	warn "버전을 $file_version 이 아닌 $version 으로 덮어썼다 — 고객사 .env에도 AUTOCERT_VERSION=$version 를 넣어야 compose가 이 이미지를 찾는다"
fi

output_dir="${output_dir:-$root_dir/dist}"
bundle_name="autocert-offline-$version"
bundle_dir="$output_dir/$bundle_name"

# --- 담을 이미지 목록 ---
# db 이미지는 compose에서 읽어 태그 변경이 조용히 어긋나지 않게 한다.
db_image=$(sed -n 's/^[[:space:]]*image:[[:space:]]*\(postgres:[^[:space:]]*\)[[:space:]]*$/\1/p' docker-compose.yml | head -1)
[ -n "$db_image" ] || die "docker-compose.yml에서 postgres 이미지를 찾지 못했다"
backend_image="$image_prefix/backend:$version"
frontend_image="$image_prefix/frontend:$version"
# 빌드 단계 전용 베이스(golang·node)는 담지 않는다 — 두 Dockerfile 모두 멀티스테이지라
# 최종 이미지(distroless·node 런타임)에 필요한 레이어가 이미 포함되어 있다.
images="$db_image $backend_image $frontend_image"

log "제품 버전: $version / 이미지 접두어: $image_prefix"
log "담을 이미지: $images"

if [ -n "$(git status --porcelain)" ]; then
	warn "작업 트리에 커밋되지 않은 변경이 있다 — 번들의 소스는 HEAD 기준이라 이 변경이 빠진다"
fi

# --- 1. 이미지 빌드 ---
if [ "$skip_build" -eq 1 ]; then
	log "[1/6] 이미지 빌드 건너뜀 (--skip-build)"
else
	log "[1/6] 이미지 빌드 (backend·frontend)"
	# POSTGRES_PASSWORD는 compose 파일 파싱을 통과시키기 위한 자리값이다(db는 빌드 대상이
	# 아니고 이 값은 어떤 산출물에도 남지 않는다). 실제 비밀번호는 고객사 .env에서 정한다.
	AUTOCERT_VERSION="$version" \
	AUTOCERT_IMAGE_PREFIX="$image_prefix" \
	POSTGRES_PASSWORD=unused-at-build-time \
		docker compose build backend frontend
fi

log "[2/6] db 이미지 확보: $db_image"
if ! docker image inspect "$db_image" >/dev/null 2>&1; then
	docker pull "$db_image"
fi

for img in $images; do
	docker image inspect "$img" >/dev/null 2>&1 || die "이미지가 로컬에 없다: $img (--skip-build를 뺐는지 확인)"
done

# --- 2. 번들 디렉터리 초기화 ---
rm -rf "$bundle_dir"
mkdir -p "$bundle_dir/images" "$bundle_dir/source" "$bundle_dir/manual" "$bundle_dir/sensor"

# --- 3. 이미지 저장 ---
log "[3/6] docker save"
image_rows=""
for img in $images; do
	fname=$(printf '%s' "$img" | tr '/:' '--')
	docker save "$img" -o "$bundle_dir/images/$fname.tar"
	gzip -n -f "$bundle_dir/images/$fname.tar"
	img_id=$(docker image inspect -f '{{.Id}}' "$img")
	image_rows="$image_rows$img|images/$fname.tar.gz|$img_id
"
	log "  저장: $img -> images/$fname.tar.gz"
done

# --- 4. 센서 바이너리 ---
if [ "$skip_sensor" -eq 1 ]; then
	log "[4/6] 센서 바이너리 건너뜀 (--skip-sensor)"
	rmdir "$bundle_dir/sensor"
else
	log "[4/6] 센서 바이너리 빌드"
	sh "$script_dir/build-sensor-dist.sh"
	cp "$root_dir/backend/dist/autocert-sensor-linux-amd64" \
		"$root_dir/backend/dist/autocert-sensor-windows-amd64.exe" \
		"$root_dir/backend/dist/SHA256SUMS" \
		"$bundle_dir/sensor/"
fi

# --- 5. 소스·compose·매뉴얼 ---
log "[5/6] 소스·매뉴얼 수집"
# git archive는 추적 중인 파일만 담는다 — .env 등 gitignore 대상은 원천적으로 빠진다.
git archive --format=tar.gz --prefix="autocert-$version/" -o "$bundle_dir/source/autocert-source-$version.tar.gz" HEAD
cp "$root_dir/docker-compose.yml" "$bundle_dir/"
cp "$root_dir/.env.example" "$bundle_dir/"
cp "$root_dir/docs/manual/"*.md "$bundle_dir/manual/"
cp "$script_dir/load-offline.sh" "$bundle_dir/"
chmod +x "$bundle_dir/load-offline.sh"

# --- 6. 매니페스트와 체크섬 ---
log "[6/6] 매니페스트·체크섬"
git_commit=$(git rev-parse HEAD)
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

{
	echo "INHERIT AutoCert 오프라인 반입 번들"
	echo "==================================="
	echo "제품 버전     : $version"
	echo "생성 일시(UTC): $built_at"
	echo "소스 커밋     : $git_commit"
	echo "빌드 플랫폼   : $(uname -s) $(uname -m)"
	echo ""
	echo "포함 이미지 (docker load 후 이 태그로 존재해야 한다)"
	printf '%s' "$image_rows" | while IFS='|' read -r tag path id; do
		[ -n "$tag" ] || continue
		echo "  - $tag"
		echo "      파일: $path"
		echo "      이미지 ID: $id"
	done
	echo ""
	echo "포함 내용"
	echo "  images/   컨테이너 이미지 (docker save, gzip)"
	echo "  sensor/   센서 바이너리 linux/windows amd64 + 자체 SHA256SUMS"
	echo "  source/   제품 소스 아카이브 (git archive HEAD — 비밀값 파일 미포함)"
	echo "  manual/   설치·운영·센서 매뉴얼"
	echo "  docker-compose.yml, .env.example"
	echo ""
	echo "고객사 절차"
	echo "  1) sha256sum -c SHA256SUMS      (또는 아래 2)가 대신 수행)"
	echo "  2) sh load-offline.sh           체크섬 검증 + docker load"
	echo "  3) tar xzf source/autocert-source-$version.tar.gz"
	echo "  4) cd autocert-$version && cp .env.example .env && (값 설정) && docker compose up -d"
	echo "  자세한 절차는 manual/INSTALL.md 5.1절."
} > "$bundle_dir/MANIFEST.txt"

(
	cd "$bundle_dir"
	# shellcheck disable=SC2086 # SHA_CMD는 "shasum -a 256"처럼 인자를 포함해 분리가 의도적이다
	# -path로 최상위 SHA256SUMS만 제외한다(-name으로 빼면 sensor/SHA256SUMS까지 빠진다).
	find . -type f ! -path './SHA256SUMS' ! -path './SHA256SUMS.tmp' -exec $SHA_CMD {} + | sort -k 2 > SHA256SUMS.tmp
	mv SHA256SUMS.tmp SHA256SUMS
)

if [ "$make_archive" -eq 1 ]; then
	log "아카이브 생성"
	# 내부 파일이 이미 압축되어 있어 outer는 무압축 tar다.
	tar -cf "$output_dir/$bundle_name.tar" -C "$output_dir" "$bundle_name"
	(
		cd "$output_dir"
		# shellcheck disable=SC2086 # 위와 같은 이유
		$SHA_CMD "$bundle_name.tar" > "$bundle_name.tar.sha256"
	)
fi

log "완료"
log "  번들 디렉터리: $bundle_dir"
if [ "$make_archive" -eq 1 ]; then
	log "  아카이브     : $output_dir/$bundle_name.tar (+ .sha256)"
	log "  반입 매체에는 위 아카이브와 .sha256 두 파일만 담으면 된다."
fi
