# Markdown Edge Cases

This sample is a collection of Markdown fragments rather than a natural article. Start with the Reader default, then try chunk sizes of 500, 240, and 120 with an overlap of 20 Unicode runes on the latter two.

## Heading-Only Section

## Skipped Heading Levels and Paths

#### A Level-Four Heading Appears Directly

This paragraph sits below a level-four heading and shows how header-path metadata handles skipped levels. It contains several clauses so punctuation and whitespace boundaries can be compared in the same block.

### Returning to Level Three

After the heading level moves back, subsequent chunks should no longer carry the level-four heading that has been left behind.

## Wide Table

| case | input | expected observation | note |
|---|---|---|---|
| CJK | 中文没有单词空格，但有逗号、分号；句号。 | Prefer nearby CJK punctuation | Each Han character normally counts as one rune |
| emoji | 👨‍👩‍👧‍👦 family, 🇨🇳 flag, 👍🏽 tone | Keep the string valid UTF-8 | One visible glyph may contain several runes |
| combining | café and café look similar | The two forms may have different rune counts | The latter uses `e` followed by a combining mark |
| long cell | This cell deliberately contains a much longer explanation so that a small chunk budget cannot keep the complete table row together and the Markdown strategy must find a finer boundary without cutting through arbitrary words. | Refine by sentence or whitespace | The original source still contains valid table syntax |
| empty |  | Do not fail on an empty cell | Adjacent pipes are valid input |

## Nested Structures

> The first blockquote level summarizes a retrieval result.
>
> > The second level contains more specific evidence and is followed by a list.
> >
> > - The first item contains `inline code`.
> > - The second item contains [a link](https://example.com/knowledge/chunking?mode=edge).

1. First ordered-list item
   - Child A describes three steps: read the source, split the content, and write metadata.
   - Child B contains English punctuation. It should still prefer a sentence boundary.
2. Second ordered-list item
   1. One level deeper
   2. The next item at the same level

---

## Oversized Code Block

The following code block deliberately exceeds a small chunk budget. Blank lines, comments, strings, and text resembling Markdown headings must remain fenced-block content rather than becoming document headings.

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

This paragraph confirms that the fence closed correctly. MarkdownChunking should resume ordinary block parsing and retain the `Markdown Edge Cases -> Oversized Code Block` header path.

## Continuous Content

Ordinary long sentence: when a paragraph exceeds the budget, the strategy should try line, sentence, punctuation, and whitespace boundaries in order; it should fall back to an exact rune position only when no natural boundary exists, ensuring that final output remains within the configured chunk size.

Unbroken token with an intentional CJK suffix:

`TRPCAgentGoMarkdownChunkingEdgeCaseWithoutAnyNaturalBoundary0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz再追加一段没有空格也没有标点的中文内容用于触发最终回退`

## End of Document

There is no additional section after this one. This short paragraph checks whether tail content is retained and whether overlap repeats only content that already exists.
