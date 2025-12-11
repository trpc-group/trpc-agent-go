## imagetool example

This example shows how to use the `ToolResultMessages` callback to turn the result
of a normal function tool that returns raw PNG bytes into an OpenAI‑compatible
pair of messages:

- one `role: "tool"` JSON text message (default behavior), and  
- one `role: "user"` message whose `content_parts` includes an `image` part.

The tool itself is a regular function tool (not MCP), so you can copy this pattern
into any existing agent that already uses function tools.

### Files

- `main.go`: example entry point. Creates the LLM agent, registers the tool and
  `ToolResultMessages` callback, and runs a single interaction.
- `imageToolArgs` / `imageToolResult`: input and output structs for the tool.
  The result contains PNG bytes in memory.
- `generateDemoImage`: tool implementation that generates a small PNG image.
- `toolResultMessagesCallback`: the key callback that converts the PNG bytes
  into `ContentParts` for the model.

### How to run

From the repository root:

```bash
cd examples

# Configure your OpenAI‑compatible endpoint (example values)
export OPENAI_BASE_URL="your-openai-compatible-base-url"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="your-model-name"

go run ./callbacks/imagetool
```

The program sends a user message asking the model to:

1. call `demo_image_tool` to generate an image, and  
2. describe what is in the image.

If the model and proxy support image input, you should see a description of a small
PNG image in the final assistant response.

### ToolResultMessages usage tips

1. **The callback is optional**  
   - If you do not register `ToolResultMessages`, or the callback returns `nil`
     / an empty slice, the framework only sends the default `role: "tool"` JSON
     message and behaves exactly like previous versions.

2. **Once you register the callback, protocol correctness is your responsibility**  
   - The callback must return a `model.Message` or `[]model.Message`. The framework
     only type‑checks the value and wraps it into choices; it does not fix `Role`
     or `ToolID` for you.
   - If you omit the `role: "tool"` message or use a `ToolID` that does not match
     the `tool_call_id`, OpenAI‑compatible providers may return 4xx errors.

3. **Keep at least one `RoleTool` result message per tool_call**  
   - For OpenAI‑style function calling, most implementations expect that an
     `assistant(tool_calls)` message is followed by one or more `role: "tool"`
     messages with matching `tool_call_id`.
   - In this example we keep the framework‑supplied `DefaultToolMessage` as the
     tool result and append an extra `RoleUser` message that carries the image.

4. **Do not base64‑encode the image twice**  
   - The tool implementation should return real binary image data (PNG bytes in
     this example).
   - In `ToolResultMessages` you simply put that into `model.Image{Data: ..., Format: "png"}`.
   - The OpenAI adapter (`model/openai`) is responsible for turning that into
     the correct wire format (`image_url` / base64) for the HTTP API. You should
     not add another encoding layer in the callback.

5. **Treat each callback invocation as scoped to a single tool_call**  
   - `ToolResultMessagesInput.ToolCallID` identifies the current tool call.
     The callback should only handle that one result.
   - Do not mix content for multiple `tool_call_id`s in a single returned
     `[]model.Message`.

### Adapting this pattern to your own tools

1. Make your tool return a struct that includes the raw image bytes (or other
   multimodal payload), e.g. a `[]byte` field.  
2. Register a `ToolResultMessages` callback and branch on `in.ToolName` or the
   tool declaration to decide which tools need special handling.  
3. In the callback:
   - Use `in.DefaultToolMessage.(model.Message)` as the baseline tool result;
   - Cast `in.Result` to your tool result type;
   - Build one or more `model.Message` values, adding `RoleUser` +
     `ContentParts` (text + image, files, etc.) as needed;  
   - Return something like `[]model.Message{defaultToolMsg, extraUserMsgWithImage}`.

This lets you keep existing text‑only tool call behavior while layering image
support (including MCP tools that emit images) on top, in an OpenAI‑compatible way.
