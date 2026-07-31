#!/usr/bin/env python3
# Tencent is pleased to support the open source community by making
# trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.

"""parse_diff.py parses a unified diff and prints a JSON array of changed files.

The output schema (one object per file, in diff order) is:

    [
      {
        "path": "internal/db/query.go",
        "old_path": "internal/db/query.go",
        "status": "modified",
        "added_lines": 9,
        "deleted_lines": 3,
        "hunks": [
          {
            "old_start": 18,
            "old_count": 11,
            "new_start": 18,
            "new_count": 19
          }
        ],
        "added_line_numbers": [22, 23, 24],
        "deleted_line_numbers": [20]
      }
    ]

Field meanings:

- path          post-change file path (the "+++" side), or the new name.
- old_path      pre-change file path (the "---" side), or the old name.
- status        "added", "modified", "renamed", or "deleted".
- added_lines   count of "+" content lines across all hunks.
- deleted_lines count of "-" content lines across all hunks.
- hunks         per-hunk header metadata.
- added_line_numbers   1-based line numbers of "+" lines in the new file.
- deleted_line_numbers 1-based line numbers of "-" lines in the old file.

Usage:
    python3 scripts/parse_diff.py diff.patch
    git diff main...HEAD | python3 scripts/parse_diff.py -
    python3 scripts/parse_diff.py - < diff.patch

Exit status:
    0  the input parsed successfully (even with zero files).
    1  the input could not be read or no diff was found.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from typing import List, Optional, TextIO


# Matches: @@ -old_start,old_count +new_start,new_count @@ optional heading
# Counts are optional (default to 1 per the unified diff spec).
_HUNK_RE = re.compile(
    r"^@@ -(?P<old_start>\d+)(?:,(?P<old_count>\d+))?"
    r" \+(?P<new_start>\d+)(?:,(?P<new_count>\d+))? @@"
)

# Matches the "diff --git a/<old> b/<new>" extended header. The paths may be
# quoted if they contain spaces or special characters.
_GIT_HEADER_RE = re.compile(r"^diff --git a/(.*) b/(.*)$")

# Matches "--- " and "+++ " lines. The path may be prefixed with "a/" or "b/"
# or may be "/dev/null". Quoted paths are surrounded by double quotes.
_OLD_FILE_RE = re.compile(r"^--- (?:a/)?(?P<path>.+)$")
_NEW_FILE_RE = re.compile(r"^\+\+\+ (?:b/)?(?P<path>.+)$")


@dataclass
class Hunk:
    """Metadata for a single hunk plus its recorded line numbers."""

    old_start: int
    old_count: int
    new_start: int
    new_count: int
    added_line_numbers: List[int] = field(default_factory=list)
    deleted_line_numbers: List[int] = field(default_factory=list)

    def to_dict(self) -> dict:
        return {
            "old_start": self.old_start,
            "old_count": self.old_count,
            "new_start": self.new_start,
            "new_count": self.new_count,
        }


@dataclass
class FileChange:
    """A single file's parsed diff representation."""

    path: str
    old_path: str
    status: str
    hunks: List[Hunk] = field(default_factory=list)
    added_lines: int = 0
    deleted_lines: int = 0
    added_line_numbers: List[int] = field(default_factory=list)
    deleted_line_numbers: List[int] = field(default_factory=list)

    def to_dict(self) -> dict:
        return {
            "path": self.path,
            "old_path": self.old_path,
            "status": self.status,
            "added_lines": self.added_lines,
            "deleted_lines": self.deleted_lines,
            "hunks": [h.to_dict() for h in self.hunks],
            "added_line_numbers": self.added_line_numbers,
            "deleted_line_numbers": self.deleted_line_numbers,
        }


def _strip_quotes(path: str) -> str:
    """Remove surrounding double quotes from a diff path if present."""
    if len(path) >= 2 and path.startswith('"') and path.endswith('"'):
        # Unescape octal sequences Git uses for unsafe characters.
        try:
            return path[1:-1].encode("latin-1", "backslashreplace").decode(
                "unicode_escape"
            )
        except (UnicodeDecodeError, UnicodeEncodeError):
            return path[1:-1]
    return path


