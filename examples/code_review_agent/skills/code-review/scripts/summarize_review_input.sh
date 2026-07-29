#!/bin/sh
set -eu

input=work/review-input.json
test -f "$input"
wc -c < "$input"
