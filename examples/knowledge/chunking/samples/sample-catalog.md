# Common pipeline_profile Values

All entries below are fictional. The document demonstrates how Markdown tables, grouped rows, and heading levels are split under different chunk-size budgets.

## Online Processing

| pipeline_profile | Stage or purpose |
|------------------|------------------|
| **General reading** | |
| `reader_auto` | Select a registered Reader from the file extension and fall back to plain text when the type is unknown |
| `reader_markdown` | Read Markdown headings, paragraphs, lists, tables, and fenced code blocks |
| `reader_text` | Read plain text without structural markup while preserving the original paragraph order |
| `reader_json` | Parse objects and arrays into structured JSON document content |
| `reader_csv` | Normalize multiline records into a text representation suitable for retrieval |
| **Structural preprocessing** | |
| `normalize_space` | Normalize line endings and repeated whitespace before locating natural boundaries |
| `preserve_unicode` | Preserve CJK text, combining characters, emoji, and other Unicode content |
| **Chunking strategies** | |
| `chunk_fixed` | Find a nearby line, sentence, punctuation, or whitespace boundary within the size budget |
| `chunk_recursive` | Refine oversized segments by separator priority, then combine smaller adjacent pieces |
| `chunk_markdown` | Preserve the relationship between heading paths and Markdown blocks whenever possible |
| `chunk_json` | Preserve object and array hierarchy while enforcing the serialized-byte budget |
| **Index writes** | |
| `index_dense` | Generate an embedding for every chunk and write it to a vector index |
| `index_hybrid` | Write vector and keyword fields together for hybrid retrieval |
| **Retrieval pipeline** | |
| `search_vector` | Retrieve semantically similar chunks from a query embedding |
| `search_hybrid` | Merge vector and keyword candidates before applying a shared ranking stage |
| **Quality checks** | |
| `check_budget` | Verify that every final chunk stays within the configured size budget |
| `check_overlap` | Measure the actual shared boundary between adjacent chunks |
| `check_metadata` | Display chunk size, overlap, and Markdown header-path metadata |
| `check_mapping` | Map each chunk back to a contiguous region of the source when possible |
| **Other modes** | |
| `pipeline_preview` | Produce preview output without connecting to a model, embedding service, or vector database |
| `pipeline_trace` | Report the Reader, strategy, size unit, and selected boundaries |
| `pipeline_noop` | Preserve the original Document without applying another content transformation |
| `pipeline_fallback` | Fall back to TextReader and the default FixedSizeChunking strategy when a Reader is unavailable |

## Offline Jobs

| pipeline_profile | Stage or purpose |
|------------------|------------------|
| `batch_full_index` | Read every document again and rebuild the complete index |
| `batch_incremental_index` | Process only changed documents and update their corresponding chunks |

## Local Debugging

| pipeline_profile | Stage or purpose |
|------------------|------------------|
| `debug_compact` | Print a compact preview, size, and metadata for each chunk in the terminal |
| `debug_visual` | Show the source and chunks side by side in the web viewer with source connector lines |

## Failure Drills

| pipeline_profile | Stage or purpose |
|------------------|------------------|
| `fallback_readonly` | **Read-only fallback mode.** Use local sample documents and deterministic chunking without calling external services or writing an index; this verifies that Reader selection, Unicode counting, natural boundaries, overlap budgets, and metadata remain independently observable when dependencies are unavailable |
