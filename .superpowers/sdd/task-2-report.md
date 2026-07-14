## Task 2 report

Status: completed

Branch: `feat/safety-scanner`

Base context: Task 1 complete through `c34068d2`

### Requirement handled

Close the audit persistence gap where scanner-controlled `Report.Recommendation`
strings could be written to JSONL verbatim, including secrets or sensitive
paths, and ensure `AuditEvent.Redacted` is true when audit-time sanitization
changes the recommendation.

Per instruction, this task followed SDD rather than TDD: implementation first,
then the regression test.

### Root cause

`tool/safety/audit.go:auditEventFromReport` copied
`report.Recommendation` directly into `AuditEvent.Recommendation`.

`DefaultScanner` already redacted report command/evidence/finding text with:

- `redactString(...)` for secrets
- `redactSensitivePath(...)` for denied paths

but that logic was not applied to audit recommendations, and the audit boundary
did not previously carry policy-specific denied-path context for recommendation
redaction.

### Implementation

1. Shared the existing secret/path redaction flow for audit recommendation
   sanitization by introducing `redactReportTextWithDeniedPaths(...)`.
2. Updated `auditEventFromReport(...)` to:
   - redact `Report.Recommendation` before constructing the audit event
   - keep the recommendation single-line via the existing `singleLine(...)`
     helper
   - set `AuditEvent.Redacted` when audit-time sanitization changes the
     recommendation, even if `Report.Redacted` was originally false
3. Threaded denied-path context into `Report` via `AuditDeniedPaths` so the
   audit boundary can use actual configured denied paths when they are known.
4. Updated `DefaultScanner.Scan(...)` to populate `Report.AuditDeniedPaths`
   from `s.policy.DeniedPaths`.
5. Added a regression that uses a custom `ScannerFunc` returning a
   recommendation containing both:
   - a secret (`password=hunter2`)
   - a configured sensitive path (`/srv/team/private/secrets/api.env`)

   The test writes through `NewJSONLAuditWriter(...)` and verifies the emitted
   JSON does not contain either raw value while it does contain redacted output
   and `"redacted":true`.

### Files changed

- `E:\trpc-agent-go-pr2136\tool\safety\audit.go`
- `E:\trpc-agent-go-pr2136\tool\safety\scanner.go`
- `E:\trpc-agent-go-pr2136\tool\safety\types.go`
- `E:\trpc-agent-go-pr2136\tool\safety\permission_test.go`

### Commands run

1. Format changed files

   ```powershell
   gofmt -w 'tool\safety\audit.go' 'tool\safety\scanner.go' 'tool\safety\types.go' 'tool\safety\permission_test.go'
   ```

   Result: success

2. Focused audit/package regression subset

   ```powershell
   go test ./tool/safety -run 'Test(PermissionPolicy|JSONLAuditWriter|Audit)' -count=1
   ```

   Output:

   ```text
   ok  	trpc.group/trpc-go/trpc-agent-go/tool/safety	2.425s
   ```

3. Full safety package tests

   ```powershell
   go test ./tool/safety
   ```

   Output:

   ```text
   ok  	trpc.group/trpc-go/trpc-agent-go/tool/safety	1.864s
   ```

### Self-review

#### Spec check

- Audit recommendation strings are now sanitized before JSONL persistence.
- `AuditWriter` and `JSONLAuditWriter` signatures were left unchanged.
- Existing permission decision behavior was not changed.
- The regression covers a custom scanner recommendation containing both a secret
  and a sensitive path and verifies redacted JSONL output.

#### Standards check

- No new dependencies were added.
- Changes are limited to the exact safety/audit path plus the regression test.
- Existing file headers were preserved.
- Changed Go files were formatted with `gofmt`.

### Concerns

1. `Report` gained an additive exported field, `AuditDeniedPaths`, because the
   audit boundary only receives a `Report` and otherwise cannot know
   policy-specific denied paths for public/custom scanners.
2. When a custom scanner does not provide `AuditDeniedPaths`, audit-time path
   redaction falls back to `DefaultPolicy().DeniedPaths`; this protects default
   sensitive paths without falsely implying access to scanner-specific custom
   path configuration.
