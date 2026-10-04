# System One client

This internal package provides a shared Go client for Jev and Laya. Callers pass
business structs, maps, slices, or strings as `Request.State`; the client uses
`encoding/json`, including an optional custom `MarshalJSON` implementation.
The encoded state must be a JSON string, object, or array. Callers do not need
to construct `json.RawMessage`.

Use `BinaryQuestion` for P(true), `ChoiceQuestion` for named options, and
`ScoreQuestion` for ordered levels. Instructions must contain nonblank UTF-8
text. Criteria and descriptions are text-only; structured instructions/criteria
from the earlier API draft are no longer accepted. Binary questions still use
`noul` on the wire. Score answers expose `Levels []ScoreLevel`, pairing each
returned text description with its probability in ordinal order; structured
legends are rejected rather than converted to text.

Read answers with `Response.Binary(id)`, `Response.Choice(id)`, or
`Response.Score(id)`. These return the original answer objects and descriptive
errors for missing IDs, wrong types, or nil answers. See `example_test.go` for
a complete executable example and `DESIGN.md` for ownership and validation rules.

## Integration tests

The package has two test layers:

- Ordinary tests use `httptest` and synthetic Jev/Laya fixtures. They require no
  model service or credentials and run with `go test ./internal/systemone`.
- `integration_test.go` has separate `TestIntegrationJev` and
  `TestIntegrationLaya` core suites. Each uses its own endpoint and configuration,
  requires the `integration` build tag, and can incur inference charges. Missing
  URL configuration skips only that provider; configured authentication,
  connection, and protocol failures fail its suite.

`integration_extended_test.go` adds `TestIntegrationJevExtended` and
`TestIntegrationLayaExtended` for object-array state and single-level scoring.
These are valid client inputs, but some hosted gateways accept only text arrays
and require at least two score levels. Core suites use text arrays and two or
three score levels. Passing core cases does not establish support for extended
inputs. Extended tests still fail when a server rejects them; no HTTP error is
converted into a pass or an automatic skip.

The production client does not read environment variables. The following
configuration belongs only to the integration test harness. The two suites share
protocol cases, not credentials, model selection, or HTTP clients.

## Configuration

| Jev variable | Laya variable | Meaning |
| --- | --- | --- |
| `JEV_BASE_URL` | `LAYA_BASE_URL` | Explicit HTTP(S) API root, optionally including a gateway prefix. Do not append `/v1/systemone`. Missing URL skips that provider. |
| `JEV_API_KEY` | `LAYA_API_KEY` | Bearer credential. Jev requires a key once its URL is configured; Laya may omit it for an unauthenticated server. |
| `JEV_MODEL` | `LAYA_MODEL` | Jev requires an available model name. Official `laya-serve` supports omission for automatic routing; hosted deployments may require their own model ID. |
| `JEV_TIMEOUT` | `LAYA_TIMEOUT` | Positive Go duration for each HTTP call, including response reading. Defaults to `60s` independently for each provider. |
| `JEV_LOG_ERROR_BODY` | `LAYA_LOG_ERROR_BODY` | Optional boolean, default `false`. Logs a bounded HTTP error response body for troubleshooting. The configured API key is redacted; other echoed request data may remain. |

For Jev, set `JEV_BASE_URL=https://api.typesafe.ai`, configure `JEV_API_KEY`
through your local credential setup, and set `JEV_MODEL` to a model available to
your account. For an existing local Laya server, set `LAYA_BASE_URL` to its API
root (for example `http://localhost:8000`) and configure model/authentication only
if required by that deployment. The test does not start a server, download
models, or discover model names.

For a third-party Laya deployment, verify that it exposes the System One
`POST /v1/systemone` request and response contract. Hosting Laya weights does not
by itself establish API compatibility. Use that platform's model ID and
authentication requirements; a different inference API needs a separate adapter.

The earlier generic `SYSTEMONE_*` test variables and `TestIntegrationSystemOne`
entry point have been replaced. Set the provider-specific variables instead;
neither suite falls back to the other provider's settings or a generic endpoint.

Run the core suites separately from the repository root:

```sh
go test -tags=integration ./internal/systemone \
  -run '^TestIntegrationJev$' -count=1 -v -timeout=15m

go test -tags=integration ./internal/systemone \
  -run '^TestIntegrationLaya$' -count=1 -v -timeout=15m
```

With both providers configured, run both core suites in one invocation:

```sh
go test -tags=integration ./internal/systemone \
  -run '^TestIntegration(Jev|Laya)$' -count=1 -v -timeout=20m
```

The report shows each provider and case separately, for example
`TestIntegrationJev/ChoiceStringArrayState` and
`TestIntegrationLaya/ChoiceStringArrayState`. A skipped suite is not evidence that its
provider passed, even when the overall command succeeds.

