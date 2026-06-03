# Python 代码图边关系解析质量报告

## 概述

本文档总结了对 Python 代码图谱边关系实施"Plan C"两阶段修复后的质量评估：
1. **Go 侧模糊符号匹配** — `graph_source.go` 中基于 short name + prefix 的 ID 重映射
2. **Python 侧类型推断** — `python_parser.py` 中通过 `_build_instance_type_map` 解析 `self.attr.method()` 调用

评估日期：2025-06-03  
目标仓库：`trpc-agent-python`（约 651 个 Python 类）

## 核心指标

| 边类型 | 修复前 | 修复后 | 提升幅度 |
|--------|--------|--------|----------|
| CALLS     | 139       | 4,261     | **30.7 倍** |
| INHERITS  | 0         | 244       | **从无到有** |
| METHOD    | 2,611     | 2,611     | 不变 |
| 总节点数  | 6,158     | 6,158     | 不变 |

## INHERITS 边分析

### 覆盖率

- 图中 Python 类总数：**651**
- 有 INHERITS 边的类：**239**（37%）
- 无 INHERITS 边的类：**412**（63%）

### 缺失原因分类

| 类别 | 数量 | 是否可修复 | 根因 |
|------|------|------------|------|
| 外部基类（`BaseModel`，pydantic） | ~300 | 否 | 第三方类不在图中 |
| 外部基类（`Enum`/`IntEnum`，标准库） | ~50 | 否 | 标准库类 |
| 外部基类（`Generic[_T]`，typing） | ~10 | 否 | 标准库泛型构造 |
| **泛型参数未剥离** | **9** | **是** | `Parent[TypeArg]` 未剥离为 `Parent` |
| 其他外部类 | ~40 | 否 | 第三方/标准库 |

### 可修复问题：泛型参数导致匹配失败

9 个类的父类**存在于图中**，但因泛型类型参数未剥离而无法匹配：

| 子类 | 签名 | 期望父类 |
|------|------|----------|
| `AgentCallbackFilter` | `CallbackFilter[SingleAgentCallback]` | `CallbackFilter` |
| `ModelCallbackFilter` | `CallbackFilter[SingleModelCallback]` | `CallbackFilter` |
| `ToolCallbackFilter` | `CallbackFilter[SingleToolCallback]` | `CallbackFilter` |
| `_FilterManager` | `BaseRegistryFactory[BaseFilter]` | `BaseRegistryFactory` |
| `_ToolManager` | `BaseRegistryFactory[BaseTool]` | `BaseRegistryFactory` |
| `_ToolSetManager` | `BaseRegistryFactory[BaseToolSet]` | `BaseRegistryFactory` |
| `CatRegistryFactory` | `BaseRegistryFactory[Cat]` | `BaseRegistryFactory` |
| `DogRegistryFactory` | `BaseRegistryFactory[Dog]` | `BaseRegistryFactory` |
| `MemorySaver` | `BaseCheckpointSaver[str]` | `BaseCheckpointSaver`（外部） |

**修复方式**：在 `visit_ClassDef` 中调用 `_resolve_symbol()` 前剥离 `[...]`。

### 正确性验证

抽样 15 条 INHERITS 边——全部正确：
- `AgentCancelledEvent` → `Event` ✓
- `BaseAgent` → `AgentABC` ✓
- `BaseFilter` → `FilterABC` ✓
- `LlmAgent` → `BaseAgent` ✓
- `TeamAgent` → `BaseAgent` ✓

多跳遍历验证：
```
AgentABC ← BaseAgent ← {LlmAgent, ChainAgent, ParallelAgent, TeamAgent, GraphAgent, ...}
```

## CALLS 边分析

### 调用目标类型分布

| 目标类型 | 数量 | 占比 |
|----------|------|------|
| Method（方法调用） | 1,645 | 38.6% |
| Class（构造函数） | 1,426 | 33.5% |
| Function（函数调用） | 1,181 | 27.7% |
| Variable（变量引用） | 9 | 0.2% |

