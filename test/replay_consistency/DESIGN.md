# Replay Consistency 设计

`session/replaytest` 负责执行、归一化、比较和报告；`test` 保留后端装配与故障注入。Add alias 使用不含 topics 的 canonical identity，update 后自动推进原 alias。state 通过独立 app/user 读取和临时 peer 校验作用域；违规与 peer 清理失败均为 runner error，不能由 `allowed_diff` 放行。其余规则保持：事件时间保留，memory query 精确比较内容，state 保留 byte 类型；retry 区分写入前后。
