# File Input Example

This example demonstrates how to process various types of file inputs (text, images, audio, and files) using the Runner pattern from trpc-agent-go.

## Features

- **Text Input**: Process plain text messages
- **Image Input**: Analyze images (JPEG, PNG, GIF, WebP)
- **Audio Input**: Process audio files (WAV format)
- **File Upload**: Upload and analyze any file type
- **Streaming Support**: Real-time streaming responses
- **Session Management**: Built-in session handling via Runner

## Usage

```bash
# Text input only
go run main.go -text "Hello, how are you?"

# Image analysis
go run main.go -image path/to/image.jpg

# Audio processing
go run main.go -audio path/to/audio.wav

# File upload and analysis
go run main.go -file path/to/document.pdf

# Multiple inputs
go run main.go -text "Analyze this image" -image path/to/image.png

# Custom model and streaming options
go run main.go -model gpt-4 -text "Hello" -streaming=false
```

## Command Line Flags

- `-model`: Model to use (default: "gpt-4o")
- `-text`: Text input to process
- `-image`: Path to image file (supports: jpg, jpeg, png, gif, webp)
- `-audio`: Path to audio file (supports: wav)
- `-file`: Path to any file for upload and analysis
- `-streaming`: Enable/disable streaming mode (default: true)

## Architecture

This example uses the **Runner pattern** which provides:

1. **Session Management**: Automatic session creation and management
2. **Event Handling**: Structured event processing for responses
3. **Error Handling**: Comprehensive error handling and reporting
4. **Streaming Support**: Real-time streaming of responses
5. **Clean Interface**: Simplified agent execution

### Key Components

- `fileProcessor`: Main struct managing the file processing workflow
- `runner.Runner`: Handles agent execution and session management
- `llmagent`: LLM-based agent with file processing capabilities
- `session.Service`: Session management for conversation history

## Example Output

```
🚀 File Input Processing with Runner
Model: gpt-4o
Streaming: true
==================================================
✅ File processor ready! Session: file-session-1753850114

📝 Text input: Hello, this is a test message
🤖 Assistant: Hello! I'm here to help you with any questions or tasks you might have. How can I assist you today?
```

## Supported File Types

### Images
- JPEG (.jpg, .jpeg)
- PNG (.png)
- GIF (.gif)
- WebP (.webp)

### Audio
- WAV (.wav)

### Files
- Any file type (uploaded as base64)

## Error Handling

The example includes comprehensive error handling for:
- Invalid file paths
- Unsupported file formats
- File reading errors
- API communication errors
- Model configuration issues

## Dependencies

- `trpc-agent-go`: Core framework
- `openai`: Model provider
- `session/inmemory`: Session management
- `runner`: Agent execution framework 
