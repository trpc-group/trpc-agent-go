# File Input Example

This example demonstrates how to process various types of file inputs (text, images, audio, and files) using the OpenAI model directly from trpc-agent-go.

## Features

- **Text Input**: Process plain text messages
- **Image Input**: Analyze images (JPEG, PNG, GIF, WebP)
- **Audio Input**: Process audio files (WAV format)
- **File Upload**: Upload and analyze any file type
- **Streaming Support**: Real-time streaming responses
- **Direct Model Access**: Direct interaction with OpenAI models

## Usage

```bash
# Set your OpenAI API key
export OPENAI_API_KEY="your-api-key-here"

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

This example uses **direct model interaction** which provides:

1. **Direct API Access**: Direct communication with OpenAI models
2. **File Handling**: Built-in support for various file types
3. **Streaming Support**: Real-time streaming of responses
4. **Error Handling**: Comprehensive error handling and reporting
5. **Simple Interface**: Straightforward model interaction

### Key Components

- `fileProcessor`: Main struct managing the file processing workflow
- `openai.Model`: Direct OpenAI model interface
- `model.Message`: Message structure with file attachment support
- `model.Request`: Request structure for model communication

## Example Output

```
🚀 File Input Processing with OpenAI Model
Model: gpt-4o
Streaming: true
==================================================
✅ File processor ready!

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

## File Processing Methods

The example uses the following methods for file processing:

- `AddImageFilePath()`: Add images from file paths
- `AddAudioFilePath()`: Add audio files from paths
- `AddFilePath()`: Add any file type from paths
- `AddImageData()`: Add raw image data
- `AddAudioData()`: Add raw audio data
- `AddFileData()`: Add raw file data

## Error Handling

The example includes comprehensive error handling for:
- Invalid file paths
- Unsupported file formats
- File reading errors
- API communication errors
- Model configuration issues
- Missing API keys

## Dependencies

- `trpc-agent-go`: Core framework
- `openai`: Model provider
- Standard library: `context`, `flag`, `fmt`, `log`, `os`, `strings`

## API Key Configuration

You can provide your OpenAI API key in the following ways:

**Environment Variable**:
   ```bash
   export OPENAI_API_KEY="your-api-key-here"
   ```


## Streaming vs Non-Streaming

The example supports both streaming and non-streaming modes:

- **Streaming** (default): Real-time response streaming
- **Non-streaming**: Complete response at once

Toggle with the `-streaming` flag:
```bash
go run main.go -streaming=false -text "Hello"
``` 
