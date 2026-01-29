# Redis Session V2 合并与兼容方案

## 1. 背景与问题陈述

当前的 Redis Session（V1）实现存在以下关键问题，需要引入 V2 解决：

1.  Cluster Slot 热点：
    *   V1 使用 {AppName} 作为 Redis Cluster hash tag
    *   在 Redis Cluster 模式下，这会导致同一应用（或同一 App+User）的 Session 数据集中到同一个 slot（节点）

2.  数据结构局限：
    *   V1 将事件列表存储为单个 ZSet；对“修改/删除指定事件不友好，无法修改中间 Event
    *   缺乏多维索引能力（例如按 RequestID 查询）

## 2. 解决方案概述

为了在解决 slot 热点问题的同时，实现"新用户无感、老用户兼容、零迁移成本"，且尽可能减少迁移的影响。

### Key 生成与版本隔离

V1 与 V2 采用不同的 key 命名规则，通过前缀自然隔离：

| 版本 | Key 前缀 | 示例 | Hash Tag 粒度 |
| :--- | :--- | :--- | :--- |
| V1 | 无 | `sess:{app}:{user}`, `event:{app}:{user}:{sessID}` | App 级别 |
| V2 | `v2:` | `v2:meta:{app:user}:sessID`, `v2:evtdata:{app:user}:sessID` | 可配置（默认 User 级别） |

V2 的 key 生成逻辑：
1. 所有 session 相关 key 统一加 `v2:` 前缀，确保与 V1 数据在 Redis 中物理隔离
2. 用户可通过 `WithKeyGenerator` 自定义 V2 的 key 生成方式（仅对 V2 生效，V1 保持固定格式）
3. 默认 key 格式采用 user 级别 hash tag：`v2:meta:{AppName:UserID}:SessionID`，同一用户的 session 落在同一 slot，便于 Lua 脚本原子操作

### 核心原则

尽可能减少兼容逻辑的影响，保证新的session service走到新的逻辑上：

1. CreateSession：优先检测 V2 key 和 V1 key 是否存在，若已存在则不做操作并返回错误；若均不存在则创建 V2 session，并设置 `ServiceMeta["version"]="v2"`
2. GetSession：一次操作同时获取 V2 key 和 V1 key 的 session 元数据，命中哪个走哪个逻辑；根据命中的版本设置 `ServiceMeta["version"]`
3. AppendEvent：若 `ServiceMeta["version"]="v2"` 则直接走 V2 逻辑；若为空则执行一次检测确定是 V2 key 还是 V1 key，然后路由到对应实现
4. 不主动迁移：不对存量 V1 数据做搬迁，V1 session 继续按原格式运行，依赖 TTL 自然过期淘汰。对于未设置 TTL 的 session，后续单独支持数据迁移模式

## 3. 详细设计流程

### 3.1 内存版本标记 (In-Memory Version Tag)

为了避免在 `AppendEvent`、`UpdateSessionState` 等写入类操作中重复查询 Redis 确认版本，在 `session.Session` 结构体上新增 `ServiceMeta` 字段用于存储 service 层面的元数据。

```go
type Session struct {
    // ... 其他字段
    
    // ServiceMeta 存储 service 层面的元数据（仅内存使用，不持久化）
    // 例如存储版本信息：ServiceMeta["version"] = "v2"
    ServiceMeta map[string]string
}
```

### 3.2 操作流程详解

#### A. CreateSession (创建会话)
目标：创建 V2 session，避免覆盖已存在的 V1 或 V2 session。

1. 同时检测 V2 key 和 V1 key 是否存在（一次 pipeline 或 Lua 脚本）
2. 若任一存在：返回"已存在"错误，不做操作
3. 若均不存在：使用 `SETNX` 原子创建 V2 session
4. 返回的 session 对象设置 `ServiceMeta["version"]="v2"`

#### B. GetSession (获取会话)
目标：一次操作获取 V2 和 V1 元数据，减少网络往返。

1. 同时读取 V2 meta key 和 V1 session key（一次 pipeline）
2. 若 V2 命中：反序列化 V2 数据，设置 `ServiceMeta["version"]="v2"`，返回
3. 若 V2 未命中但 V1 命中（且开启 `WithLegacySupport`）：反序列化 V1 数据，设置 `ServiceMeta["version"]="v1"`，返回
4. 若均未命中：返回 `NotFound`

#### C. AppendEvent / Update (更新/追加)
目标：优先使用内存标记路由，避免额外探测。

1. 检查 `sess.ServiceMeta["version"]`：
    *   `v2`：直接路由到 V2 实现
    *   `v1` 或为空：直接路由到 V1 实现（老数据可能没有 version 字段）

### 3.3 Key 策略对比

| 版本 | Hash Tag 策略 | Key 示例 | Cluster 行为 |
| :--- | :--- | :--- | :--- |
| V1 | App Level | `sess:{app}:{user}` | 热点集中，同一 app 的数据落在同一 slot |
| V2 | User Level（默认） | `v2:meta:{app:user}:sessID` | 同一用户的 session 在同一 slot，支持 Lua 原子操作 |

## 4. 代码组织与配置

### 4.1 包结构重构
`session/redis` 包结构如下：

*   `service.go`: Facade。实现 `session.Service` 接口，持有 `v1` 与 `v2` 客户端并进行路由。
*   `internal/v1/`: 包含 V1 实现逻辑。
*   `internal/v2/`: 包含 V2 实现逻辑（迁移自 `redisv2`）。
*   `internal/util/`: 共享工具函数。
*   `options.go`: 新增配置项。
*   `key_generator.go`: 包含 V2 Key 生成器接口及默认实现。

### 4.2 配置选项 (Options)

```go
type ServiceOpts struct {
    // 是否开启 V1 兼容模式。
    // 默认: true。
    // 未来 V1 数据全部过期后，可设为 false 以彻底关闭 V1 查询路径。
    enableLegacy bool
    
    // V2 的 Key 生成器（仅对 V2 生效）。
    // 默认: 使用 session 级别 hash tag 的内置生成器。
    keyGenerator KeyGenerator
    
    // ... 其他原有配置
}

// WithLegacySupport 控制是否回退查询 V1 数据。
func WithLegacySupport(enable bool) ServiceOpt { ... }

// WithKeyGenerator 自定义 V2 的 Key 生成方式（仅对 V2 生效）。
func WithKeyGenerator(gen KeyGenerator) ServiceOpt { ... }
```

## 5. 迁移与兼容性路径

1.  阶段一：双栈运行（当前）
    *   代码合并，默认开启 `WithLegacySupport(true)`。
    *   新 session 创建与写入走 V2。
    *   老 session 按 V1 逻辑继续运行，直到 TTL 过期。

2.  阶段二：自然过期
    *   随时间推移（通常覆盖一个 TTL 周期以上），V1 数据自然淘汰，主流流量转为 V2。

3.  阶段三：关闭兼容（未来）
    *   业务确认 V1 数据已清理后，配置 `WithLegacySupport(false)`。
    *   移除读取路径的 V1 回退逻辑；最终可删除 `internal/v1` 兼容实现。