### 正确性验证

**样本 1：`LlmAgent._run_async_impl`**（15 条 CALLS）
- `RequestProcessor.build_request` ✓（跨文件，类型推断）
- `LlmProcessor.call_llm_async` ✓（跨文件，类型推断）
- `ToolsProcessor.execute_tools_async` ✓（跨文件，类型推断）
- `Event.get_function_calls` ✓（类型推断方法调用）
- `LlmAgent.accumulate_content` ✓（自身方法）

**样本 2：`Runner.run_async`**（13 条 CALLS）
- `AgentABC.find_agent` ✓（通过 `self.agent.find_agent()`，类型推断）
- `Runner._new_invocation_context` ✓（自身方法）
- `Runner._append_new_message_to_session` ✓（自身方法）
- `handle_cancellation_session_cleanup` ✓（跨模块函数）
- `trace_runner` ✓（跨模块函数）

**样本 3：跨文件方法调用**（随机 15 条）
- `create_agui_runner` → `AgUiService.add_agent` ✓
- `_create_graph` → `StateGraph.add_agent_node` ✓
- `_run_async_impl` → `TeamRunContext.add_cancellation_record` ✓

**所有抽样中未发现误匹配（false positive）。**

### Short-Name 歧义检查

高扇出方法检查：
- `is_model_visible`（19 个调用者 → 唯一定义：`Event.is_model_visible`）— 无歧义 ✓
- `add_node`（17 个调用者 → 图中唯一定义）— 无歧义 ✓

### 已知限制

1. **`Optional[X]` 类型注解**：当 `__init__` 参数标注为 `Optional[BaseSessionService]` 时，类型映射存储完整字符串（含 `Optional[...]`），无法解析为实际类型。影响如 `Runner._run_post_turn_processing`（应有 2 条 CALLS，实际为 0）。

2. **标准库/内建调用已正确过滤**：`asyncio.to_thread()`、`logger.warning()` 等非项目内部调用不会生成边。

3. **动态分发**：`getattr(self, method_name)()` 等模式无法静态解析，属于预期限制。

## 失败案例举例

### Case 1：泛型参数导致 INHERITS 缺失

**现象**：`AgentCallbackFilter` 应继承 `CallbackFilter`，但图中无 INHERITS 边。

```
文件：trpc_agent_sdk/agents/_callback.py
签名：class AgentCallbackFilter(CallbackFilter[SingleAgentCallback])
```

**分析链路**：
1. Python parser `visit_ClassDef` 调用 `ast.unparse(base)` → 得到 `"CallbackFilter[SingleAgentCallback]"`
2. `_resolve_symbol("CallbackFilter[SingleAgentCallback]")` → 解析为 `"trpc_agent_sdk.agents._callback.CallbackFilter[SingleAgentCallback]"`
3. Go 侧 `resolveSymbolID` 尝试匹配 → exact match 失败（图中只有 `CallbackFilter`，没有带 `[...]` 的）
4. short name 匹配也失败（short name 变成了 `CallbackFilter[SingleAgentCallback]`，而非 `CallbackFilter`）
5. 边被丢弃

**图中实际存在的父类**：
```
full_name: trpc_agent_sdk.agents._callback.CallbackFilter
file_path: trpc_agent_sdk/agents/_callback.py
```

**修复**：在生成 `to_id` 前剥离 `[...]`，即 `base_name.split('[')[0]`。

---

### Case 2：`Optional[X]` 注解导致 CALLS 缺失

**现象**：`Runner._run_post_turn_processing` 中调用了 `self.session_service.create_session_summary()`，但 CALLS 边为 0。

```python
# Runner._run_post_turn_processing 的实际代码：
async def _run_post_turn_processing(self, *, invocation_context: InvocationContext) -> None:
    session = invocation_context.session
    try:
        await self.session_service.create_session_summary(session, ctx=invocation_context)
        if self.memory_service and self.memory_service.enabled:
            await self.memory_service.store_session(session, agent_context=...)
    except Exception as exc:
        logger.error(...)
```

