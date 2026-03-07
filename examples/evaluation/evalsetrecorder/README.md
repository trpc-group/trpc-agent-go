# EvalSet Recorder Example

This example demonstrates how to attach the `evaluation/evalset/recorder` runner plugin and persist live runner traffic into a reusable EvalSet on local disk. It uses a real `llmagent` (a calculator agent with a function tool), similar to other evaluation examples. By default, the recorder stores `sessionID` as both `EvalSetID` and `EvalCaseID`, snapshots `RunOptions.RuntimeState` into `SessionInput.State`, and records injected context messages at EvalCase scope for replay.

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work.

## Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-model` | Model identifier used by the calculator agent | `gpt-5.2` |
| `-streaming` | Enable streaming responses from the LLM | `false` |
| `-evalset-dir` | Directory where EvalSet files are stored | `./output` |
| `-user` | User ID used by Runner | `user-1` |
| `-session` | Session ID used as EvalSetID/EvalCaseID by default | `session-1` |

## Run

```bash
cd trpc-agent-go/examples/evaluation/evalsetrecorder
go run . \
  -model "gpt-5.2" \
  -evalset-dir "./output" \
  -session "session-1"
```

## Output

By default, the recorder uses `sessionID` as both `EvalSetID` and `EvalCaseID`, so the file is written to:

```text
./output/<appName>/<sessionID>.evalset.json
```

If you enable `recorder.WithTraceModeEnabled(true)`, the recorder creates `EvalModeTrace` cases and appends turns to `ActualConversation` instead of `Conversation`.
