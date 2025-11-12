---
name: python-math
description: Small Python utilities for math and text files.
---

Overview

Run short Python scripts inside the skill workspace. Results can be
returned as text and saved as output files.

Examples

1) Print the first N Fibonacci numbers

   Command:

   python3 scripts/fib.py 10 > out/fib.txt

2) Sum a list of integers

   Command:

   python3 - <<'PY'
from sys import stdin
nums = [int(x) for x in stdin.read().split()]
print(sum(nums))
PY

Output Files

- out/fib.txt
