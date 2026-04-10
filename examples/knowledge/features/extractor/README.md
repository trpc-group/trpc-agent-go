# Docling Extractor Example

This example demonstrates how to use [`Docling`](../../../knowledge/extractor/docling/docling.go) to process PDFs and HTML content, and how to view the chunked output generated through [`URL Source`](../../../knowledge/source/url/url_source.go).

## Start Docling locally

You can start `Docling Serve` locally with Docker:

```bash
docker run -p 5001:5001 ghcr.io/docling-project/docling-serve
```

`Docling` is relatively lightweight to get started with for local development. In most cases, you can run it directly on your local machine for document conversion experiments without setting up a heavy external service stack.

## Run the example

From [`examples/knowledge`](../README.md), run:

```bash
go run ./features/extractor -endpoint http://127.0.0.1:5001 -output ./output
```

This example demonstrates:

- baseline PDF reading without extractor
- direct Docling extraction for PDF / HTML
- chunked output generation through [`urlsource.WithExtractor(...)`](../../../knowledge/source/url/options.go:114)

## Output

The example writes results into [`examples/knowledge/output`](../../output):

- direct extracted markdown output
- baseline reader output
- grouped chunk files for each source document
