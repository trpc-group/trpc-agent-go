# Runtime output directory

This directory is produced by local `go run` / demos and is **gitignored**.

Typical files:

- `review_report.json` — structured review report
- `review_report.md` — human-readable report
- `review.db` — SQLite persistence for the run

Do not commit secrets or machine-absolute paths from here. For docs and reviews,
use the portable samples under `../testdata/sample_output/`.
