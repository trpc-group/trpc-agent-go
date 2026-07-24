# Chunking 能力演示

这份文档专门用来观察不同分块策略的边界。它同时包含 Markdown 标题、中英文标点、列表、代码块、emoji 和较长的连续文本，因此可以直观看到固定长度切分与结构化切分的差异。

## 为什么需要 Chunking

知识库通常不能把整篇文档直接交给 Embedding 模型。Chunking 会把文档转换成更小的检索单元；分块太大会混合多个主题，分块太小则容易丢失上下文。Overlap 可以把前一个分块的结尾带入下一个分块，但它也会增加索引体积。

The same trade-off appears in English documents. Large chunks preserve context but may reduce retrieval precision. Small chunks are focused, but a sentence or an argument can be split across two boundaries.

### 一个简单的判断标准

- 每个 chunk 不应超过配置的大小预算。
- 中文字符必须保持有效的 UTF-8 编码。
- RecursiveChunking 应优先使用段落和句子边界。
- MarkdownChunking 应尽量保留标题与所属正文的关系。
- 只有显式配置 overlap 时，相邻 chunk 才需要携带重复内容。

## 混合字符与自然边界

第一段使用中文标点。模型收到问题以后，会先分析意图；然后检索知识库；最后组合答案！如果检索结果不足，它是否应该继续调用工具？这取决于 Agent 的执行策略。

The next paragraph uses English punctuation. An agent receives a request, selects a tool, observes the result, and then decides whether another step is required. Sentence-aware splitting should prefer these punctuation boundaries instead of cutting through arbitrary words.

Emoji 也属于输入内容的一部分：🤖 表示 Agent，🔍 表示检索，✅ 表示完成。按 byte 长度切分可能破坏多字节字符，而按 Unicode rune 切分可以保持字符串有效。

## 配置示例

下面的代码块用于观察 Markdown 策略是否尽量保留结构：

```go
strategy := chunking.NewRecursiveChunking(
    chunking.WithRecursiveChunkSize(240),
    chunking.WithRecursiveOverlap(24),
)

chunks, err := strategy.Chunk(doc)
```

配置完成后，可以比较每个 chunk 的正文、字符数、metadata 和相邻重叠。这个过程不需要模型，也不需要连接向量数据库。

## 较长段落

在真实知识库中，一个段落可能连续解释多个相关概念。例如，文档先说明数据如何进入 Reader，再说明 Reader 如何产生 Document，随后由 Chunking Strategy 生成多个 chunk，最后由 Embedder 和 Vector Store 完成索引。RecursiveChunking 需要先尝试较高优先级的分隔符，并使用下一级分隔符继续细分仍然过大的片段。这个递归细分过程既要保留原始顺序，也要避免在 Unicode 文本中破坏多字节字符。

为了测试没有空格的连续内容，下面保留一段较长标识符：

`TRPCAgentGoChunkingBoundaryWithoutSpaces0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz`

## 结论

好的分块结果不是单纯追求 chunk 数量最少，而是在大小预算、语义完整性、检索精度和索引成本之间取得平衡。这个样例可以反复调整 chunk size 与 overlap，观察三种策略的实际输出。
