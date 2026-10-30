# OpenAI SDK v3 model module

This independent module contains tRPC-Agent-Go adapters for the OpenAI Go
SDK v3 generation. Its module path is
`trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3`, and it requires Go 1.25.
The current implementation provides the [Responses adapter](responses/README.md).
For Chat Completions, continue using `model/openai` and `openai.New`.

## Module versioning and release

The `sdkv3` path identifies the SDK generation, while this module follows the
framework's `v1.x` release line. The SDK is currently pinned to `v3.74.0`.
The SDK version and this module's release version are independent. Compatible
fixes and additions preserve the module's public API and contracts.

Use the tag format `model/openai/sdkv3/v1.x.x`. For example, a module release
at `v1.12.0` uses `model/openai/sdkv3/v1.12.0`.

Before publishing this module, release a root framework version containing
the required replay types. Then update this module's root framework
dependency and verify it from an external consumer without repository
`replace` directives.

Adapters for a future SDK v4 generation can use a separate `sdkv4` module.
Existing users can continue using their selected generation. This does not
change the legacy `model/openai` package or its dependencies.
