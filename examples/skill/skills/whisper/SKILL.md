---
name: whisper
description: Transcribe audio files to text using OpenAI Whisper
---

# Whisper Audio Transcription Skill

Transcribe audio files to text using OpenAI Whisper.

## Capabilities

- Transcribe audio files (MP3, WAV, M4A, FLAC, OGG, etc.) to text
- Support for 90+ languages with auto-detection
- Optional timestamp generation
- Multiple model sizes (tiny/base/small/medium/large)
- Output in plain text or JSON format

## Usage

### Basic Transcription

```bash
python3 scripts/transcribe.py <audio_file> <output_file>
```

### With Options

```bash
# Specify model size (default: base)
python3 scripts/transcribe.py audio.mp3 transcript.txt --model medium

# Specify language (improves accuracy)
python3 scripts/transcribe.py audio.mp3 transcript.txt --language zh

# Include timestamps
python3 scripts/transcribe.py audio.mp3 transcript.txt --timestamps

# JSON output with metadata
python3 scripts/transcribe.py audio.mp3 output.json --format json
```

## Parameters

- `audio_file` (required): Path to input audio file
- `output_file` (required): Path to output text/JSON file
- `--model`: Whisper model size (tiny/base/small/medium/large, default: base)
- `--language`: Language code (e.g., en, zh, es, fr, auto for detection)
- `--timestamps`: Include word-level timestamps in output
- `--format`: Output format (text/json, default: text)

## Model Sizes

| Model  | Parameters | Speed | Accuracy | Memory |
|--------|------------|-------|----------|--------|
| tiny   | 39M        | ~32x  | Good     | ~1GB   |
| base   | 74M        | ~16x  | Better   | ~1GB   |
| small  | 244M       | ~6x   | Great    | ~2GB   |
| medium | 769M       | ~2x   | Excellent| ~5GB   |
| large  | 1.5B       | 1x    | Best     | ~10GB  |

## Supported Audio Formats

MP3, WAV, M4A, FLAC, OGG, AAC, WMA, and more (via FFmpeg)

## Dependencies

- Python 3.8+
- openai-whisper
- ffmpeg

## Installation

```bash
pip install openai-whisper
sudo apt-get install ffmpeg  # Ubuntu/Debian
```
