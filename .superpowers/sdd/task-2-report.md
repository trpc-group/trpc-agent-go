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

---

## Task 2 review follow-up fixes

Status: completed

This follow-up applies the Important findings from the Task 2 review while
keeping the change scoped to the audit/permission path and tests.

Per instruction, this follow-up also followed SDD rather than TDD:
implementation first, then regression updates and test execution.

### Review findings addressed

1. Removed the additive exported `Report.AuditDeniedPaths` field to preserve
   source compatibility for `safety.Report` literals, including unkeyed use.
2. Moved denied-path audit context into private permission-policy state instead
   of the public `Report` type.
3. Preserved explicit-empty denied-path semantics for `*DefaultScanner` instead
   of falling back to `DefaultPolicy()` at audit time.
4. Reworked the custom-scanner regression to use a default sensitive path
   (`/etc/passwd`) plus a secret, without relying on a new public report field.

### Follow-up implementation

1. Changed `auditEventFromReport(...)` to accept denied paths as a private
   argument from the permission/audit path:

   - `auditEventFromReport(report Report, deniedPaths []string)`
   - `redactAuditRecommendation(recommendation string, deniedPaths []string)`

2. Added private denied-path state to `permissionPolicy`:

   - `auditDeniedPaths []string`

3. Initialized that private state with a copied path list via
   `auditDeniedPathsForScanner(scanner Scanner)`:

   - if `scanner` is `*DefaultScanner`, copy `defaultScanner.policy.DeniedPaths`
   - otherwise, copy `DefaultPolicy().DeniedPaths`

   This keeps policy-specific denied-path knowledge private and avoids claiming
   scanner-specific path configuration for arbitrary external `Scanner`
   implementations.

4. Removed the earlier `DefaultScanner.Scan(...)` assignment into `Report`.

5. Updated the custom-scanner audit regression so it now verifies a scanner
   recommendation containing:

   - secret: `password=hunter2`
   - default sensitive path: `/etc/passwd`

   The JSONL output is asserted not to contain either raw value and to contain
   `"redacted":true`.

6. Added a focused regression for explicit-empty default-scanner denied paths:

   - create `MustDefaultScanner(Policy{DisableDefaultDenies: true, DeniedPaths: []string{}})`
   - derive audit denied paths through the new private helper
   - assert `/etc/passwd` is not redacted when denied paths are explicitly empty
   - still assert the inline secret is redacted

### Follow-up files changed

- `E:\trpc-agent-go-pr2136\tool\safety\audit.go`
- `E:\trpc-agent-go-pr2136\tool\safety\permission.go`
- `E:\trpc-agent-go-pr2136\tool\safety\permission_test.go`
- `E:\trpc-agent-go-pr2136\tool\safety\scanner.go`
- `E:\trpc-agent-go-pr2136\tool\safety\types.go`

### Follow-up commands run

1. Format changed files

   ```powershell
   gofmt -w 'tool\safety\audit.go' 'tool\safety\permission.go' 'tool\safety\permission_test.go' 'tool\safety\scanner.go' 'tool\safety\types.go'
   ```

   Result: success

2. Focused audit/package regression subset

   ```powershell
   go test ./tool/safety -run 'Test(PermissionPolicy|JSONLAuditWriter|Audit)' -count=1
   ```

   Output:

   ```text
   ok  	trpc.group/trpc-go/trpc-agent-go/tool/safety	1.369s
   ```

3. Full safety package tests

   ```powershell
   go test ./tool/safety
   ```

   Output:

   ```text
   ok  	trpc.group/trpc-go/trpc-agent-go/tool/safety	1.382s
   ```

### Follow-up self-review

#### Spec check

- The public `AuditWriter` and `JSONLAuditWriter` APIs remain unchanged.
- No additive public `Report` field remains.
- Policy-specific denied-path context is private to the permission/audit path.
- Custom scanners only get default denied-path audit redaction unless an
  explicit contract exists in the future.
- Explicit empty denied-path policy on `*DefaultScanner` stays empty at audit
  time.

#### Standards check

- Production/test changes remain scoped to this task.
- No new dependencies were added.
- Existing file headers were preserved.
- Changed Go files were formatted with `gofmt`.

### Follow-up concerns

1. Custom scanners still cannot provide scanner-specific denied-path audit
   redaction without a new explicit contract, which is intentional for now.
2. The report file remains inside `.superpowers/sdd/`, which is ignored by the
   local `.gitignore`, so updates require force-adding when committing.
