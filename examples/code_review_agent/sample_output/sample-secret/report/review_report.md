# Code Review Report

Task: sample-secret

Status: completed

Mode: deterministic-rule-only

## Summary

- Findings: 1
- Warnings: 0
- Needs human review: 1
- Files changed: 1
- Sandbox runs: 1

## Findings

| Severity | Category | Location | Issue | Recommendation |
|---|---|---|---|---|
| critical | security | config/config.go:2 | Hard-coded credential-like value - const apiKey = "[REDACTED]" | Move the credential to a secret manager or an injected environment variable, then rotate the exposed value. |

## Warnings

None.

## Human review

| Severity | Category | Location | Issue | Recommendation |
|---|---|---|---|---|
| low | test_coverage | config/config.go:1 | Production Go changes have no accompanying test change - No changed file ends with _test.go. | Add focused tests for changed behavior, error paths, and lifecycle cleanup. |

## Governance decisions

- bash skills/code-review/scripts/diff_stats.sh work/change.diff out/diff_stats.json: allow
## Filter decisions

- 5bc51aef6aba2bb29899bdb7f98f95064654e817672429907c69357b5a4ddd5f: keep to finding - candidate retained after confidence and duplicate filtering
- 25fe5a9c1bb6160059a696f5c881ec8016b3735ac8094153f22d8f5472000070: route_human to needs_human_review - candidate routed to human review because automated confirmation is insufficient

## Sandbox summary

- bash skills/code-review/scripts/diff_stats.sh work/change.diff out/diff_stats.json: skipped; exit=0; timeout=false; duration=0ms; error=dry_run

## Monitoring

- Total duration: 6ms
- Sandbox duration: 0ms
- Tool calls: 1
- Permission denies: 0
- Permission asks: 0
- Severity critical: 1
- Severity low: 1

## Conclusion

Critical findings block merge until remediation and credential rotation are complete.
