# Tool Result Formatting Example

This example demonstrates how to format one Function Tool's final result as
custom model-visible text while another Function Tool keeps the default JSON
representation.

The example uses a scripted model, so it runs without an API key. Both tools
return the same Go result type:

- `run_formatted` uses `function.WithResultFormatter` and a typed
  `resultformat.FormatterFunc[commandResult]` to produce an XML observation.
- `run_default` configures no formatter and keeps the framework's default JSON.

## Run

```bash
cd examples
go run ./resultformat
```

## Expected Output

```text
run_formatted:
<observation><exit_code>0</exit_code><output>ran status: &lt;ok&gt; &amp; &#34;done&#34;</output></observation>

run_default:
{"exit_code":0,"output":"ran status: <ok> & \"done\""}
```

## Key Configuration

```go
formattedTool := function.NewFunctionTool(
    runCommand,
    function.WithName("run_formatted"),
    function.WithResultFormatter(
        resultformat.FormatterFunc[commandResult](formatObservation),
    ),
)
```

The formatter receives the final normal result and changes only the default
tool message content. tRPC-Agent-Go still manages the tool message role, tool
name, tool call ID, ordering, events, and model request history.

The formatter is application code. This example uses Go's `encoding/xml`
package so escaping and output validity are explicit. Without a formatter, the
existing JSON behavior remains unchanged.
