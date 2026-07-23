# Code Review Report

- Task ID: `review-sample-secret-leak`
- Status: `completed`
- Conclusion: `findings`
- Runtime: `fake`
- Diff SHA-256: `45c28f3dbb83644041f9d5e7f039123ef45525bc85c4701bea82a8f75f05dce6`
- Duration: 4 ms

## Summary

- Findings: 1
- Warnings: 0
- Commands: planned 3, allowed 3, blocked 0
- Permission blocks: 0

## Findings

- `high` config.go:3 Hardcoded secret-like value
  Recommendation: Move secrets to a managed secret store or environment-provided configuration.

## Needs Human Review

None.

## Governance

Filter decisions:
- `checkGoVersion`: `allow`
- `checkGoTest`: `allow`
- `checkGoVet`: `allow`

Permission decisions:
- `checkGoVersion`: `allow`
- `checkGoTest`: `allow`
- `checkGoVet`: `allow`

## Sandbox

- `checkGoVersion`: ok, exit 0, 1 ms
- `checkGoTest`: ok, exit 0, 1 ms
- `checkGoVet`: ok, exit 0, 1 ms

## Metrics

- Sandbox duration: 3 ms
- Redactions: 1
- Suppressed matches: 0

## Reports

- JSON: `output/review-sample-secret-leak/review_report.json`
- Markdown: `output/review-sample-secret-leak/review_report.md`
