# Holdout fixtures

This directory contains "holdout" fixtures used by the quality regression
test `TestHoldoutFixtureQualityThresholds` in `review_test.go`. Borrowed
from competitor PR #2243.

Unlike the main `testdata/fixtures/` directory (which drives the
integration tests in `main_test.go`), the holdout directory is a
separate quality gate:

- **risk-*.diff** — must produce at least one finding. These guard
  against regressions where a rule stops firing on a pattern it was
  supposed to catch.
- **benign-*.diff** — must produce zero critical/high findings. These
  guard against false positives where a rule fires on safe code.

The thresholds are intentionally stricter than the integration tests:
a risk fixture that previously produced 2 findings must still produce
at least 1 (not 0). A benign fixture that produced 0 critical findings
must still produce 0.

Adding a new rule? Add a risk fixture that exercises it. Tightening a
rule? Make sure the benign fixtures still pass.