`-count=1` disables result caching. Increase the overall `-timeout` when increasing
a provider timeout. The core Jev suite makes eight calls; the core Laya suite
makes seven, or eight when its model is configured. Each extended suite adds two
calls. No automatic retries or parallel calls are made. A focused
Laya check is:

```sh
go test -tags=integration ./internal/systemone \
  -run '^TestIntegrationLaya$/^MixedQuestionsStructState$' \
  -count=1 -v -timeout=3m
```

To check the extended inputs as well, include the extended suite explicitly:

```sh
go test -tags=integration ./internal/systemone \
  -run '^TestIntegrationJev(Extended)?$' -count=1 -v -timeout=15m

go test -tags=integration ./internal/systemone \
  -run '^TestIntegrationLaya(Extended)?$' -count=1 -v -timeout=15m
```

Running with `-tags=integration` and no `-run` filter includes both core and
extended suites for every configured provider. A gateway that rejects extended
inputs will fail that run, even if its core suite passes. Record those results
separately. The former `TestIntegrationLaya/ChoiceArrayState` case is now
`TestIntegrationLayaExtended/ChoiceObjectArrayState` (likewise for Jev).
`ScoreSingleLevel` also moved to the extended suite. Neither case was removed
from live coverage.

For an HTTP failure such as 422, enable error-body logging for a single failing
case to see the server's validation details:

```sh
LAYA_LOG_ERROR_BODY=true go test -tags=integration ./internal/systemone \
  -run '^TestIntegrationLaya$/^BinaryStringState$' \
  -count=1 -v -timeout=3m
```

This logs up to 4096 bytes after redacting the configured key, with a marker if
the log was truncated. `server_body_truncated` separately reports whether the
client's 8 MiB response limit truncated the body. The command-scoped variable
does not enable logging in subsequent runs. Error bodies may echo input or
deployment details; review them before sharing. The production client's
`HTTPError.Error()` continues to omit response bodies.

An HTTP 422 alone does not identify the invalid field. Inspect the returned
`detail` and the deployed server's schema/version before changing requests or
making `LAYA_MODEL` mandatory; the referenced Laya server supports an omitted
model. A deployment with a different schema may impose different requirements.

## Coverage and assertions

| Subtest | Request contract exercised |
| --- | --- |
| `BinaryStringState` | String state and a binary question without explicit criteria. |
| `BinaryCriteriaObjectState` | Object state and explicit true/false descriptions. |
| `ChoiceStringArrayState` | Text array state and ordered named options with descriptions. |
| `ChoiceLabelsOnly` | Choice descriptions omitted and encoded as null. |
| `ScoreOrdinalLevels` | Three ordinal levels; fractional scores are accepted. |
| `ScoreTwoLevels` | Two ordinal levels; scores range from zero to one. |
| `MixedQuestionsStructState` | One request containing all three question types, a caller-defined state struct, Unicode question IDs, and pointer questions. |
| `ExplicitRequestModel` | A model supplied directly on the request; always included for Jev; included for Laya when `LAYA_MODEL` is configured. |

The extended suites cover these additional cases:

| Subtest | Request contract exercised |
| --- | --- |
| `ChoiceObjectArrayState` | Conversation state as an array of role/content objects. |
| `ScoreSingleLevel` | Single-level boundary, whose only valid score is zero. |

Every response must match all requested question IDs and answer types. Choice
labels and score levels must match the request. Probabilities and confidence
must be finite and in range; distributions tolerate per-entry four-decimal
rounding. Score levels pair textual legends with probabilities in ordinal order. Response model,
raw JSON, and optional usage are inspected; missing token counts or truncation
metadata are not interpreted as zero or false. Resolved model names need not
equal the configured alias.

The suite checks protocol compatibility, not classification accuracy or approval
policy. It does not assert a particular selected option, exact probability, or
common confidence threshold across providers. It does not require Laya-only
extensions from Jev or strict-mode Laya. By default, logs contain response model,
request ID, latency, and available usage metadata, not credentials or full
request/response bodies. Error-body logging requires the diagnostic option above.

Malformed responses, HTTP errors, redirect refusal, transport cancellation,
timeouts, and request encoding are tested deterministically by the ordinary
local tests. Live timing or deliberate authentication failures are not used as
portable provider assertions. Client cancellation does not prove that a remote
model stopped inference.

The test owns an HTTP transport and closes its idle connections with `t.Cleanup`.
Each call has a fresh bounded context. There are no persistent remote resources
to delete. A pass against a local fixture server only validates the test harness;
provider compatibility requires a pass against the actual configured deployment.
