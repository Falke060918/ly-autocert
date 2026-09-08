#!/usr/bin/env bash
# Compatibility entrypoint; Python is the single implementation.
set -euo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if command -v python3 >/dev/null 2>&1; then py=python3; else py=python; fi
exec "$py" "$here/starter.py" snapshot "$@"
