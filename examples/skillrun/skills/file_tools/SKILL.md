---
name: file-tools
description: Simple shell utilities for files and archives.
---

Overview

Use these examples to explore basic shell commands inside a skill
workspace. The assistant can run them and return results and files.

Examples

1) List files in the workspace

   Command:

   ls -la

2) Write a sample file to out/sample.txt

   Command:

   bash scripts/write_sample.sh "Hello from skill" out/sample.txt

3) Create a tar.gz archive of the out/ folder

   Command:

   tar -czf out/sample.tgz -C out .

Output Files

- out/sample.txt
- out/sample.tgz
