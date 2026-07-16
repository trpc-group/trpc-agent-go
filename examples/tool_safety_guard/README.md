# Tool Safety Guard (scan-only example)

This example evaluates tool requests without executing any command or code. It demonstrates strict policy loading, structured findings, metadata-only JSONL auditing, and the `allow` / `ask` / `deny` decision model.

## Run

From the repository's `examples` directory:

```bash
go run ./tool_safety_guard -policy ./tool_safety_guard/tool_safety_policy.yaml -output-dir ./tool_safety_guard/output
```

The command scans 16 public samples and writes:

- `output/tool_safety_report.json`: one structured report per sample;
- `output/tool_safety_audit.jsonl`: one metadata-only audit event per scan.

No sample is executed. The program exits non-zero if a sample's decision differs from its expected result.
Committed reference outputs are available in [`sample/`](sample/).

Run its automated check with:

```bash
go test ./tool_safety_guard
```

## Decision semantics

- `allow`: the request has no enabled finding that requires intervention.
- `ask`: execution must pause for explicit human approval.
- `deny`: execution must not occur.

When several rules match, the strongest action wins: `deny > ask > allow`. A failed audit write also fails closed.

The example policy intentionally permits only exact network domains and explicit `*.suffix` wildcards. A wildcard does not match the suffix's apex. YAML and JSON policies reject unknown fields, duplicate keys, trailing documents or values, unsupported versions, and invalid actions.

For framework and direct-tool integration, threat model, telemetry, and operational guidance, see [Tool Safety Guard](../../docs/tool-safety-guard.md). A Chinese version is available in [README.zh-CN.md](README.zh-CN.md).