**分析链路**：
1. `_build_instance_type_map` 检查 Runner 的 `__init__` 参数注解
2. 参数 `session_service: BaseSessionService` → `param_types["session_service"] = "BaseSessionService"` ✓
3. 参数 `memory_service: Optional[BaseMemoryService]` → `param_types["memory_service"] = "Optional[BaseMemoryService]"` ✗
4. `self.session_service = session_service` → `type_map["session_service"] = "BaseSessionService"` ✓
5. `self.memory_service = memory_service` → `type_map["memory_service"] = "Optional[BaseMemoryService]"` ✗
6. 调用 `self.memory_service.store_session()` 时，`_resolve_symbol("Optional[BaseMemoryService]")` 无法匹配任何类

**图中实际存在的目标方法**：
```
full_name: trpc_agent_sdk.sessions._base_session_service.BaseSessionService.create_session_summary
```

**修复**：解析注解时解包 `Optional[X]` 为 `X`。

---

### Case 3：外部库基类导致 INHERITS 缺失（预期行为）

**现象**：`AgentABC` 继承自 `BaseModel`，但无 INHERITS 边。

```
文件：trpc_agent_sdk/abc/_agent.py
签名：class AgentABC(BaseModel)
```

**分析链路**：
1. Python parser 生成 INHERITS 边：`from_id = "...AgentABC"`, `to_id = "pydantic.BaseModel"`
2. Go 侧 `resolveSymbolID("pydantic.BaseModel", keptSymbols, ...)` → exact match 失败
3. short name "BaseModel" 在 `keptSymbols` 中不存在（pydantic 是外部库，不解析）
4. 边被正确丢弃

**为什么不可修复**：`BaseModel` 来自第三方库 pydantic，其源码不在我们的解析范围内，因此图中不存在该节点。即使建立边也无目标节点可指向。

**影响**：不影响实际使用——agent 查询代码关系时，关注的是项目内部类的继承链（如 `LlmAgent → BaseAgent → AgentABC`），而非 `AgentABC → BaseModel` 这一层。

## 图遍历验证

Apache AGE Cypher 查询修复（`traverseNodeQueryCypher`）同时得到验证：

```sql
-- 继承链遍历：正常工作
MATCH p=(start:Node {id: "...AgentABC..."})<-[:INHERITS*1..2]-(n:Node)
-- 返回：BaseAgent, LlmAgent, TeamAgent 等

-- 方法调用链：正常工作
MATCH p=(start:Node)-[:METHOD*1..1]->(m:Node)-[:CALLS*1..1]->(callee:Node)
-- 返回：该类所有方法调用的目标
```

## 优化建议

### 高优先级（简单修复）

1. **剥离泛型参数**（预计 +8 条 INHERITS）
   ```python
   # visit_ClassDef 中，_resolve_symbol 调用前：
   if '[' in base_name:
       base_name = base_name.split('[')[0]
   ```

2. **解包 `Optional[X]` 类型注解**（预计 +20 条 CALLS）
   ```python
   # _build_instance_type_map 中，获取注解后：
   ann = ast.unparse(arg.annotation)
   if ann.startswith("Optional[") and ann.endswith("]"):
       ann = ann[len("Optional["):-1]
   ```

### 低优先级（收益递减）

3. **类级别类型注解**：部分 Pydantic 模型使用类级声明（`session_service: BaseSessionService`）而非 `__init__` 赋值，读取类体注解可进一步提升覆盖率。

4. **`Union[X, None]` 处理**：与 `Optional[X]` 语义相同但使用显式 Union 语法。

## 结论

两阶段修复实现了 **CALLS 边 30 倍提升**、**INHERITS 边从零到 244**。边正确性优秀——抽样中未检测到误匹配。剩余缺口主要源于：
- 外部库类（预期行为，不可修复）
- 泛型参数语法（可修复，9 条边）
- `Optional[X]` 注解处理（可修复，约 20 条边）

**整体边解析质量：已达生产可用水平。**
