# Replay Consistency 设计

`session/replaytest` 负责执行、归一化、比较和报告；`test` 保留后端装配与故障注入。Summary 用单调 `EventPrefix` 表达事件间的执行顺序。Track 分别比较 map key、外层容器和内嵌 event；payload 覆盖完整 JSON 值域，并以标签区分 nil、空字节、JSON、UTF-8 和二进制。Backend 名称必须非空且无首尾空白。既有 alias、state scope、事件时间、精确 memory query 与 retry 语义保持不变。
