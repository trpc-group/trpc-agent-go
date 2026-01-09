# OCR 文字识别

Knowledge 系统支持 OCR (Optical Character Recognition) 功能，能够从图片或扫描版 PDF 文档中提取文本内容，极大地扩展了知识库的数据来源范围。

目前框架内置了 **Tesseract** OCR 引擎支持。

## 环境准备

### 1. 安装 Tesseract

在使用 OCR 功能之前，必须在系统上安装 Tesseract 引擎及其语言包。

**Linux (Ubuntu/Debian)**:
```bash
sudo apt-get update
sudo apt-get install tesseract-ocr libtesseract-dev
# 安装中文语言包（可选）
sudo apt-get install tesseract-ocr-chi-sim
```

**macOS**:
```bash
brew install tesseract
# 安装语言包
brew install tesseract-lang
```

**Windows**:
请从 [UB-Mannheim/tesseract](https://github.com/UB-Mannheim/tesseract/wiki) 下载并安装。

### 2. Go Build Tag

由于 Tesseract 绑定使用了 CGO，为了避免不使用 OCR 的用户引入不必要的依赖，OCR 功能被放置在 `tesseract` 构建标签下。

在运行或编译包含 OCR 功能的代码时，**必须**添加 `-tags tesseract` 标签：

```bash
go run -tags tesseract main.go
# 或
go build -tags tesseract .
```

在代码文件的开头也建议添加构建约束：

```go
//go:build tesseract
// +build tesseract
```

## 快速开始

> **完整示例**: [examples/knowledge/features/OCR](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/OCR)

### 基础用法

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/ocr/tesseract"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    
    // 引入 PDF reader 以支持 PDF 文件解析
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

func main() {
    // 1. 创建 Tesseract OCR 引擎
    ocrExtractor, err := tesseract.New(
        tesseract.WithLanguage("eng+chi_sim"), // 支持英文和简体中文
        tesseract.WithConfidenceThreshold(60.0), // 设置置信度阈值
    )
    if err != nil {
        panic(err)
    }

    // 2. 创建支持 OCR 的知识源
    // OCR 提取器可以注入到 File Source 或 Directory Source 中
    pdfSource := dirsource.New(
        []string{"./data/pdfs"},
        dirsource.WithOCRExtractor(ocrExtractor), // 启用 OCR
        dirsource.WithName("Scanned Documents"),
    )

    // 3. 创建 Knowledge
    kb := knowledge.New(
        knowledge.WithSources([]source.Source{pdfSource}),
        // ... 其他配置 (Embedder, VectorStore)
    )
    
    // ... 加载和使用
}
```

## 配置选项

### Tesseract 配置

`tesseract.New` 支持以下配置选项：

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithLanguage(lang)` | 设置识别语言，多个语言用 `+` 连接 (如 `eng+chi_sim`) | `"eng"` |
| `WithConfidenceThreshold(score)` | 设置最低置信度阈值 (0-100)，低于此阈值的识别结果将被拒绝 | `60.0` |
| `WithPageSegMode(mode)` | 设置页面分割模式 (PSM 0-13)，对应 Tesseract 的 `--psm` 参数 | `3` (全自动) |

### Source 集成

OCR 提取器可以通过 `WithOCRExtractor` 选项集成到以下 Source 中：

- **File Source**: `filesource.WithOCRExtractor(ocr)`
- **Directory Source**: `dirsource.WithOCRExtractor(ocr)`
- **Auto Source**: `autosource.WithOCRExtractor(ocr)`

当配置了 OCR 提取器后，Source 在处理支持的文件类型（如 PDF）时，会尝试对其中的图像或页面进行 OCR 处理。

## 注意事项

1. **性能影响**：OCR 处理是计算密集型任务，会显著增加文档加载时间。建议仅对需要 OCR 的文档源启用此功能。
2. **CGO 依赖**：使用 OCR 功能会导致编译后的二进制文件依赖系统库（`libtesseract`），请确保部署环境已安装相应依赖。
3. **PDF 支持**：要处理 PDF 文件，务必导入 `knowledge/document/reader/pdf` 包。该 Reader 会自动检测 PDF 中的图像内容并调用 OCR 引擎。
