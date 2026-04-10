# Extractor Content Extraction

> **Full example**: [examples/knowledge/features/extractor](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/extractor)

Extractors are used before Reader and Chunking to convert complex raw inputs into an intermediate format that is easier to process downstream.

The typical pipeline looks like this:

```text
raw file / URL response body -> Extractor -> markdown / text -> Reader -> Chunking -> Document
```

This is useful for inputs such as PDFs, HTML, Office documents, and images that are not ideal for direct text chunking.

## Difference between Extractor, Reader, and OCR

- **Extractor**: converts complex formats into `markdown` or `text`; conceptually it is document conversion, and some implementations may also include OCR internally
- **Reader**: reads `markdown`, `text`, `json`, `csv`, and other supported formats, then performs chunking
- **OCR**: recognizes text from images or scanned pages, usually as an internal enhancement for PDF reading

You can think of them like this:

- `Extractor` solves “first turn complex content into readable text”
- `Reader` solves “how to organize text into documents and chunks”
- `OCR` solves “whether text can be recognized from image content”

If your input is already in formats such as `.md`, `.txt`, or `.json`, you usually do not need an extra Extractor and can pass it directly to a Reader.

If the input is a complex static web page, a complicated PDF, an academic paper, a poster, or other content with strong layout structure, it is generally better to use an `Extractor` first, especially an implementation such as `Docling` that preserves structure more effectively.

## Current integration points

The following Sources currently support content extraction via `WithExtractor(...)`:

- `filesource.WithExtractor(...)`
- `dirsource.WithExtractor(...)`
- `urlsource.WithExtractor(...)`
- `autosource.WithExtractor(...)`

For `URL Source`, the common pipeline is:

```text
URL -> download response body -> Extractor -> markdown -> Markdown Reader -> MarkdownChunking
```

This means that when a URL returns complex content such as `application/pdf` or `text/html`, you can first pass it through an Extractor and then continue processing it with the appropriate Reader.

For Extractors that support direct remote source fetching, such as Docling, `URL Source` can also call the service-side URL conversion API directly instead of downloading the response locally first.

## Docling Extractor

The built-in content extractor currently provided in this repository is `Docling`:

- package path: `trpc.group/trpc-go/trpc-agent-go/knowledge/extractor/docling`
- default support: `.pdf`, `.html`, `.docx`, `.pptx`, images, `.csv`, and more
- default output: `markdown`

If you want to try it locally, you can start `Docling Serve` with Docker:

```bash
docker run -p 5001:5001 ghcr.io/docling-project/docling-serve
```

`Docling` has a relatively low barrier to entry for local usage and is practical to run directly on a development machine for document conversion experiments.

Docling is a good fit for:

- PDF -> Markdown
- HTML pages -> Markdown
- Office documents -> Markdown / text

For scanned PDFs, image-based PDFs, and structurally complex HTML / Office documents, **Docling should generally be the preferred main processing path**.

Also note that `Docling` already includes OCR capability internally, so in this kind of pipeline you usually **do not need to configure a separate OCR capability in addition to it**.

## Example

The example below shows how to inject Docling into URL Source so that linked content is extracted before chunking:

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/extractor/docling"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

    // Register the markdown reader for extractor output.
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
)

func main() {
    ctx := context.Background()

    ext := docling.New(
        docling.WithEndpoint("http://localhost:5001"),
    )
    defer ext.Close()

    src := urlsource.New(
        []string{
            "https://arxiv.org/pdf/1706.03762",
            "https://www.rfc-editor.org/rfc/rfc9110.html",
        },
        urlsource.WithExtractor(ext),
        urlsource.WithChunkSize(500),
        urlsource.WithChunkOverlap(50),
    )

    docs, err := src.ReadDocuments(ctx)
    if err != nil {
        panic(err)
    }

    _ = docs
}
```

For a more complete example, see [examples/knowledge/features/extractor](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/extractor).

## When to use an Extractor

Recommended cases for using an `Extractor`:

- when you want to preserve better structure from PDF / HTML content
- when the original format is not suitable for direct reading by existing Readers
- when you want to normalize multiple complex formats into Markdown before entering the same chunking pipeline

Cases where you may not need an `Extractor`:

- the input is already `.md`, `.txt`, `.json`, or another directly readable format
- you only need plain text extraction from PDF and do not care about Markdown structure
- you only need OCR on image content rather than full document conversion

Cases where `OCR` may be a better fit than an `Extractor`:

- the input is a single image or a pure image stream
- you only care about recognized plain text, not Markdown structure or layout preservation
- you want OCR to be used as an internal enhancement for a Reader instead of introducing a full document conversion service

## Related documents

- [Source](source.md) - Source types and configuration
- [OCR](ocr.md) - OCR for scanned documents and images
- [Knowledge overview](index.md) - Overall Knowledge module architecture and capabilities
