#!/usr/bin/env sh
# INHERIT AutoCert 오프라인 반입 번들 적재 — 대상: 고객사 서버(폐쇄망, Linux/macOS).
# 실행: 번들 디렉터리에서  sh load-offline.sh
#
# 하는 일: SHA256SUMS 대조 → docker load → 적재된 이미지 태그 확인.
# 체크섬이 하나라도 어긋나면 즉시 중단한다(적재하지 않는다) — 매체 손상·변조 판별이
# 폐쇄망 반입의 유일한 방어선이다.
#
# 이 파일은 저장소의 scripts/load-offline.sh 사본이며 번들에 그대로 실려 온다.
# 필요 도구: docker, gzip, sha256sum 또는 shasum. (제품 기동에는 docker compose v2도 필요)
set -eu

usage() {
	cat <<'EOF'
사용법: sh load-offline.sh [옵션]

옵션:
  --verify-only   체크섬만 대조하고 적재는 하지 않는다
  -h, --help      이 도움말

반드시 번들 디렉터리(MANIFEST.txt·SHA256SUMS가 있는 곳)에서 실행한다.
EOF
}

log() { printf '[load] %s\n' "$*"; }
die() { printf '[load] 오류: %s\n' "$*" >&2; exit 1; }

verify_only=0
while [ $# -gt 0 ]; do
	case "$1" in
		--verify-only) verify_only=1; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; die "알 수 없는 옵션: $1" ;;
	esac
done

bundle_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
cd "$bundle_dir"

[ -f SHA256SUMS ] || die "SHA256SUMS가 없다 — 번들 디렉터리에서 실행하고 있는지 확인한다"
[ -f MANIFEST.txt ] || die "MANIFEST.txt가 없다 — 번들 디렉터리에서 실행하고 있는지 확인한다"

if command -v sha256sum >/dev/null 2>&1; then
	SHA_CHECK="sha256sum -c"
elif command -v shasum >/dev/null 2>&1; then
	SHA_CHECK="shasum -a 256 -c"
else
	die "sha256sum 또는 shasum 이(가) 필요하다"
fi

# --- 1. 체크섬 대조 ---
log "[1/3] 체크섬 대조 (SHA256SUMS)"
# shellcheck disable=SC2086 # SHA_CHECK는 "shasum -a 256 -c"처럼 인자를 포함해 분리가 의도적이다
if ! $SHA_CHECK SHA256SUMS > /tmp/autocert-sha256-check.$$ 2>&1; then
	grep -v ': OK$' /tmp/autocert-sha256-check.$$ >&2 || true
	rm -f /tmp/autocert-sha256-check.$$
	die "체크섬 불일치 — 적재를 중단한다. 매체를 다시 복사해 받고 재시도한다"
fi
rm -f /tmp/autocert-sha256-check.$$
log "  전체 파일 무결성 확인됨"

if [ "$verify_only" -eq 1 ]; then
	log "--verify-only: 여기서 종료한다"
	exit 0
fi

command -v docker >/dev/null 2>&1 || die "docker 가 필요하다 (PATH에 없음)"
docker info >/dev/null 2>&1 || die "docker 데몬에 연결할 수 없다 (권한 또는 서비스 상태 확인)"

# --- 2. 이미지 적재 ---
log "[2/3] docker load"
for f in images/*.tar.gz; do
	[ -f "$f" ] || die "images/ 에 이미지 파일이 없다"
	log "  적재: $f"
	docker load -i "$f"
done

# --- 3. 태그 확인 ---
log "[3/3] 적재 결과 확인"
# MANIFEST.txt의 "  - <태그>" 줄이 있어야 할 이미지 목록이다.
missing=0
sed -n 's/^  - \(.*\)$/\1/p' MANIFEST.txt | while read -r tag; do
	[ -n "$tag" ] || continue
	if docker image inspect "$tag" >/dev/null 2>&1; then
		printf '[load]   OK      %s\n' "$tag"
	else
		printf '[load]   MISSING %s\n' "$tag" >&2
		exit 1
	fi
done || missing=1
[ "$missing" -eq 0 ] || die "매니페스트에 적힌 이미지가 적재되지 않았다"

log "완료. 다음 단계:"
log "  1) tar xzf source/autocert-source-*.tar.gz      (autocert-<버전>/ 으로 풀린다)"
log "  2) 그 디렉터리로 이동 후  cp .env.example .env  → .env 값 설정"
log "  3) docker compose up -d      (--build 없이 적재한 이미지를 그대로 쓴다)"
log "  자세한 절차는 manual/INSTALL.md 5장."
