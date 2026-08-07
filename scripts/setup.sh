#!/usr/bin/env bash
# claude-starter setup — rtk 설치 및 Claude Code 훅 등록 (Linux/macOS, 머신당 1회)
# 실행: bash scripts/setup.sh
# pipefail: curl | sh 파이프에서 curl 실패가 set -e에 걸리게 한다.
set -e -o pipefail

echo "=== claude-starter setup ==="

# 1. rtk 확인/설치
if ! command -v rtk >/dev/null 2>&1 && [ ! -x "$HOME/.local/bin/rtk" ]; then
  echo "[1/3] rtk가 없어 설치합니다..."
  curl -fsSL https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh | sh
fi
export PATH="$HOME/.local/bin:$PATH"
RTK="$(command -v rtk || echo "$HOME/.local/bin/rtk")"
echo "[1/3] rtk: $RTK ($("$RTK" --version))"

# 2. ripgrep 확인 (rtk 일부 필터가 사용)
if command -v rg >/dev/null 2>&1; then
  echo "[2/3] ripgrep 확인됨"
else
  echo "[2/3] ripgrep(rg)이 없습니다. 설치 권장:"
  echo "      sudo apt install ripgrep   # Debian/Ubuntu"
  echo "      brew install ripgrep       # macOS"
fi

# 3. Claude Code 전역 훅 등록 (bash 명령 출력 자동 압축)
echo "[3/3] rtk 훅 등록 (rtk init -g)..."
"$RTK" init -g --auto-patch

echo ""
echo "완료. 대부분 즉시 적용되어 bash 명령 출력이 자동 압축됩니다."
echo "  적용이 안 되면 Claude Code를 재시작하세요."
echo "  확인: rtk init --show    절약량: rtk gain    제거: rtk init -g --uninstall"
