---
name: envdump
description: Dump basic environment info to out/env.txt.
metadata:
  { "openclaw": { "requires": { "bins": ["bash"] } } }
---

Overview

This skill collects basic system/environment info and writes it to `out/`.

Command

bash scripts/envdump.sh out/env.txt

Output Files

- out/env.txt
