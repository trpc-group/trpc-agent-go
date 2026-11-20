# Decision models

Use `decision.Decider` to ask several independent questions about the same
evidence. A predicate reports the probability that a proposition is true, a
choice selects an option ID, and a score rates the evidence against ordered
levels. Callers choose the evidence, thresholds, and subsequent action.

## Choose a backend

- `decision/systemone` uses the System One protocol for Jev and compatible Laya
  servers. Set the API root in `systemone.Config.BaseURL` without
  `/v1/systemone`. Configure the key and model required by the server; local Laya
  can omit them when its deployment provides defaults.
- `model/openai/sdkv3/decisions` uses the official [OpenAI Decisions API][api]
  through the existing SDK v3 module (Go 1.25+). Set `OPENAI_API_KEY` and
  optionally `OPENAI_BASE_URL`, or pass OpenAI SDK options to `decisions.New`.
  The SDK appends `decisions` to the base URL, which normally ends in `/v1`.

The [minimal example](../model/openai/sdkv3/examples/decisions/main.go) changes
only the configured backend. From `model/openai/sdkv3`, after configuring the
credentials for your endpoint, run:

```sh
go run ./examples/decisions -backend openai -model gpt-6-luna
go run ./examples/decisions -backend systemone -model "$SYSTEMONE_MODEL"
```

For the second command, set `SYSTEMONE_BASE_URL` and, if required,
`SYSTEMONE_API_KEY` and `SYSTEMONE_MODEL`. Run the same request against
deterministic local servers without credentials using:

```sh
go test ./decisions -run '^TestDecideAcrossBackends$' -v
```

## Interpret a response

- Supply either `Input.Text` or `Input.JSON`. JSON evidence is an object or an
  array of objects. It is sent as a native `input_json` part on OpenAI and as
  structured `state` on System One.
- Read `Response.Answers` by question ID. An `Answered` predicate with
  probability zero is a valid result. An explicit OpenAI refusal is
  `Refused`; an omitted named answer is `Unavailable`. Other valid answers in
  the same response remain usable. The System One client requires a complete
  matching response, so a missing System One answer is a call error.
- Score levels start at zero and may yield a fractional score. For both choice
  and score, distributions are keyed by the option IDs sent in the request.
  Confidence keeps its provider meaning, and thresholds should be evaluated
  separately for each backend. Unreported usage and truncation fields remain
  unset. Laya-specific routing and confidence gate annotations are outside the
  common contract.
- An invalid request, transport error, or mismatched answer fails the call.
  Set an appropriate context deadline for your application.

[api]: https://developers.openai.com/api/reference/go/resources/decisions/methods/create
