#!/usr/bin/env sh
# 센서 바이너리 배포판 빌드 (Phase 3a). 대상: 개발측(인터넷 가능 환경).
# 실행: sh scripts/build-sensor-dist.sh   또는   backend/ 에서 make sensor-dist
#
# 로컬 Go 없이 Docker golang 이미지 경유로 linux/amd64·windows/amd64 정적 크로스
# 컴파일 후 SHA-256 체크섬을 만든다. 체크섬은 컨테이너 안에서 계산해 호스트 OS 비의존.
# 산출물: backend/dist/ (gitignore 대상)
#
# backend/Makefile의 sensor-dist 타깃과 scripts/bundle-offline.sh가 모두 이 파일을
# 호출한다 — 크로스 빌드 레시피의 단일 진실원천이다.
set -e

GO_IMAGE="${GO_IMAGE:-golang:1.25-alpine}"

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
backend_dir=$(CDPATH='' cd -- "$script_dir/../backend" && pwd)
repo_root=$(CDPATH='' cd -- "$script_dir/.." && pwd)

# 버전 결정 순서: AUTOCERT_VERSION 환경변수 → 저장소 루트 VERSION 파일 → dev.
# main.version으로 주입되어 `autocert-sensor --version`이 출력한다.
sensor_version="${AUTOCERT_VERSION:-}"
if [ -z "$sensor_version" ] && [ -f "$repo_root/VERSION" ]; then
	sensor_version=$(tr -d ' \t\r\n' < "$repo_root/VERSION")
fi
[ -n "$sensor_version" ] || sensor_version="dev"

# Git Bash/MSYS는 docker 인자의 `/src`를 호스트 경로(C:/Program Files/Git/src)로 자동
# 변환해 컨테이너 경로를 망가뜨린다. 변환을 끄고 호스트 경로만 Windows 형식으로 넘긴다.
mount_dir="$backend_dir"
case "$(uname -s)" in
	MINGW*|MSYS*)
		mount_dir=$(CDPATH='' cd -- "$backend_dir" && pwd -W)
		MSYS_NO_PATHCONV=1
		export MSYS_NO_PATHCONV
		;;
esac

echo "센서 배포판 빌드 ($GO_IMAGE, 버전 $sensor_version) — 산출물: $backend_dir/dist"

# 버전은 -e로 넘긴다 — 컨테이너 안에서 전개되므로 호스트 셸의 따옴표 처리를 타지 않는다.
docker run --rm -v "$mount_dir":/src -w /src -e CGO_ENABLED=0 -e "SENSOR_VERSION=$sensor_version" "$GO_IMAGE" \
	sh -ec '
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$SENSOR_VERSION" -o dist/autocert-sensor-linux-amd64 ./cmd/sensor
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$SENSOR_VERSION" -o dist/autocert-sensor-windows-amd64.exe ./cmd/sensor
	cd dist && sha256sum autocert-sensor-linux-amd64 autocert-sensor-windows-amd64.exe > SHA256SUMS'

echo "완료:"
cat "$backend_dir/dist/SHA256SUMS"
