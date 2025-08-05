# File Input Example

This example demonstrates how to process various types of file inputs (text, images, audio, and files) using the OpenAI model directly from trpc-agent-go. It supports both file data (base64) and file_ids approaches for file handling.

## Features

- **Text Input**: Process plain text messages
- **Image Input**: Analyze images (JPEG, PNG, GIF, WebP)
- **Audio Input**: Process audio files (WAV format)
- **File Upload**: Upload and analyze any file type using two modes:
  - **File Data (base64)**: Direct file content embedding (default for OpenAI)
  - **File IDs**: Upload files to OpenAI and reference by ID (recommended for Hunyuan/Gemini)
- **Streaming Support**: Real-time streaming responses
- **Direct Model Access**: Direct interaction with OpenAI models
- **OpenAI File API Integration**: Uses OpenAI's file upload API for file_ids mode

## Usage

### Basic Usage

```bash
# Set your OpenAI API key
export OPENAI_API_KEY="your-api-key-here"

# Text input only
go run main.go -text "Hello, how are you?"

# Image analysis
go run main.go -image path/to/image.jpg

# Audio processing
go run main.go -audio path/to/audio.wav

# File upload and analysis (using file_ids by default)
go run main.go -file path/to/document.pdf

# Multiple inputs
go run main.go -text "Analyze this image" -image path/to/image.png
```

### File Handling Modes

#### File IDs Mode (Default - Recommended for Hunyuan/Gemini)

```bash
# Use file_ids mode (default)
go run main.go -file path/to/document.pdf

# Explicitly enable file_ids mode
go run main.go -file path/to/document.pdf -file-ids=true
```

#### File Data Mode (Base64 - For OpenAI)

```bash
# Use file data (base64) mode
go run main.go -file path/to/document.pdf -file-ids=false
```

### Model Variants

#### OpenAI Variant (Default)

```bash
# Use OpenAI variant (default behavior)
go run main.go -variant openai -file test.json -text "Analyze this JSON file"

# Or simply omit the variant flag
go run main.go -file test.json -text "Analyze this JSON file"
```

#### Hunyuan Variant

```bash
# Use Hunyuan variant with specific file handling
go run main.go -variant hunyuan -file test.json -text "Analyze this JSON file"
```

### Advanced Options

```bash
# Custom model and streaming options
go run main.go -model gpt-4 -text "Hello" -streaming=false

# Custom model with file processing
go run main.go -model gpt-4o -file test.json -text "Analyze this JSON file"

# Hunyuan variant with custom model
go run main.go -variant hunyuan -model hunyuan-pro -file test.json -text "Analyze this JSON file"
```

## Command Line Flags

- `-model`: Model to use (default: "gpt-4o")
- `-variant`: Model variant (openai, hunyuan) (default: "openai")
- `-text`: Text input to process
- `-image`: Path to image file (supports: jpg, jpeg, png, gif, webp)
- `-audio`: Path to audio file (supports: wav)
- `-file`: Path to any file for upload and analysis
- `-streaming`: Enable/disable streaming mode (default: true)
- `-file-ids`: Use file_ids instead of file data (base64) (default: true)

## Model Variants Explained

### OpenAI Variant (Default)

The OpenAI variant provides standard OpenAI-compatible behavior:

- **File Upload Path**: `/openapi/v1/files`
- **File Purpose**: `user_data`
- **Delete Method**: `DELETE`
- **File Type Handling**: Includes file type in content parts

### Hunyuan Variant

The Hunyuan variant provides specific behavior for Hunyuan models:

- **File Upload Path**: `/openapi/v1/files/uploads`
- **File Purpose**: `file-extract`
- **Delete Method**: `POST` with JSON body `{"file_id":"..."}`
- **File Type Handling**: Skips file type in content parts, uses file_ids only

## File Handling Modes Explained

### File IDs Mode (Recommended for Hunyuan/Gemini)

