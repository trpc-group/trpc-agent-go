# Structured Output + Skills (Typed JSON)

> This demo is built on top of an older skill execution surface. For
> the recommended way to wire Agent Skills in new code, see
> [docs/mkdocs/en/skill.md](../../docs/mkdocs/en/skill.md). The
> structured-output pattern shown here is orthogonal and also works on
> top of the recommended setup.

This example demonstrates:

- Using Agent Skills to load knowledge and run scripts
- Disabling automatic execution of fenced code blocks in assistant text
  (`llmagent.WithEnableCodeExecutionResponseProcessor(false)`)
- Returning a **typed** final answer via `WithStructuredOutputJSON`

The key idea is:

- The agent may call tools first (Skills are tools).
- Only the **final** answer must be a single JSON object that matches the
  schema.

## Provider compatibility

This pattern requires the model service to support tools together with native
structured output. Some OpenAI-compatible endpoints accept both request fields
but let the JSON constraint suppress tool calls. A schema-valid final response
does not by itself prove that a tool ran; check the tool-call and tool-result
events shown by this example.

If your endpoint does not reliably support the combination, use separate model
calls: collect evidence with tools and without native structured output, then
disable tools and request the structured final response. Continue the same
session or message history for the second call, or explicitly include the first
call's tool-call and tool-result messages, so the final response receives the
collected evidence. See
[vLLM #39929](https://github.com/vllm-project/vllm/issues/39929) and
[SGLang #21593](https://github.com/sgl-project/sglang/pull/21593) for related
backend behavior.

## Run

From this directory:

```bash
go run . -model deepseek-v4-flash
```

Then ask anything (for example: "run the hello skill").

With a compatible provider, you should see:

- tool calls (`skill_load`, then `skill_run`)
- a final JSON response
- a typed `event.StructuredOutput` payload printed by the example

If a required tool call does not appear, do not treat a schema-valid final
response as evidence that the tool ran. Use the separate-call fallback above.

## What the skill does

The demo skill lives under `skills/hello/`. It runs:

```bash
bash scripts/hello.sh
```

and prints a short, deterministic message.
