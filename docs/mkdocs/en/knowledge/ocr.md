# OCR Text Recognition

> Deprecated: the standalone OCR integration path is no longer recommended for
> new code. Prefer `WithExtractor(...)` with an OCR-capable content extractor,
> such as `knowledge/extractor/docling`.

The Knowledge package previously supported OCR through `knowledge/ocr` and
`WithOCRExtractor(...)`. That path injects an OCR engine into Readers and is
mainly used by the PDF Reader to process image content inside PDF pages.

The recommended path is now Extractor-based:

```text
raw file / URL response body -> Extractor(OCR + document conversion) -> markdown / text -> Reader -> Chunking -> Document
```

For scanned PDFs, image-based PDFs, images, and complex-layout documents, use
Docling:

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/extractor/docling"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"

    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
)

func main() {
    ctx := context.Background()

    ext := docling.New(
        docling.WithEndpoint("http://localhost:5001"),
        docling.WithOCR(true),
    )
    defer ext.Close()

    src := filesource.New(
        []string{"./data/scanned.pdf"},
        filesource.WithExtractor(ext),
    )

    docs, err := src.ReadDocuments(ctx)
    if err != nil {
        panic(err)
    }

    _ = docs
}
```

`docling.New()` enables OCR by default. Use `docling.WithOCR(false)` only when
you need to disable OCR explicitly.

## Legacy OCR Path

The old path remains for compatibility, but these APIs are now deprecated:

- `knowledge/ocr`
- `knowledge/ocr/tesseract`
- `filesource.WithOCRExtractor(...)`
- `dirsource.WithOCRExtractor(...)`
- `autosource.WithOCRExtractor(...)`
- `reader.WithOCRExtractor(...)`

The legacy path requires Tesseract, CGO dependencies, and the `tesseract` build
tag:

```bash
go run -tags tesseract main.go
go build -tags tesseract .
```

## Configuring OCR and Extractor Together

If the same Source is configured with both `WithExtractor(...)` and
`WithOCRExtractor(...)`, and the Extractor supports the current file extension,
the Source uses the Extractor path first. The Reader-level legacy OCR path is
not triggered in that case.

Do not configure both paths in new code. When OCR is needed, choose an
OCR-capable Extractor directly.

## Related Documents

- [Extractor content extraction](extractor.md) - Recommended path for complex documents and OCR
- [Source](source.md) - Source types and configuration
- [Knowledge overview](index.md) - Overall Knowledge module architecture and capabilities
