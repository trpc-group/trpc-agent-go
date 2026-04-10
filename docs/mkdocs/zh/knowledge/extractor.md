# Extractor 内容提取

> **完整示例**: [examples/knowledge/features/extractor](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/extractor)

Extractor 用于在进入 Reader 和 Chunking 之前，先把复杂原始内容转换成更适合后续处理的中间格式。

典型处理链路如下：

```text
原始文件 / URL 响应体 -> Extractor -> markdown / text -> Reader -> Chunking -> Document
```

这类能力适合 PDF、HTML、Office 文档、图片等不适合直接按文本切块的输入。

## Extractor、Reader、OCR 的区别

- **Extractor**：把复杂格式转换为 `markdown` 或 `text`，本质上更偏“文档转换”；某些实现内部也可能带 OCR 能力
- **Reader**：读取 `markdown` / `text` / `json` / `csv` 等内容，并执行 chunking
- **OCR**：从图片或扫描页中识别文字，通常作为 PDF Reader 的内部增强能力

可以简单理解为：

- `Extractor` 解决“先把内容变成可读文本”
- `Reader` 解决“如何把文本组织成文档与分块”
- `OCR` 解决“图像里有没有文字可识别”

如果你的输入已经是 `.md`、`.txt`、`.json` 这类格式，通常不需要额外引入 Extractor，直接交给 Reader 即可。

如果输入属于复杂的静态网页、复杂格式的 PDF、论文、海报等版面结构较强的内容，则更推荐优先使用 `Extractor`，尤其是像 `Docling` 这类能够更好保留结构信息的实现。

## 当前接入方式

目前以下 Source 支持通过 `WithExtractor(...)` 接入内容提取器：

- `filesource.WithExtractor(...)`
- `dirsource.WithExtractor(...)`
- `urlsource.WithExtractor(...)`
- `autosource.WithExtractor(...)`

其中 `URL Source` 的典型链路是：

```text
URL -> 下载响应体 -> Extractor -> markdown -> Markdown Reader -> MarkdownChunking
```

这意味着当链接返回的是 `application/pdf`、`text/html` 等复杂内容时，可以先交给 Extractor，再交给对应 Reader 继续处理。

对于支持直接远程取源的 Extractor（例如 Docling），`URL Source` 还可以直接走服务端的 URL 转换接口，而不是先由本地下载响应体后再上传。

## Docling 提取器

当前仓库内置的内容提取器实现是 `Docling`：

- 包路径：`trpc.group/trpc-go/trpc-agent-go/knowledge/extractor/docling`
- 默认支持：`.pdf`、`.html`、`.docx`、`.pptx`、图片、`.csv` 等格式
- 默认输出：`markdown`

如果你想在本地快速体验，可以直接用 Docker 启动 `Docling Serve`：

```bash
docker run -p 5001:5001 ghcr.io/docling-project/docling-serve
```

`Docling` 的部署门槛相对较低，适合本地开发机直接跑起来做文档转换实验，不需要额外搭建复杂服务。

Docling 适合：

- PDF -> Markdown
- HTML 网页 -> Markdown
- Office 文档 -> Markdown / 文本

对于扫描 PDF、图片型 PDF、结构复杂的 HTML / Office 文档，**推荐优先使用 Docling 作为主处理路径**。

同时需要注意：`Docling` 本身已经内置 OCR 能力，因此在这类链路里通常**不需要再额外配置独立的 OCR 能力**。

## 使用示例

下面的示例展示了如何将 Docling 注入到 URL Source 中，对链接内容先提取再切分：

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/extractor/docling"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

    // 注册 markdown reader，用于处理 extractor 输出的 markdown 内容。
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

更完整的示例可参考 [examples/knowledge/features/extractor](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/extractor)。

## 什么时候用 Extractor

推荐使用 `Extractor` 的情况：

- 希望 PDF / HTML 保留更好的结构信息
- 原始格式不适合直接用现有 Reader 读取
- 需要统一将多种复杂格式转成 Markdown 后再进入同一套切分流程

不一定需要 `Extractor` 的情况：

- 输入本身已经是 `.md`、`.txt`、`.json` 等可直接读取格式
- 只需要 PDF 的纯文本抽取，不在意 Markdown 结构
- 只需要 OCR 图像识别，而不是完整文档格式转换

推荐优先使用 `OCR` 而不是 `Extractor` 的情况：

- 输入本身是单张图片或纯图片流
- 你只关心识别出的纯文本，不关心 Markdown 结构和版面保留
- 你希望将 OCR 能力作为某个 Reader 的内部增强，而不是引入完整文档转换服务

## 相关文档

- [文档源](source.md) - Source 类型与配置方式
- [OCR 图片文字识别](ocr.md) - 扫描件和图片文字识别
- [Knowledge 概述](index.md) - Knowledge 模块整体结构与能力
