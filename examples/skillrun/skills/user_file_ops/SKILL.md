---
name: user-file-ops
description: Simple operations on user-provided text files including summarization.
---

Overview

Summarize text files that come from the user or from other skills.
This skill can compute basic statistics (lines, words, bytes) and
capture a short preview of the file.

User-provided files are typically exposed under `work/inputs/` (for
example, when a host directory is mounted as inputs). Files produced
by other skills are usually written under `out/` and can be
summarized directly from there.

Examples

1) Summarize a text file already present in the workspace

   Command:

   bash scripts/summarize_file.sh \
     work/inputs/example.txt \
     out/example_summary.txt

2) Summarize a different file

   Command:

   bash scripts/summarize_file.sh \
     work/inputs/notes.txt \
     out/notes_summary.txt

3) Summarize a file produced by another skill

   Command:

   bash scripts/summarize_file.sh \
     out/sample_fib.txt \
     out/sample_fib_summary.txt

Output Files

- out/example_summary.txt
- out/notes_summary.txt
- out/sample_fib_summary.txt
