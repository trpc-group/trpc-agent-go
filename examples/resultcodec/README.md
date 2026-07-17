# Result Codec Example

This example shows how to control the format a tool's result is presented to the
model, per tool, using the `tool/resultcodec` package. The default stays JSON; a
tool can opt into XML, plain text, or a custom template without writing callbacks
or constructing `model.Message`.

It needs no API key: it prints codec output directly and shows the per-tool
wiring. In a real agent, the framework applies the bound codec to the tool's
final result when it builds the model-visible tool result message.

## Run

```bash
cd examples
go run ./resultcodec
```

## What it shows

- The built-in codecs `resultcodec.JSON()`, `XML()`, `Text()`, and `Custom[T]()`
  encoding the same result into different model-visible text.
- Binding a codec per tool:
  - `function.WithResultCodec(codec)` on a function tool.
  - `resultcodec.Wrap(tool, codec)` for a tool whose construction you cannot
    modify (for example a tool produced by a `ToolSet`).
- Leaving a tool on the default JSON behavior by configuring no codec.

## Expected output

```text
== Built-in codecs (same result, different model-visible formats) ==
JSON   -> {"exit_code":0,"output":"<ok> & \"done\""}
XML    -> <result><exit_code>0</exit_code><output>&lt;ok&gt; &amp; &#34;done&#34;</output></result>
Text   -> plain observation text
Custom -> exit=0
<ok> & "done"

== Per-tool configuration ==
function tool "bash_xml" -> XML codec
function tool "bash_custom" -> Custom codec
function tool "bash_json" -> default JSON (no codec)
wrapped tool "bash_wrapped" -> XML codec via resultcodec.Wrap
...
```

## How it works

A `resultcodec.Codec` only turns the final tool result into the model-visible
string. The framework still builds the protocol-correct tool result message
(`RoleTool`, tool name, and tool-call-ID pairing) and keeps event, session, and
resume semantics unchanged.

Notes:

- Without a codec, the model-visible JSON is byte-identical to previous versions.
- Encoding failures are reported as clear errors; the framework does not fall
  back to JSON or re-run the tool.
- Built-in codecs are deterministic and always emit valid UTF-8.
- For a streamable tool, only the final result is encoded; intermediate stream
  events are unchanged.
- Permission results (denied / approval-required) are framework control protocol
  and are never passed through the codec.
