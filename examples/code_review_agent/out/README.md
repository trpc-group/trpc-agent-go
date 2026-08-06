# Runtime output directory

This directory is produced by local `go run` / demos and is **gitignored**.

Typical layout:

- `review.db` — SQLite persistence for the run
- `<taskID>/review_report.json` — structured report for one task
- `<taskID>/review_report.md` — human-readable report for one task

Do not commit secrets or machine-absolute paths from here. For docs and reviews,
use the portable samples under `../testdata/sample_output/`.
