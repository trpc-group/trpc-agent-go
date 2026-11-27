# ACP Client Files Example

This example shows the optional ACP v1 client filesystem tools. The ACP server
enables reads and writes explicitly, and the client advertises both capabilities
during `initialize`. The tools are available only when both sides enable the
corresponding feature.

The example has two processes:

```text
examples/acpserver/clientfiles/
├── client/  # ACP client; implements fs callbacks and permission handling
└── server/  # ACP agent server; enables client file tools
```

The client creates a fresh run directory with `input.txt`, passes that absolute
path as `session/new.cwd`, and asks the agent to copy the file with
`acp_read_text_file` and `acp_write_text_file`. The write tool requests
permission first; the example client approves it once and verifies the resulting
`copied.txt`. When no `-workspace` is supplied, the temporary run directory is
removed after verification.

## Prerequisites

Configure an OpenAI-compatible model:

```bash
export OPENAI_API_KEY="<your-api-key>"
export OPENAI_BASE_URL="<your-base-url>" # Optional for compatible endpoints.
```

## Run

From the `examples` module:

```bash
cd examples
go run ./acpserver/clientfiles/client
```

The client starts `go run ./server`, runs the complete read, permission, and
write flow, and removes the temporary run directory after verification.

The feature directory can also be used directly:

```bash
cd examples/acpserver/clientfiles
go run ./client
```

## Options

| Flag | Default | Description |
| --- | --- | --- |
| `-model` | `gpt-4o-mini` | Model passed to the ACP server. |
| `-prompt` | Built-in file-copy prompt | Prompt sent to the ACP agent. |
| `-workspace` | Temporary directory | Base directory for a new run directory when the result should be inspected after the run. |

## Expected Output

The client prints streamed assistant text, the two ACP file tool calls, and a
permission request. A successful run ends with:

```text
[client] verified copied.txt
```

The server rejects paths that are lexically outside the session workspace. It
cannot determine where client-side links resolve. The client must enforce
workspace containment when opening files; this example uses `os.OpenRoot` for
all reads, directory creation, and writes.

Client file reads stream line ranges incrementally and limit the response to
1 MiB so the escaped JSON stays below the ACP SDK frame limit.
