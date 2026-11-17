# Cancellation series validation

Tracking issue: [#2757](https://github.com/trpc-group/trpc-agent-go/issues/2757).
The six stacked draft PRs are #2758–#2763. Baseline: `2ec44b2f`.
Validation was performed on Linux amd64 with Go 1.24.11 for the root module,
automatic module toolchains (Go 1.25 for E2E), and golangci-lint 1.64.8.

| Check | Result |
| --- | --- |
| Root `go build ./...` and `go test ./...` | Passed |
| Separate E2E module `go test ./...` | Passed |
| Library test script | All 73 selected modules covered across script runs |
| Examples tidy/build script | All 29 selected modules passed |
| Repository lint, gofmt/`any`, goimports | Passed |
| Each intermediate PR: repository lint and affected-package tests | Passed |
| Combined cancellation regression suite with `-race -count=10` | Passed |
| Non-cooperative execution observation and recovery concurrency | Repeated race checks passed |
| Occupied concurrency gate / callback context replacement | 20 race-enabled repetitions passed |
| SQLite close/reopen recovery | Three race-enabled repetitions passed |

The module script initially encountered an unrelated stale `/tmp/go.work`.
`GOWORK=off` isolated subprocess fixtures from it. The sqlite-vec module also
needed SQLite C headers; validation supplied the matching bundled headers from
its existing go-sqlite3 v1.14.32 dependency through a local include directory.
The remaining modules were then run using the script's `--module` selection.
No system package or repository dependency version was changed for these fixes.

Broad Runner race testing exposed existing chain-invocation and summary mock
races, reproduced on unchanged baseline. Those broad race tests are not claimed
as passing. The new cancellation suites passed race detection separately.

After redistributing helpers and regression fixes into their owning PRs, each
intermediate branch passed lint and affected-package tests independently. The
restacked final source tree was verified identical to the fully tested snapshot;
only this validation report was updated afterward.

## Live provider checks

Explicitly gated tests used the configured Hunyuan-compatible endpoint:

```sh
# Configure OPENAI_BASE_URL, OPENAI_API_KEY and MODEL_NAME outside the repository.
TRPC_CANCEL_LIVE=1 go test ./runner -run '^TestCancellationLive_' -v -count=1
```

Checks covered cancellation before tool entry with cleanup, cancellation during
streaming, a real result returned after cancellation, settlement observation,
next-turn history with a completed sibling and a not-dispatched explanation,
and safe Resume reusing A while executing B exactly once. Local counters were
the only tool side effects. Provider acceptance and final responses were checked;
credentials and endpoint values were not committed.

One initial Resume live run produced no final text within a smaller generation
budget. The successful rerun used a bounded larger budget and disabled new
model-requested tool calls so the counter assertions isolated Resume itself.
These tests do not establish remote inference termination, rollback or billing.

## Contract review

A separate public-contract pass checked default cancellation behavior, cleanup,
optional tracking and observation, unchanged Runner/SessionService method sets,
strict legacy migration, snapshot versions and retention, synchronous-storage
requirements, native-provider refusal, event identity and local concurrency
limits. Pure state-only late events retain audit data in an immutable state key;
normal/fallback submissions share acknowledged results without blindly retrying
ambiguous writes. The [usage guide](cancellation.md) documents these boundaries.
