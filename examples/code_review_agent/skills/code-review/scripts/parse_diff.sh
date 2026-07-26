#!/usr/bin/env bash
#
# parse_diff.sh — Parses a unified diff file and lists changed files.
# This script is intended to be executed inside a sandbox workspace.
#
# Usage: bash scripts/parse_diff.sh <diff-file>
#
set -euo pipefail

DIFF_FILE="${1:-}"
if [ -z "$DIFF_FILE" ]; then
    echo "Usage: bash scripts/parse_diff.sh <diff-file>" >&2
    exit 1
fi

if [ ! -f "$DIFF_FILE" ]; then
    echo "Error: diff file not found: $DIFF_FILE" >&2
    exit 1
fi

python3 - "$DIFF_FILE" <<'PY'
import sys


def split_header(line):
    rest = line[len(b"diff --git "):]
    tokens = []
    pos = 0
    while pos < len(rest) and len(tokens) < 2:
        while pos < len(rest) and rest[pos:pos + 1] == b" ":
            pos += 1
        start = pos
        if pos < len(rest) and rest[pos:pos + 1] == b'"':
            pos += 1
            escaped = False
            while pos < len(rest):
                byte = rest[pos]
                pos += 1
                if escaped:
                    escaped = False
                elif byte == ord("\\"):
                    escaped = True
                elif byte == ord('"'):
                    break
        else:
            while pos < len(rest) and rest[pos:pos + 1] != b" ":
                pos += 1
        tokens.append(rest[start:pos])
    return tokens


def decode_git_path(token):
    if len(token) < 2 or token[:1] != b'"' or token[-1:] != b'"':
        raw = token
    else:
        source = token[1:-1]
        raw = bytearray()
        pos = 0
        escapes = {
            ord("a"): 7, ord("b"): 8, ord("t"): 9, ord("n"): 10,
            ord("v"): 11, ord("f"): 12, ord("r"): 13,
            ord('"'): 34, ord("\\"): 92,
        }
        while pos < len(source):
            byte = source[pos]
            pos += 1
            if byte != ord("\\") or pos == len(source):
                raw.append(byte)
                continue
            escaped = source[pos]
            pos += 1
            if ord("0") <= escaped <= ord("7"):
                digits = bytes([escaped])
                while pos < len(source) and len(digits) < 3 and ord("0") <= source[pos] <= ord("7"):
                    digits += bytes([source[pos]])
                    pos += 1
                raw.append(int(digits, 8))
            else:
                raw.append(escapes.get(escaped, escaped))
        raw = bytes(raw)
    return raw.decode("utf-8", "surrogateescape")


changed = set()
added = {}
removed = {}
current = None
with open(sys.argv[1], "rb") as diff:
    for raw_line in diff:
        line = raw_line.rstrip(b"\r\n")
        if line.startswith(b"diff --git "):
            tokens = split_header(line)
            current = None
            if len(tokens) == 2:
                current = decode_git_path(tokens[0])
                if current.startswith("a/"):
                    current = current[2:]
                changed.add(current)
        elif current is not None and line.startswith(b"+") and not line.startswith(b"+++"):
            added[current] = added.get(current, 0) + 1
        elif current is not None and line.startswith(b"-") and not line.startswith(b"---"):
            removed[current] = removed.get(current, 0) + 1

print("=== Changed files ===")
for name in sorted(changed):
    print(name)
print()
print("=== Added lines per file ===")
for name in sorted(added):
    print(f"  {name}: +{added[name]} lines")
print()
print("=== Removed lines per file ===")
for name in sorted(removed):
    print(f"  {name}: -{removed[name]} lines")
PY
