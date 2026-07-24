# pipeline_profile 常见值速查

以下列表均为虚构数据，用于演示 Markdown 表格、分组行和标题层级在不同 chunk size 下的切分效果。

## 在线处理

| pipeline_profile | 对应阶段/用途 |
|------------------|--------------|
| **通用读取** | |
| `reader_auto` | 根据文件扩展名自动选择已注册的 Reader，无法识别时回退到纯文本 |
| `reader_markdown` | 读取 Markdown 标题、段落、列表、表格和 fenced code block |
| `reader_text` | 读取没有结构标记的普通文本，并保留原始段落顺序 |
| `reader_json` | 解析对象与数组，生成结构化的 JSON 文档内容 |
| `reader_csv` | 将多行记录归一化为便于检索的文本表示 |
| **结构预处理** | |
| `normalize_space` | 统一换行符并整理连续空白，便于后续定位自然边界 |
| `preserve_unicode` | 保留中文、组合字符和 emoji 等 Unicode 内容 |
| **分块策略** | |
| `chunk_fixed` | 在大小预算附近寻找换行、句子、标点或空白边界 |
| `chunk_recursive` | 按分隔符优先级递归细分超长片段，再合并较小片段 |
| `chunk_markdown` | 优先保留标题路径和 Markdown block 的结构关系 |
| `chunk_json` | 保留对象和数组层级，并按序列化后的 byte 大小控制结果 |
| **索引写入** | |
| `index_dense` | 为每个 chunk 生成向量并写入向量索引 |
| `index_hybrid` | 同时写入向量字段与关键词字段，用于混合检索 |
| **检索链路** | |
| `search_vector` | 根据 query embedding 召回语义相近的 chunks |
| `search_hybrid` | 合并向量召回和关键词召回结果，再进行统一排序 |
| **质量检查** | |
| `check_budget` | 检查最终 chunk 是否超过配置的 size budget |
| `check_overlap` | 统计相邻 chunks 的实际共享边界是否符合配置 |
| `check_metadata` | 展示 chunk size、overlap 和 Markdown header path metadata |
| `check_mapping` | 尝试把每个 chunk 映射回原文中的连续区间 |
| **其他** | |
| `pipeline_preview` | 只生成预览结果，不连接模型、Embedding 服务或向量数据库 |
| `pipeline_trace` | 输出 Reader、Strategy、大小单位和边界选择过程 |
| `pipeline_noop` | 保留原始 Document，不执行额外的内容转换 |
| `pipeline_fallback` | Reader 不可用时回退到 TextReader 和默认 FixedSizeChunking |

## 离线任务

| pipeline_profile | 对应阶段/用途 |
|------------------|--------------|
| `batch_full_index` | 重新读取全部文档并构建完整索引 |
| `batch_incremental_index` | 只处理内容发生变化的文档，并更新对应 chunks |

## 本地调试

| pipeline_profile | 对应阶段/用途 |
|------------------|--------------|
| `debug_compact` | 在终端打印每个 chunk 的简短预览、大小和 metadata |
| `debug_visual` | 在 Web 页面并排显示原文与 chunks，并绘制来源关联线 |

## 故障演练

| pipeline_profile | 对应阶段/用途 |
|------------------|--------------|
| `fallback_readonly` | **只读降级模式**。该模式使用本地示例文档和确定性的 chunking 策略，不调用外部服务，也不写入任何索引；它用于确认 Reader 选择、Unicode 计数、自然边界、overlap 预算和 metadata 展示在依赖不可用时仍然可以独立运行 |
