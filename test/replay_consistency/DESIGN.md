# Replay Consistency 设计

`session/replaytest` 提供 runner、normalize、compare、report，不依赖数据库；`test` 保留 backend factory、用例、断言与故障注入。后端生成的 ID/response 时间元数据被归一化，调用方提供的 event timestamp 保留为 UTC；memory query 按无序的精确内容集合断言，state 保留字节类型；summary 比较 filter-key/boundary，track 比较名称、顺序、payload。`allowed_diff` 必须限定 section、包含具体片段的 path、backend pair、reason。retry 分写入前后：前者恢复基线，后者验证 memory/state/summary 幂等并报告重复 event。
