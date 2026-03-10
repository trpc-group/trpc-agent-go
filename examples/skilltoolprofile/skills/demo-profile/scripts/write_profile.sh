#!/bin/sh
set -eu

out_file="${1:-out/profile.txt}"
out_dir=$(dirname "$out_file")
mkdir -p "$out_dir"

printf 'skill tool profile demo output\n' > "$out_file"
printf 'wrote %s\n' "$out_file"
