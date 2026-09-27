# E2B envd Process protocol

The protocol and generated Go files in this directory are pinned to
[`e2b-dev/infra@01da054a`](https://github.com/e2b-dev/infra/commit/01da054ac9ed73de4b2d803bfa45e02d955ab4c9):

- `packages/envd/spec/process/process.proto`
- `packages/envd/internal/services/spec/process/process.pb.go`
- `packages/envd/internal/services/spec/process/processconnect/process.connect.go`

The generated files retain their generated-code markers. The repository
license header is prepended to both Go files, and the Connect client's process
import is rewritten to this local internal package. The protobuf output is
also mechanically normalized from `interface{}` to `any` to satisfy the root
repository formatting check. Upgrade the proto and both generated files
together.

The pinned envd `Handler.Wait` populates `EndEvent` from Go's `os.ProcessState`.
For signal termination, `exited` is false and `exit_code` is -1; `status` carries
`signal: <name>` (optionally followed by ` (core dumped)`). A nonempty `error`
also accompanies ordinary nonzero exits and signals, so it does not by itself
indicate a protocol or execution-service failure.

The client maps recognized Linux amd64/arm64 signal statuses to the shell
convention `128 + signal`, preserving the workspace API's exit-status behavior.
It uses the remote Linux signal table regardless of the client's operating
system. Unknown statuses and inconsistent signal exit codes remain errors.
