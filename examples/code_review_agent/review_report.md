# Code Review Report

- Task: `sample&#45;review`
- Mode: `fake-model`
- Findings: 1
- Warnings: 0
- Needs human review: 0

## Conclusion

review completed: 1 findings, 0 warnings, 0 human review items, 0 governance blocks, model degraded=true

## Monitoring Summary

- Total duration: `56.665ms`
- Sandbox duration: `46.541µs`
- Tool invocations: 2
- Permission blocks: 0
- Severity counts: `critical=0` `high=1` `medium=0` `low=0` `info=0`
- Error types: `model&#95;error=1` `sandbox&#95;failed=2`

## Sandbox Runs

| Command | Status | Duration | Exit | Truncated |
| --- | --- | --- | --- | --- |
| go test &#46;/&#46;&#46;&#46; | failed | `958ns` | `&#45;1` | false |
| go vet &#46;/&#46;&#46;&#46; | failed | `45.583µs` | `&#45;1` | false |

## Governance Decisions

| Kind | Tool | Action | Rule | Reason |
| --- | --- | --- | --- | --- |
| filter | workspace&#95;exec | allow | filter&#45;review&#45;tools | tool is visible to the review model |
| filter | skill&#95;load | allow | filter&#45;review&#45;tools | tool is visible to the review model |
| filter | skill&#95;select&#95;docs | allow | filter&#45;review&#45;tools | tool is visible to the review model |
| filter | skill&#95;list&#95;docs | allow | filter&#45;review&#45;tools | tool is visible to the review model |
| permission | skill&#95;load | allow | allow&#45;skill&#45;read | fixed review policy decision |
| permission | workspace&#95;exec | allow | allow&#45;go&#45;vet | fixed review policy decision |

## Findings

| Severity | Layer | Location | Rule | Title |
| --- | --- | --- | --- | --- |
| high | unified | `config/config&#46;go:3` | `go/hardcoded&#45;secret/v1` | Hard&#45;coded credential in changed code |

### 1. Hard&#45;coded credential in changed code

- Disposition: `finding`
- Confidence: `high`
- Fingerprint: `60ccbd9cca56bddfd1ee8208bfba2ea67ce0c2c171355bb814c1479f9d82ac73`
- Evidence: An added string or byte&#45;slice literal is assigned to a sensitive identifier; the value is omitted from evidence&#46;
- Recommendation: Load the credential from an approved secret provider and rotate the exposed value&#46;
