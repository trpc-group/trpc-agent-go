#!/usr/bin/env bash
set -euo pipefail

report="${1:?report path required}"

if grep -Eiq '(api[_-]?key|token|password|authorization: bearer)[[:space:]]*[:=][[:space:]]*["'\'']?[A-Za-z0-9._~+/=-]{8,}' "$report"; then
  echo "redaction check failed: possible secret found" >&2
  exit 1
fi

if grep -Eq -e '-----BEGIN [A-Z ]*PRIVATE KEY-----' "$report"; then
  echo "redaction check failed: private key block found" >&2
  exit 1
fi

echo "redaction check passed"
