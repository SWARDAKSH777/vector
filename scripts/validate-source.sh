#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Use Git's tracked/untracked file list instead of scanning .git internals.
# Inspect only the first 256 bytes through a pipe, avoiding NUL bytes in
# Bash variables and command substitution.
mapfile -d '' shell_files < <(
  git ls-files --cached --others --exclude-standard -z |
    while IFS= read -r -d '' file; do
      [[ -f "$file" ]] || continue

      case "$file" in
        frontend/node_modules/*|\
        frontend/dist/*|\
        backend/vendor/*|\
        backend/web/*)
          continue
          ;;
      esac

      if head -c 256 -- "$file" 2>/dev/null |
        LC_ALL=C grep -aEq \
          '^#!.*[[:space:]/](ba|da|k|z)?sh([[:space:]]|$)'; then
        printf '%s\0' "$file"
      fi
    done
)

if ((${#shell_files[@]} == 0)); then
  echo "ERROR: no shell scripts found" >&2
  exit 1
fi

for file in "${shell_files[@]}"; do
  bash -n "$file"
done

python3 - <<'PYCODE'
from pathlib import Path

path = Path("scripts/validate-embedded-python.py")
source = path.read_text(encoding="utf-8")
compile(source, str(path), "exec")
PYCODE
python3 scripts/validate-embedded-python.py "${shell_files[@]}"

echo "validated ${#shell_files[@]} shell script(s)"
