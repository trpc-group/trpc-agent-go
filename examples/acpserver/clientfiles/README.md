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

The client creates a temporary session workspace with `input.txt`, passes that
absolute path as `session/new.cwd`, and asks the agent to copy the file with
`acp_read_text_file` and `acp_write_text_file`. The write tool requests
permission first; the example client approves it once and verifies the resulting
`copied.txt`.

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
write flow, and removes the temporary workspace after verification.

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
| `-workspace` | Temporary directory | Workspace to use when it should be inspected after the run. |

## Expected Output

The client prints streamed assistant text, the two ACP file tool calls, and a
permission request. A successful run ends with:

```text
[client] verified copied.txt
```

The server keeps relative paths inside the session workspace. The client also
checks the path before performing real filesystem operations.
