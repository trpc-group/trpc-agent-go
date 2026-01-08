# OCR Text Recognition

The Knowledge system supports OCR (Optical Character Recognition) functionality, enabling text extraction from images or scanned PDF documents, greatly expanding the range of data sources for knowledge bases.

The framework currently has built-in support for the **Tesseract** OCR engine.

## Environment Setup

### 1. Install Tesseract

Before using OCR functionality, you must install the Tesseract engine and its language packs on your system.

**Linux (Ubuntu/Debian)**:
```bash
sudo apt-get update
sudo apt-get install tesseract-ocr libtesseract-dev
# Install Chinese language pack (optional)
sudo apt-get install tesseract-ocr-chi-sim
```

**macOS**:
```bash
brew install tesseract
# Install language packs
brew install tesseract-lang
```

**Windows**:
Please download and install from [UB-Mannheim/tesseract](https://github.com/UB-Mannheim/tesseract/wiki).

### 2. Go Build Tag

Since the Tesseract binding uses CGO, to avoid introducing unnecessary dependencies for users who don't use OCR, the OCR functionality is placed under the `tesseract` build tag.

When running or compiling code that includes OCR functionality, you **must** add the `-tags tesseract` flag:

```bash
go run -tags tesseract main.go
# or
go build -tags tesseract .
```

It's also recommended to add build constraints at the beginning of your code files:

```go
//go:build tesseract
// +build tesseract
```

## Quick Start

> **Complete Example**: [examples/knowledge/features/OCR](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/OCR)

### Basic Usage

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/ocr/tesseract"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    
    // Import PDF reader to support PDF file parsing
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

func main() {
    // 1. Create Tesseract OCR engine
    ocrExtractor, err := tesseract.New(
        tesseract.WithLanguage("eng+chi_sim"), // Support English and Simplified Chinese
        tesseract.WithConfidenceThreshold(60.0), // Set confidence threshold
    )
    if err != nil {
        panic(err)
    }

    // 2. Create OCR-enabled knowledge source
    // OCR extractor can be injected into File Source or Directory Source
    pdfSource := dirsource.New(
        []string{"./data/pdfs"},
        dirsource.WithOCRExtractor(ocrExtractor), // Enable OCR
        dirsource.WithName("Scanned Documents"),
    )

    // 3. Create Knowledge
    kb := knowledge.New(
        knowledge.WithSources([]source.Source{pdfSource}),
        // ... other configurations (Embedder, VectorStore)
    )
    
    // ... load and use
}
```

## Configuration Options

### Tesseract Configuration

`tesseract.New` supports the following configuration options:

| Option | Description | Default |
|--------|-------------|---------|
| `WithLanguage(lang)` | Set recognition language(s), use `+` to combine multiple languages (e.g., `eng+chi_sim`) | `"eng"` |
| `WithConfidenceThreshold(score)` | Set minimum confidence threshold (0-100), results below this threshold will be rejected | `60.0` |
| `WithPageSegMode(mode)` | Set page segmentation mode (PSM 0-13), corresponds to Tesseract's `--psm` parameter | `3` (fully automatic) |

### Source Integration

OCR extractors can be integrated into the following Sources via the `WithOCRExtractor` option:

- **File Source**: `filesource.WithOCRExtractor(ocr)`
- **Directory Source**: `dirsource.WithOCRExtractor(ocr)`
- **Auto Source**: `autosource.WithOCRExtractor(ocr)`

When an OCR extractor is configured, the Source will attempt to perform OCR processing on images or pages when handling supported file types (such as PDF).

## Notes

1. **Performance Impact**: OCR processing is computationally intensive and will significantly increase document loading time. It's recommended to enable this feature only for document sources that require OCR.
2. **CGO Dependency**: Using OCR functionality will cause the compiled binary to depend on system libraries (`libtesseract`). Ensure the deployment environment has the required dependencies installed.
3. **PDF Support**: To process PDF files, make sure to import the `knowledge/document/reader/pdf` package. This Reader will automatically detect image content in PDFs and invoke the OCR engine.
