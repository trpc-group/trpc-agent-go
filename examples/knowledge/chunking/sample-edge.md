# Markdown 边角用例

这个样例不是一篇自然文章，而是一组用来放大边界行为的 Markdown 片段。建议先用 Reader default，再依次尝试 500、240、120 的 chunk size，并为后两组配置少量 overlap。

## 只有标题的章节

## 跳级标题与路径

#### 直接出现的四级标题

这一段位于四级标题下面，用来确认 metadata 中的 header path 如何处理跳过的层级。它既包含中文，也 contains a short English clause, so punctuation and whitespace boundaries can be compared in the same block.

### 返回三级标题

标题层级返回以后，后续 chunk 的路径不应继续携带已经离开的四级标题。

## 宽表格

| case | input | expected observation | note |
|---|---|---|---|
| CJK | 中文没有单词空格，但有逗号、分号；句号。 | 优先在中文标点附近切分 | 每个汉字通常计为一个 rune |
| emoji | 👨‍👩‍👧‍👦 family, 🇨🇳 flag, 👍🏽 tone | 字符串保持有效 UTF-8 | 一个视觉字形可能包含多个 runes |
| combining | café and café look similar | 两种写法的 rune 数可能不同 | 后者由 `e` 和 combining mark 组成 |
| long cell | This cell deliberately contains a much longer explanation so that a small chunk budget cannot keep the complete table row together and the Markdown strategy must find a finer boundary without cutting through arbitrary words. | 继续按句子或空白细分 | 表格语法仍保留在原文中 |
| empty |  | 空单元格不会让 Reader 崩溃 | 连续竖线是有效输入 |

## 嵌套结构

> 引用块第一层说明检索结果。
>
> > 第二层引用包含更具体的证据；它后面还有一个列表。
> >
> > - 第一项包含 `inline code`。
> > - 第二项包含 [一个链接](https://example.com/knowledge/chunking?mode=edge)。

1. 有序列表第一项
   - 子项 A 包含中文标点：先读取，再切分，最后写入 metadata。
   - 子项 B contains English punctuation. It should still prefer a sentence boundary.
2. 有序列表第二项
   1. 更深一层
   2. 同一层的下一项

---

## 超长代码块

下面的代码块故意超过较小的 chunk budget。代码里的空行、注释、字符串和类似 Markdown 标题的内容都应该被当作 fenced block 内容，而不是新的文档标题。

```go
package pipeline

import (
    "context"
    "fmt"
    "strings"
    "unicode/utf8"
)

type Document struct {
    ID       string
    Name     string
    Content  string
    Metadata map[string]any
}

type Chunk struct {
    Index    int
    Content  string
    Runes    int
    Bytes    int
    Metadata map[string]any
}

type Strategy interface {
    Chunk(context.Context, *Document) ([]Chunk, error)
}

type Reporter struct {
    ChunkSize int
    Overlap   int
}

func (r Reporter) Run(
    ctx context.Context,
    strategy Strategy,
    document *Document,
) ([]Chunk, error) {
    if r.ChunkSize <= 0 {
        return nil, fmt.Errorf("chunk size must be positive")
    }
    if r.Overlap < 0 || r.Overlap >= r.ChunkSize {
        return nil, fmt.Errorf("overlap must be smaller than chunk size")
    }

    chunks, err := strategy.Chunk(ctx, document)
    if err != nil {
        return nil, fmt.Errorf("chunk %q: %w", document.Name, err)
    }
    for index := range chunks {
        chunks[index].Index = index + 1
        chunks[index].Runes = utf8.RuneCountInString(chunks[index].Content)
        chunks[index].Bytes = len(chunks[index].Content)
        if chunks[index].Runes > r.ChunkSize {
            return nil, fmt.Errorf(
                "chunk %d exceeds budget: got %d runes, limit %d",
                index+1,
                chunks[index].Runes,
                r.ChunkSize,
            )
        }
    }
    return chunks, nil
}

func Preview(content string, head int, tail int) string {
    normalized := strings.ReplaceAll(content, "\n", " ↵ ")
    runes := []rune(normalized)
    if len(runes) <= head+tail {
        return normalized
    }
    return string(runes[:head]) + " … " + string(runes[len(runes)-tail:])
}

func Explain() string {
    return strings.Join([]string{
        "# This is text inside a Go string, not a Markdown heading.",
        "中文逗号，中文句号。English comma, English period.",
        "The final sentence is intentionally long enough to provide several natural boundaries inside one source line.",
    }, " ")
}
```

代码块之后的正文用于确认 fence 已正确闭合。MarkdownChunking 应恢复普通块解析，并继续携带 `Markdown 边角用例 -> 超长代码块` 这条标题路径。

## 连续内容

普通长句：当一个段落超过预算时，策略应该依次尝试换行、句子标点、一般标点和空白边界；只有确实找不到自然边界时，才回退到精确的 rune 位置，从而保证最终结果不超过配置的 chunk size。

连续 token：

`TRPCAgentGoMarkdownChunkingEdgeCaseWithoutAnyNaturalBoundary0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz再追加一段没有空格也没有标点的中文内容用于触发最终回退`

## 末尾

文档最后没有额外章节。这个短段落用于观察尾块是否被遗漏，以及 overlap 是否只重复已存在的内容。
