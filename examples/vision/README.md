# Vision Tool Example

This example gives a text-only main model image understanding through a
separate multimodal model and the `analyze_image` tool.

The image is attached to the current user message. The main OpenAI-compatible
model is configured with `WithTextOnlyMessageContent(true)`, so image bytes are
not sent to that provider. When the main model calls `analyze_image`, the tool
reads the image from the current invocation and sends it to the separately
configured vision model.

## Configuration

The main and vision models can use different OpenAI-compatible providers:

| Variable | Description | Default |
|---|---|---|
| `MAIN_MODEL_NAME` | Text-only main model | `gpt-4.1-mini` |
| `MAIN_OPENAI_BASE_URL` | Main model endpoint | `OPENAI_BASE_URL` |
| `MAIN_OPENAI_API_KEY` | Main model API key | `OPENAI_API_KEY` |
| `MAIN_OPENAI_VARIANT` | Main adapter variant | `openai` |
| `VISION_MODEL_NAME` | Multimodal model | `gpt-4.1-mini` |
| `VISION_OPENAI_BASE_URL` | Vision model endpoint | `OPENAI_BASE_URL` |
| `VISION_OPENAI_API_KEY` | Vision model API key | `OPENAI_API_KEY` |
| `VISION_OPENAI_VARIANT` | Vision adapter variant | `openai` |

Supported adapter variants are `openai`, `hunyuan`, `deepseek`, `qwen`, and
`glm`.

## Run

From the `examples` directory:

```bash
export MAIN_MODEL_NAME="your-text-model"
export MAIN_OPENAI_BASE_URL="https://main-provider.example.com/v1"
export MAIN_OPENAI_API_KEY="your-main-api-key"
export MAIN_OPENAI_VARIANT="openai"

export VISION_MODEL_NAME="your-vision-model"
export VISION_OPENAI_BASE_URL="https://vision-provider.example.com/v1"
export VISION_OPENAI_API_KEY="your-vision-api-key"
export VISION_OPENAI_VARIANT="openai"

go run ./vision \
  -image /path/to/image.png \
  -prompt "Read the visible text and describe the layout."
```

The expected execution path is:

```text
local image -> current invocation -> text-only main model -> analyze_image
            -> vision model -> text tool result -> main model response
```

`image_urls` is intentionally omitted in this example. When the main model
does provide non-empty `image_urls`, the tool analyzes only those URLs instead
of images attached to the current message.
