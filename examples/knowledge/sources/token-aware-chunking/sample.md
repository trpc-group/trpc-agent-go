# Token-Aware Knowledge

Token-aware chunking keeps each emitted chunk's content within a tokenizer budget instead of approximating its size with bytes or runes.

## Markdown Structure

The source still selects MarkdownChunking. Heading paths remain available as metadata, and the strategy continues to prefer Markdown and natural text boundaries.

### Mixed Characters

English words and 中文字符 consume different numbers of tokens. Emoji such as 🚀 can also span multiple tokens, so rune counts alone cannot predict the chunk's token size precisely.

## Overlap

The configured overlap is included in the chunk content budget. Each emitted chunk is recounted after its overlap and separator are attached.
