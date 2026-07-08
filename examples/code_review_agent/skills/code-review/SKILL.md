---
name: code-review
description: Go code review agent with static analysis, sandbox execution, and security scanning
---

Overview

This skill provides automated code review capabilities for Go projects, including:
- Static analysis (go vet, staticcheck)
- Unit test execution
- Security scanning (secrets detection)
- Pattern-based rule matching

Capabilities

1. Static Analysis
   - Run go vet to detect common Go issues
   - Run staticcheck for deeper analysis
   - Parse diff files to identify changed code

2. Security Scanning
   - Detect hardcoded secrets and API keys
   - Identify sensitive information leaks
   - Check for goroutine leaks
   - Verify proper resource cleanup

3. Test Execution
   - Run unit tests in sandbox
   - Capture test coverage
   - Report failures with context

Security Limits

- All commands run in isolated sandbox environment
- Command timeout: 60 seconds per execution
- Output size limit: 1MB per command
- High-risk commands require human review
- Sensitive data is automatically redacted

Commands

1. Run full static analysis on diff
   bash scripts/run_static_analysis.sh <diff_file>

2. Parse diff and extract changed files
   bash scripts/parse_diff.sh <diff_file>

3. Check for secrets
   bash scripts/check_secrets.sh <file_path>

4. Run go vet
   bash scripts/run_go_vet.sh <package_path>

5. Run staticcheck
   bash scripts/run_staticcheck.sh <package_path>

Input Parameters

- --diff-file: Path to unified diff file
- --repo-path: Path to local repository
- --output-dir: Output directory for reports
- --db-path: SQLite database path
- --fixture: Run specific test fixture
- --dry-run: Run without LLM, only static analysis

Output Files

Shell script outputs (in `out/` directory relative to script execution):
- out/go_vet_results.txt: go vet analysis results
- out/staticcheck_results.txt: staticcheck analysis results
- out/secrets_scan.txt: Secrets detection results

Go program outputs (in `output/` directory specified by --output-dir):
- output/review_report.json: Structured findings
- output/review_report.md: Human-readable report

Risk Classification

- HIGH: Critical security issues, data leaks, goroutine leaks
- MEDIUM: Resource leaks, missing error handling
- LOW: Code style issues, best practice violations
- REVIEW: Requires human review, uncertain findings

Dry-Run Mode

When dry-run is enabled:
- No LLM calls are made
- Only static analysis and rule matching run
- Full report generation still works
- Useful for testing and CI integration