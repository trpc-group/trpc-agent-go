#!/bin/bash
set -euo pipefail

FILE_PATH="${1:-}"
if [ -z "$FILE_PATH" ]; then
    echo "Usage: $0 <file_path>"
    exit 1
fi

mkdir -p out

echo "=== Secrets Check ===" > out/secrets_scan.txt

if [ ! -f "$FILE_PATH" ]; then
    echo "Error: File not found: $FILE_PATH" >> out/secrets_scan.txt
    exit 1
fi

SECRET_PATTERNS=(
    "api[_-]?key"
    "secret[_-]?key"
    "access[_-]?token"
    "auth[_-]?token"
    "password"
    "credential"
    "secret"
)

echo "Scanning for potential secrets in: $FILE_PATH" >> out/secrets_scan.txt
echo "" >> out/secrets_scan.txt

for pattern in "${SECRET_PATTERNS[@]}"; do
    matches=$(grep -i -n "$pattern" "$FILE_PATH" | head -20)
    if [ -n "$matches" ]; then
        echo "Found potential '$pattern' references:" >> out/secrets_scan.txt
        echo "$matches" >> out/secrets_scan.txt
        echo "" >> out/secrets_scan.txt
    fi
done

API_KEY_PATTERNS=(
    "sk-[a-zA-Z0-9_-]{20,}"
    "AKIA[A-Z0-9]{16}"
    "[a-zA-Z0-9]{32,}"
)

echo "Scanning for API key patterns:" >> out/secrets_scan.txt
echo "" >> out/secrets_scan.txt

for pattern in "${API_KEY_PATTERNS[@]}"; do
    matches=$(grep -i -n -E "$pattern" "$FILE_PATH" | head -20)
    if [ -n "$matches" ]; then
        echo "Found potential API keys:" >> out/secrets_scan.txt
        echo "$matches" >> out/secrets_scan.txt
        echo "" >> out/secrets_scan.txt
    fi
done

echo "Secrets check completed" >> out/secrets_scan.txt