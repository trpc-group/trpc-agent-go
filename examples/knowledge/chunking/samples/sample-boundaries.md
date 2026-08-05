# Chunking Boundary Cases

This sample collects representative text-boundary cases. In the web viewer,
select **RecursiveChunking**, set the chunk size to `120`, and use an overlap of
`20` for a readable boundary preview. Then raise the overlap to `100` as a
stress case: every final chunk should still remain within 120 Unicode runes
even though only a small part of each chunk is new content.

To inspect Markdown section packing, select **MarkdownChunking**, set the chunk
size to `1500`, and use zero overlap. Headings remain preferred semantic
boundaries, while adjacent small sections share a chunk when the combined
content fits the configured budget.

## Budget and Overlap

Reliable document ingestion requires the configured chunk size to describe the
final text sent to an embedding model. Repeated context is useful near a chunk
boundary, but overlap must consume part of the same budget instead of being
prepended after a full-size chunk. This paragraph is intentionally long enough
to create several chunks under both moderate and very large overlap settings.

## Numeric Dots and Version Labels

The measured value was 12.6 millimeters before calibration and 13.1
millimeters afterward. Section 2.8.12 defines the recovery procedure, while
release v1.2.3 documents the compatible wire format. Decimal points, dotted
section identifiers, and semantic versions should remain intact when they fit
inside the configured budget.

## English Sentence Boundaries

Natural boundaries improve retrieval quality. A complete sentence should be
preferred over cutting through an arbitrary word. Commas, semicolons, and
whitespace provide progressively finer fallbacks when a full sentence is too
large, and a rune-safe hard split remains available for an indivisible token.

## CJK Sentence Boundaries

以下内容专门验证中文边界。第一句在这里结束。第二句包含连续标点，真的可以吗？！下一句不应从单独的感叹号开始！数值
12.6、章节 2.8.12 和版本 v1.2.3 中的点也不应被当作句末。

## Unbroken Fallback

The following value has no natural internal boundary, so a hard split is
expected while the final budget still remains strict:
`boundary_regression_abcdefghijklmnopqrstuvwxyz_ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789`.
