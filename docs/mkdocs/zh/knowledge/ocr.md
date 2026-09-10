# OCR 文字识别

> Deprecated: 独立 OCR 接入方式已不再作为推荐路径。新代码优先使用
> `WithExtractor(...)` 接入带 OCR 能力的内容提取器，例如
> `knowledge/extractor/docling`。

Knowledge 早期通过 `knowledge/ocr` 和 `WithOCRExtractor(...)` 支持 OCR。
这条链路会把 OCR 引擎注入到 Reader 内部，目前主要用于 PDF Reader 处理 PDF
页面中的图片内容。

现在更推荐使用 Extractor 路径：

```text
原始文件 / URL 响应体 -> Extractor(OCR + 文档转换) -> markdown / text -> Reader -> Chunking -> Document
```

对于扫描 PDF、图片型 PDF、图片和复杂版面文档，推荐使用 Docling：

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

`docling.New()` 默认开启 OCR；只有需要显式关闭 OCR 时才需要传
`docling.WithOCR(false)`。

## 旧 OCR 路径

旧路径仍保留兼容，但相关 API 已标记为 Deprecated：

- `knowledge/ocr`
- `knowledge/ocr/tesseract`
- `filesource.WithOCRExtractor(...)`
- `dirsource.WithOCRExtractor(...)`
- `autosource.WithOCRExtractor(...)`
- `reader.WithOCRExtractor(...)`

旧路径需要 Tesseract、CGO 依赖和 `tesseract` build tag：

```bash
go run -tags tesseract main.go
go build -tags tesseract .
```

## 与 Extractor 同时配置

如果同一个 Source 同时配置 `WithExtractor(...)` 和 `WithOCRExtractor(...)`，
并且 Extractor 支持当前文件扩展名，则 Source 会优先走 Extractor 路径。
这时 Reader 内部的旧 OCR 路径不会被触发。

因此新代码不要同时配置两套能力。需要 OCR 时，直接选择带 OCR 能力的
Extractor。

## 相关文档

- [Extractor 内容提取](extractor.md) - 推荐的复杂文档和 OCR 处理路径
- [文档源](source.md) - Source 类型与配置方式
- [Knowledge 概述](index.md) - Knowledge 模块整体结构与能力
