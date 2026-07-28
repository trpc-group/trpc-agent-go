# Tool Result Formatting Example

This example formats a bash tool result as the XML-like observation used by
coding agents while keeping the framework's default JSON path for comparison.

The example uses a scripted model, so it runs without an API key. The two tools
call the same Go function with the same arguments and return the same
`commandResult`; only their result formatting differs:

- `bash_xml_like` uses `function.WithResultFormatter` and a typed
  `resultformat.FormatterFunc[commandResult]` to produce `<returncode>` and
  `<output>` blocks.
- `bash_default_json` configures no formatter and keeps the default JSON.

The sample command returns multiline `rg --json` output. JSON must escape the
embedded quotes, backslashes, and line breaks, while the XML-like observation
can keep that output as-is. The program prints the model-visible content and a
model-agnostic estimate from `model.NewSimpleTokenCounter`; exact token counts
depend on the model and tokenizer. For this sample, the simple counter estimates
190 tokens for the XML-like content and 209 for the default JSON content.

## Run

```bash
cd examples
go run ./toolresultformat
```

## Expected Output

```text
bash_xml_like (estimated content tokens: 190):
<returncode>0</returncode>
<output>
$ rg --json 'ResultFormatter' tool internal
{"type":"match","data":{"path":{"text":"tool/resultformat/formatter.go"},...}}
...</output>

bash_default_json (estimated content tokens: 209):
{"returncode":0,"output":"$ rg --json 'ResultFormatter' tool internal\n{\"type\":\"match\",...}"}
```

## Key Configuration

```go
xmlLikeTool := function.NewFunctionTool(
    runBash,
    function.WithName("bash_xml_like"),
    function.WithResultFormatter(
        resultformat.FormatterFunc[commandResult](formatObservation),
    ),
)
```

The formatter receives the final result and changes only the default tool
message content. The framework still manages the message role, tool name, tool
call ID, ordering, events, and model request history.

`formatObservation` is ordinary Go code and can follow an agent-specific
message format, including its escaping and output-truncation rules. Without a
formatter, the existing JSON behavior remains unchanged.