**How it works:**
1. File is uploaded to OpenAI via their file upload API
2. OpenAI returns a unique file ID
3. File ID is included in the message sent to the model
4. Model retrieves file content using the file ID

**Advantages:**
- Compatible with models that only support file_ids (Hunyuan, Gemini)
- Better performance for large files
- Reduced message size
- Server-side file management by OpenAI
- Reusable file references

**Usage:**
```bash
go run main.go -file test.json -text "Analyze this JSON file"
```

### File Data Mode (Base64)

**How it works:**
1. File content is read and base64 encoded
2. Encoded data is embedded directly in the message
3. Model receives the file content inline

**Advantages:**
- Works with any OpenAI-compatible model
- No additional API calls required
- Self-contained messages

**Usage:**
```bash
go run main.go -file test.json -file-ids=false -text "Analyze this JSON file"
```

## Architecture

This example uses **direct model interaction** which provides:

1. **Direct API Access**: Direct communication with OpenAI models
2. **Dual File Handling**: Support for both file_ids and file data approaches
3. **OpenAI File API Integration**: File upload capabilities via OpenAI's file API
4. **Streaming Support**: Real-time streaming of responses
5. **Error Handling**: Comprehensive error handling and reporting
6. **Simple Interface**: Straightforward model interaction

### Key Components

- `fileProcessor`: Main struct managing the file processing workflow
- `openai.Model`: Direct OpenAI model interface with file upload methods
- `model.Message`: Message structure with file attachment support
- `model.Request`: Request structure for model communication

## OpenAI File API Integration

The example uses OpenAI's file upload API for file_ids mode:

### File Upload Methods

The `openai.Model` provides several file management methods:

- `UploadFile(ctx, filePath)`: Upload a file from path and get file ID
- `UploadFileData(ctx, filename, data)`: Upload file data and get file ID
- `DeleteFile(ctx, fileID)`: Delete a file from OpenAI
- `GetFile(ctx, fileID)`: Get file information from OpenAI

### File Upload Process

1. **File Upload**: File is uploaded to OpenAI using their `/files` endpoint
2. **File ID Retrieval**: OpenAI returns a unique file ID
3. **Message Creation**: File ID is added to the message using `AddFileID()`
4. **Model Processing**: Model processes the message with file reference

## Example Output

### OpenAI Variant
```
🚀 File Input Processing with OpenAI Model
Model: gpt-4o
Variant: openai
Streaming: true
File Mode: file_ids (recommended for Hunyuan/Gemini)
==================================================
✅ File processor ready!

📄 File input: test.json (mode: file_ids)
📤 File uploaded with ID: file-abc123def456
🤖 Assistant: I can see the contents of test.json. Here's what I found...
```

### Hunyuan Variant
```
🚀 File Input Processing with OpenAI Model
Model: hunyuan-pro
Variant: hunyuan
Streaming: true
File Mode: file_ids (recommended for Hunyuan/Gemini)
==================================================
✅ File processor ready!

📄 File input: test.json (mode: file_ids)
📤 File uploaded with ID: file-abc123def456 (variant: hunyuan)
🤖 Assistant: I can see the contents of test.json. Here's what I found...
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
- Any file type (uploaded as base64 or via OpenAI file API)

## File Processing Methods

The example uses the following methods for file processing:

- `AddImageFilePath()`: Add images from file paths
- `AddAudioFilePath()`: Add audio files from paths
- `AddFilePath()`: Add any file type from paths (file data mode)
- `AddFileID()`: Add file by ID (file_ids mode)
- `AddImageData()`: Add raw image data
- `AddAudioData()`: Add raw audio data
- `AddFileData()`: Add raw file data

## Error Handling

The example includes comprehensive error handling for:
- Invalid file paths
- Unsupported file formats
- File reading errors
- OpenAI API upload failures
- API communication errors
- Model configuration issues
- Missing API keys

## Dependencies

- `trpc-agent-go`: Core framework
- `openai`: Model provider with file upload support
- Standard library: `context`, `flag`, `fmt`, `log`, `strings`

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
