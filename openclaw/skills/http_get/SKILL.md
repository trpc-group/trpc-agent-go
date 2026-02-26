---
name: http_get
description: Fetch a URL with curl and write it to out/.
metadata:
  { "openclaw": { "requires": { "bins": ["bash", "curl"] } } }
---

Overview

This skill fetches a URL using `curl` and writes the response body to `out/`.

Command

bash scripts/http_get.sh https://example.com out/example.html

Output Files

- out/example.html