def _classify_status(old_path: str, new_path: str) -> str:
    """Determine the change status from the --- and +++ paths."""
    if old_path == "/dev/null":
        return "added"
    if new_path == "/dev/null":
        return "deleted"
    if old_path != new_path:
        return "renamed"
    return "modified"


class DiffParser:
    """Stateful parser for unified diff text."""

    def __init__(self, lines: List[str]) -> None:
        self._lines = lines
        self._index = 0

    def parse(self) -> List[FileChange]:
        changes: List[FileChange] = []
        while self._index < len(self._lines):
            line = self._lines[self._index]

            # A new file block can be introduced either by an extended
            # "diff --git" header or directly by a "--- " line (plain unified
            # diff produced by tools other than git).
            if line.startswith("diff --git "):
                change = self._parse_git_file_block()
                if change is not None:
                    changes.append(change)
            elif line.startswith("--- "):
                change = self._parse_plain_file_block()
                if change is not None:
                    changes.append(change)
            else:
                self._index += 1
        return changes

    def _parse_git_file_block(self) -> Optional[FileChange]:
        """Parse a file block starting at a 'diff --git' header."""
        header = self._lines[self._index]
        match = _GIT_HEADER_RE.match(header)
        self._index += 1

        # The a/ b/ paths in the diff --git line are unreliable for renames
        # and quoted paths, so prefer the ---/+++ lines when they appear.
        old_path_hint = _strip_quotes(match.group(1)) if match else ""
        new_path_hint = _strip_quotes(match.group(2)) if match else ""

        old_path, new_path = self._scan_to_file_paths(old_path_hint, new_path_hint)
        if old_path is None or new_path is None:
            # No ---/+++ found; fall back to the git header hints.
            old_path = old_path_hint
            new_path = new_path_hint

        change = FileChange(
            path=new_path,
            old_path=old_path,
            status=_classify_status(old_path, new_path),
        )

        self._parse_hunks(change)
        return change

    def _parse_plain_file_block(self) -> Optional[FileChange]:
        """Parse a file block starting directly at a '--- ' line."""
        old_path, new_path = self._scan_to_file_paths("", "")
        if old_path is None or new_path is None:
            return None

        change = FileChange(
            path=new_path,
            old_path=old_path,
            status=_classify_status(old_path, new_path),
        )
        self._parse_hunks(change)
        return change

    def _scan_to_file_paths(
        self, old_hint: str, new_hint: str
    ) -> tuple[Optional[str], Optional[str]]:
        """Advance past preamble lines until --- and +++ are consumed.

        Returns the resolved old/new paths. Falls back to the provided hints
        when the ---/+++ lines are absent.
        """
        old_path: Optional[str] = None
        new_path: Optional[str] = None
        while self._index < len(self._lines):
            line = self._lines[self._index]
            if line.startswith("--- "):
                m = _OLD_FILE_RE.match(line)
                old_path = _strip_quotes(m.group("path")) if m else line[4:]
                self._index += 1
            elif line.startswith("+++ "):
                m = _NEW_FILE_RE.match(line)
                new_path = _strip_quotes(m.group("path")) if m else line[4:]
                self._index += 1
            elif line.startswith("@@"):
                # Reached the first hunk; stop scanning for file paths.
                break
            elif line.startswith("diff --git "):
                # Next file began without ---/+++ lines; stop and let the
                # outer loop handle it.
                break
            else:
                # Preamble line (index, similarity, rename from/to, etc.).
                self._index += 1

        if old_path is None:
            old_path = old_hint or ""
        if new_path is None:
            new_path = new_hint or ""
        return old_path, new_path

    def _parse_hunks(self, change: FileChange) -> None:
        """Parse all consecutive hunks belonging to the current file."""
        while self._index < len(self._lines):
            line = self._lines[self._index]
            if line.startswith("@@"):
                hunk = self._parse_one_hunk()
                if hunk is not None:
                    change.hunks.append(hunk)
                    change.added_lines += len(hunk.added_line_numbers)
                    change.deleted_lines += len(hunk.deleted_line_numbers)
                    change.added_line_numbers.extend(hunk.added_line_numbers)
                    change.deleted_line_numbers.extend(hunk.deleted_line_numbers)
            elif line.startswith("diff --git "):
                # Next file block; stop.
                break
            elif line.startswith("--- "):
                # A new plain-diff file block begins without a diff --git
                # header. Stop so the outer loop can parse it.
                break
            else:
                # Non-hunk, non-file-header line between hunks (e.g. a stray
                # "\ No newline at end of file"). Skip it.
                self._index += 1

    def _parse_one_hunk(self) -> Optional[Hunk]:
        """Parse a single hunk starting at the current @@ line."""
        header = self._lines[self._index]
        match = _HUNK_RE.match(header)
        if match is None:
            self._index += 1
            return None

        old_start = int(match.group("old_start"))
        old_count = int(match.group("old_count")) if match.group("old_count") else 1
        new_start = int(match.group("new_start"))
        new_count = int(match.group("new_count")) if match.group("new_count") else 1

        hunk = Hunk(
            old_start=old_start,
            old_count=old_count,
            new_start=new_start,
            new_count=new_count,
        )
        self._index += 1

        # Running line counters. old_start/new_start are 1-based.
        old_line = old_start
        new_line = new_start

        # Consume exactly old_count old-side lines and new_count new-side
        # lines, which is the authoritative way to bound a hunk.
        consumed_old = 0
        consumed_new = 0
        while self._index < len(self._lines):
            line = self._lines[self._index]
            if line.startswith("@@") or line.startswith("diff --git "):
                break
            if line.startswith("--- "):
                # Plain-diff file boundary inside what we thought was a hunk.
                break

            if line.startswith("\\"):
                # "\ No newline at end of file" marker; not a content line.
                self._index += 1
                continue

            if line.startswith("+"):
                hunk.added_line_numbers.append(new_line)
                new_line += 1
                consumed_new += 1
            elif line.startswith("-"):
                hunk.deleted_line_numbers.append(old_line)
                old_line += 1
                consumed_old += 1
            elif line.startswith(" "):
                old_line += 1
                new_line += 1
                consumed_old += 1
                consumed_new += 1
            else:
                # An empty line within a hunk is treated as a context line
                # (some diff producers emit a bare empty string for a blank
                # context line instead of a leading space).
                old_line += 1
                new_line += 1
                consumed_old += 1
                consumed_new += 1

            self._index += 1

            # Stop once both sides are saturated. This guards against diffs
            # where the hunk counts are smaller than the emitted lines.
            if consumed_old >= old_count and consumed_new >= new_count:
                # Peek: if the next line is still a content line belonging to
                # this hunk (not a header), keep consuming to be tolerant of
                # inaccurate counts. Otherwise break.
                if self._index >= len(self._lines):
                    break
                nxt = self._lines[self._index]
                if nxt.startswith("+") or nxt.startswith("-") or nxt.startswith(" "):
                    # Only continue if there are still lines expected; otherwise
                    # this is the natural end of the hunk.
                    if consumed_old < old_count or consumed_new < new_count:
                        continue
                    break
                break

        return hunk


def read_input(stream: TextIO) -> List[str]:
    """Read all lines from a text stream without dropping trailing content."""
    data = stream.read()
    if not data:
        return []
    # splitlines avoids creating a phantom trailing empty line, while still
    # preserving interior blank lines that are meaningful in a diff.
    return data.splitlines()


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        description="Parse a unified diff into a JSON list of changed files."
    )
    parser.add_argument(
        "source",
        nargs="?",
        default="-",
        help="Path to a unified diff file, or '-' to read from stdin (default).",
    )
    args = parser.parse_args(argv)

    if args.source == "-":
        lines = read_input(sys.stdin)
    else:
        try:
            with open(args.source, "r", encoding="utf-8", errors="replace") as fh:
                lines = read_input(fh)
        except OSError as exc:
            print(f"error: cannot read {args.source}: {exc}", file=sys.stderr)
            return 1

    if not lines:
        print("error: empty input, no diff found", file=sys.stderr)
        return 1

    parser_state = DiffParser(lines)
    changes = parser_state.parse()

    if not changes:
        print("error: no file changes found in input", file=sys.stderr)
        return 1

    json.dump([c.to_dict() for c in changes], sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
