# Safety Guard demo & docs — acceptance checklist

Every item below is verifiable by a single command and has a concrete expected
result. Run from the repo root (`d:\zhuomian\Code\trpc-agent-go`).

## A. Build and format

- [ ] `go build ./...` exits 0 (root module compiles, including the new demo).
- [ ] `go build ./tool/safety/cmd/safety_demo/` exits 0.
- [ ] `gofmt -l tool/safety/cmd/safety_demo/main.go` prints nothing.
- [ ] `gofmt -r 'interface{} -> any' -l tool/safety/cmd/safety_demo/main.go` prints nothing.
- [ ] `goimports -l tool/safety/cmd/safety_demo/main.go` prints nothing (if goimports is installed).
- [ ] License header present at the top of `tool/safety/cmd/safety_demo/main.go` (Tencent Apache 2.0).

## B. Existing safety tests still pass

- [ ] `go test ./tool/safety/` exits 0 (whole package green).
- [ ] `go test ./tool/safety/ -run "^TestCorpus$" -v` exits 0 and prints:
  - `total_cases: 62`, `high_risk_cases: 49`, `safe_cases: 10`, `functional_cases: 3`
  - `detection_rate: 1`, `false_positive_rate: 0`
  - Every category line in `=== Per-Category Statistics ===` ends with `OK`.
  - `--- PASS: TestCorpus` at the end.
- [ ] `go test ./tool/safety/ -run "^TestCorpusPolicyHotReload$" -v` exits 0
  (proves `Policy.Reload` flips decision live across three policy files).
- [ ] `go test ./tool/safety/ -run "^TestCorpusEnvWhitelistAndOutput$" -v` exits 0
  (env whitelist filtering + output truncation through `PermissionAdapter.Wrap`).
- [ ] `go test ./tool/safety/ -run "^TestJSONLAuditorBasicWrite$" -v` exits 0
  (JSONL line format with deterministic timestamp `2025-07-13T12:00:00Z`).
- [ ] `go test ./tool/safety/ -run "^TestPermissionAdapterJSONLAuditorEndToEnd$" -v` exits 0
  (adapter writes one JSONL event per scanned call: line 0 deny, line 1 allow).
- [ ] `go test ./tool/safety/ -run "^TestScanner" -v` exits 0 (all Scanner unit tests green).
- [ ] `go test ./tool/safety/ -run "^TestJSONLAuditor" -v` exits 0 (all auditor tests green).
- [ ] `go test ./tool/safety/ -run "^TestSafetySpan" -v` exits 0 (all OTel span tests green).

## C. Runnable demo / CLI

- [ ] `go run ./tool/safety/cmd/safety_demo` exits 0 and prints exactly three result lines:
  - `[0] command="echo hello" decision=allow risk=none intercepted=false`
  - `[1] command="go get example.com/module" decision=ask risk=medium intercepted=true`
  - `[2] command="rm -rf /" decision=deny risk=critical intercepted=true`
- [ ] `go run ./tool/safety/cmd/safety_demo -policy tool/safety/tool_safety_policy.yaml -report <tmp>.json -audit <tmp>.jsonl`
  accepts the flags without error and writes both files to the custom paths.
- [ ] Re-running the demo produces a stable `example_report.json` (byte-identical except
  for `duration_ms`, which may be 0 in either run) and a stable `example_audit.jsonl`
  (byte-identical except for the `timestamp` field, which reflects wall-clock time).

## D. Structured report example

- [ ] `tool/safety/testdata/example_report.json` exists and is valid JSON.
- [ ] It is a JSON array of exactly 3 `Report` objects.
- [ ] Object 0 has `decision: "allow"`, `risk_level: "none"`, `intercepted: false`,
  and no `evidences` key (omitted when empty).
- [ ] Object 1 has `decision: "ask"`, `risk_level: "medium"`, `intercepted: true`,
  and exactly one evidence with `rule_id: "dependency-change"`.
- [ ] Object 2 has `decision: "deny"`, `risk_level: "critical"`, `intercepted: true`,
  and at least two evidences including `command-policy-denied` and `dangerous-delete`.
- [ ] No object contains a `secret_not_in_report` leak — the `redacted` field is
  present on every object.

## E. JSONL audit example

- [ ] `tool/safety/testdata/example_audit.jsonl` exists and has exactly 3 lines.
- [ ] Each line is a standalone JSON object (parses with `json.Unmarshal`).
- [ ] Line 0 has `decision: "allow"`, no `rule_ids` key (omitted when empty),
  `intercepted: false`.
- [ ] Line 1 has `decision: "ask"`, `rule_ids: ["dependency-change"]`,
  `intercepted: true`.
- [ ] Line 2 has `decision: "deny"`,
  `rule_ids: ["command-policy-denied","dangerous-delete"]`, `intercepted: true`.
- [ ] No line contains the strings `command`, `matched_snippet`, `reason`, or
  `recommendation` — only metadata fields (`timestamp`, `tool_name`, `backend`,
  `decision`, `risk_level`, `rule_ids`, `duration_ms`, `redacted`, `intercepted`).
- [ ] File mode is `0600` and parent directory is `0700` (verified by `TestJSONLAuditorFilePermissions`).

## F. Corpus run capture

- [ ] `tool/safety/testdata/corpus_run.txt` exists and contains the full
  structured corpus report (starts with `=== Safety Corpus Report ===` and a
  JSON object whose `total_cases` is 62).
- [ ] The capture ends with `--- PASS: TestCorpus` and `ok ... tool/safety`.
- [ ] Every category in `=== Per-Category Statistics ===` ends with `OK`
  (none with `INCOMPLETE`).

## G. Policy file

- [ ] `tool/safety/tool_safety_policy.yaml` loads without error via
  `safety.LoadPolicy` (covered by `TestCorpus` which uses the corpus's own
  `default_policy`; the canonical file is exercised by the demo).
- [ ] The file contains all eight documented fields: `allowed_commands`,
  `denied_commands`, `forbidden_paths`, `network_whitelist`,
  `network_failure_decision`, `max_timeout_ms`, `max_output_bytes`,
  `env_whitelist`.

## H. Documentation accuracy

- [ ] Every Go identifier cited in `PR_DESCRIPTION.md` exists in the source
  tree (no aspirational API names). Spot-check at least:
  - `safety.LoadPolicy`, `safety.NewScanner`, `safety.NewPermissionAdapter`,
    `safety.NewJSONLAuditor`, `safety.WithAuditor`, `safety.WithFailClosed`.
  - `agent.WithToolPermissionPolicy`.
  - `hostexec.NewToolSet`, `workspaceexec.NewExecTool`,
    `codeexecutor.CodeExecutor`.
  - `shellsafe.Parse`, `shellsafe.PolicyFromLists`.
- [ ] Every file path cited in `PR_DESCRIPTION.md` resolves to a real file.
- [ ] The defense-in-depth disclaimer (section 7) explicitly states the guard
  is not a sandbox replacement and lists at least: no isolation, no egress
  enforcement, no process lifetime management, no binary verification.

## I. Multi-module sanity (CI-style, optional but recommended)

- [ ] `bash .github/scripts/run-go-tests.sh` passes across all sub-modules
  (the new `cmd/safety_demo` lives in the root module, so root tests cover it;
  no sub-module `go.mod` was added or changed).
- [ ] `bash .github/scripts/check-examples.sh` still passes (no example was
  added under `examples/`, so this should be unaffected).
