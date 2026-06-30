#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mapfile -d '' shell_files < <(
  find . -type f -print0 | while IFS= read -r -d '' file; do
    case "$file" in
      ./frontend/node_modules/*|./frontend/dist/*|./backend/vendor/*|./backend/web/*) continue ;;
    esac
    first_line="$(head -n 1 "$file" 2>/dev/null || true)"
    if [[ "$first_line" == '#!'*bash* || "$first_line" == '#!'*sh* ]]; then
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

python3 -m py_compile scripts/validate-embedded-python.py
python3 scripts/validate-embedded-python.py "${shell_files[@]}"

echo "validated ${#shell_files[@]} shell script(s)"
