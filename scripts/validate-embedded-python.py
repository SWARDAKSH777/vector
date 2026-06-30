#!/usr/bin/env python3
"""Compile Python heredocs embedded in release/installer scripts.

This catches quoting/newline damage that `bash -n` cannot see because the shell
only treats the heredoc body as opaque input to Python.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

HEREDOC = re.compile(
    r"\bpython(?:3(?:\.\d+)?)?\b[^\n]*?<<-?\s*(?:'([^']+)'|\"([^\"]+)\"|([A-Za-z_][A-Za-z0-9_]*))"
)


def embedded_blocks(path: Path):
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    index = 0
    while index < len(lines):
        match = HEREDOC.search(lines[index])
        if not match:
            index += 1
            continue
        delimiter = next(group for group in match.groups() if group is not None)
        strip_tabs = "<<-" in match.group(0)
        start_line = index + 2
        block: list[str] = []
        index += 1
        while index < len(lines):
            candidate = lines[index].rstrip("\r\n")
            compare = candidate.lstrip("\t") if strip_tabs else candidate
            if compare == delimiter:
                yield start_line, "".join(block)
                break
            block.append(lines[index].lstrip("\t") if strip_tabs else lines[index])
            index += 1
        else:
            raise SyntaxError(f"{path}:{start_line}: unterminated Python heredoc {delimiter!r}")
        index += 1


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(f"usage: {argv[0]} SCRIPT...", file=sys.stderr)
        return 2
    failures = 0
    blocks = 0
    for raw in argv[1:]:
        path = Path(raw)
        try:
            for line, source in embedded_blocks(path):
                blocks += 1
                try:
                    compile(source, f"{path}:heredoc@{line}", "exec")
                except SyntaxError as exc:
                    failures += 1
                    print(exc, file=sys.stderr)
        except (OSError, UnicodeError, SyntaxError) as exc:
            failures += 1
            print(exc, file=sys.stderr)
    if failures:
        print(f"embedded Python validation failed: {failures} error(s)", file=sys.stderr)
        return 1
    print(f"validated {blocks} embedded Python block(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
