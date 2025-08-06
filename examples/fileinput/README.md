# File Input Example

This example demonstrates how to process various types of file inputs (text, images, audio, and files) using OpenAI-compatible models.

## Features

- **Text Input**: Process plain text messages
- **Image Input**: Analyze images (JPEG, PNG, GIF, WebP)
- **Audio Input**: Process audio files (WAV format)
- **File Upload**: Upload and analyze any file type
- **Streaming Support**: Real-time streaming responses
- **Model Variants**: Support for OpenAI and Hunyuan models

## Quick Start

```bash
# Set your API key
export OPENAI_API_KEY="your-api-key-here"

# Basic text processing
go run main.go -text "Hello, how are you?"

# Image analysis
go run main.go -image path/to/image.jpg

# File analysis
go run main.go -file path/to/document.pdf -text "Analyze this file"

# Multiple inputs
go run main.go -text "What's in this image?" -image path/to/image.png
```

## Command Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-model` | Model to use | `gpt-4o` |
| `-variant` | Model variant (`openai`, `hunyuan`) | `openai` |
| `-text` | Text input to process | - |
| `-image` | Path to image file | - |
| `-audio` | Path to audio file | - |
| `-file` | Path to file for analysis | - |
| `-streaming` | Enable streaming mode | `true` |
| `-file-ids` | Use file IDs instead of base64 | `true` |

## File Handling

The example supports two file handling modes:

### File IDs Mode (Default)
Files are uploaded to the model provider and referenced by ID. This is the recommended approach for most use cases.

```bash
# Use file IDs (default)
go run main.go -file document.pdf -text "Analyze this PDF"
```

### File Data Mode
File content is embedded directly in the message as base64 data.

```bash
# Use file data
go run main.go -file document.pdf -file-ids=false -text "Analyze this PDF"
```

## Model Variants

### OpenAI Variant
Standard OpenAI-compatible behavior with full file support.

```bash
go run main.go -variant openai -file data.json -text "Analyze this JSON"
```

### Hunyuan Variant
Optimized for Hunyuan models with specific file handling.

```bash
go run main.go -variant hunyuan -file data.json -text "Analyze this JSON"
```

## Examples

### Text Analysis
```bash
go run main.go -text "Explain quantum computing in simple terms"
```

### Image Analysis
```bash
go run main.go -image photo.jpg -text "What objects do you see in this image?"
```

### Audio Processing
```bash
go run main.go -audio recording.wav -text "Transcribe this audio"
```

### Document Analysis
```bash
go run main.go -file report.pdf -text "Summarize the key points in this document"
```

### Custom Model
```bash
go run main.go -model gpt-4 -file code.py -text "Review this Python code"
```

## Supported File Types

### Images
- JPEG (.jpg, .jpeg)
- PNG (.png)
- GIF (.gif)
- WebP (.webp)

### Audio
- WAV (.wav)

### Documents
- Any file type (PDF, DOC, TXT, JSON, etc.)

## Output Example

```
🚀 File Input Processing
Model: gpt-4o
Variant: openai
Streaming: true
File Mode: file_ids (recommended for Hunyuan/Gemini)
==================================================
✅ File processor ready!

📄 File input: document.pdf (mode: file_ids)
📤 File uploaded with ID: file-abc123def456
🤖 Assistant: I can see the contents of document.pdf. Here's what I found...
```

## Configuration

### API Key
Set your API key as an environment variable:
```bash
export OPENAI_API_KEY="your-api-key-here"
```

### Streaming
Toggle streaming mode for real-time responses:
```bash
# Enable streaming (default)
go run main.go -streaming=true -text "Hello"

# Disable streaming
go run main.go -streaming=false -text "Hello"
```

## Error Handling

The example includes comprehensive error handling for:
- Invalid file paths
- Unsupported file formats
- API communication errors
- Missing API keys

## Dependencies

- `trpc-agent-go`: Core framework
- `openai`: Model provider
- Standard library: `context`, `flag`, `fmt`, `log`, `strings`
