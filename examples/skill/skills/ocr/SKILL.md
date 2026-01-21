---
name: ocr
description: Extract text from images using Tesseract OCR
---

# OCR Image Text Extraction Skill

Extract text from images using Tesseract OCR engine.

## Capabilities

- Extract text from image files (PNG, JPG, JPEG, GIF, BMP, TIFF)
- Support for 100+ languages
- Optional image preprocessing for better accuracy
- Output in plain text or JSON format with confidence scores

## Usage

### Basic OCR

```bash
python3 scripts/ocr.py <image_file> <output_file>
```

### With Options

```bash
# Specify language (default: eng)
python3 scripts/ocr.py image.png text.txt --lang eng

# Chinese text
python3 scripts/ocr.py image.png text.txt --lang chi_sim

# Multiple languages
python3 scripts/ocr.py image.png text.txt --lang eng+chi_sim

# With image preprocessing (improves accuracy)
python3 scripts/ocr.py image.png text.txt --preprocess

# JSON output with confidence scores
python3 scripts/ocr.py image.png output.json --format json
```

### Download and OCR from URL

```bash
# OCR from remote image
python3 scripts/ocr_url.py <image_url> <output_file>

# With options
python3 scripts/ocr_url.py https://example.com/image.jpg text.txt --lang eng --preprocess
```

## Parameters

- `image_file` / `image_url` (required): Path to local image or image URL
- `output_file` (required): Path to output text/JSON file
- `--lang`: Language code (e.g., eng, chi_sim, jpn, fra, deu). Default: eng
- `--preprocess`: Apply image preprocessing (grayscale, thresholding) for better accuracy
- `--format`: Output format (text/json, default: text)

## Common Languages

| Language | Code |
|----------|------|
| English | eng |
| Chinese (Simplified) | chi_sim |
| Chinese (Traditional) | chi_tra |
| Japanese | jpn |
| Korean | kor |
| French | fra |
| German | deu |
| Spanish | spa |
| Russian | rus |
| Arabic | ara |

## Supported Image Formats

PNG, JPG, JPEG, GIF, BMP, TIFF, WEBP

## Dependencies

- Python 3.8+
- pytesseract
- Pillow (PIL)
- tesseract-ocr (system package)

## Installation

```bash
# Python packages
pip install pytesseract Pillow

# Tesseract OCR engine
sudo apt-get install tesseract-ocr  # Ubuntu/Debian
sudo yum install tesseract           # CentOS/RHEL
brew install tesseract               # macOS
```
