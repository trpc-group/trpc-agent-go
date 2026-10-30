# OpenAI v3 model module

This module contains tRPC-Agent-Go adapters for the OpenAI Go SDK v3
generation. Its module path is
`trpc.group/trpc-go/trpc-agent-go/model/openai/v3`, and it requires Go 1.25.
The current implementation provides the OpenAI Responses API adapter in
`responses`; it does not provide a Chat Completions adapter. The existing
`model/openai` package and `openai.New` remain available for Chat Completions.

## SDK and module versions

The `/v3` path identifies the SDK major generation. This module's semantic
versions are released independently of the SDK minor and patch versions. The
current SDK pin is `v3.50.0`; a module release `v3.0.0` does not imply SDK
`v3.0.0`.

Compatible additions and fixes stay in this v3 module. Adapters for a future
SDK v4 generation would use a separate `model/openai/v4` module. Cross-generation
API compatibility is not guaranteed. Existing users can continue using their
selected generation; this does not change the legacy `model/openai` package.

## Release

Release a root framework version containing the required replay types first.
Then update this module's root framework dependency and verify it from an
external consumer without repository `replace` directives before publishing
this module. The Go module tag for the first v3 module release is
`model/openai/v3.0.0`.

See the [Responses adapter contract](responses/README.md) and the
[English model guide](../../../docs/mkdocs/en/model.md#openai-responses-api)
or [Chinese model guide](../../../docs/mkdocs/zh/model.md#openai-responses-api).
