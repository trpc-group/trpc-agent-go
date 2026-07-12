阶段一：配置层 - 创建 types.go，定义数据模型和枚举（Config、Gate、AttributionRule等）

阶段一：配置层 - 创建 config.go，实现配置加载、相对路径重写和验证

阶段一：配置层 - 创建 config_test.go，覆盖配置验证测试

阶段二：适配器层 - 创建 adapters.go，实现评估服务适配、prompt surface patching、profile构建

阶段二：适配器层 - 创建 adapters_test.go，覆盖适配器测试

阶段三：失败归因引擎 - 创建 attribution.go，实现多源结构化归因 + 因果链归因

阶段三：失败归因引擎 - 创建 attribution_test.go，覆盖6类归因测试

阶段四：Delta计算 - 创建 delta.go，实现确定性metric delta计算

阶段四：Delta计算 - 创建 delta_test.go，覆盖6类delta测试

阶段五：发布门禁策略 - 创建 gate.go，实现两阶段门禁 + 多维度规则

阶段五：发布门禁策略 - 创建 gate_test.go，覆盖所有门禁规则测试

阶段六：流水线编排 - 创建 pipeline.go，实现S1-S6六阶段流程

阶段六：流水线编排 - 创建 pipeline_test.go，覆盖流水线测试

阶段七：报告生成与审计 - 创建 report.go，实现JSON/Markdown双格式报告

阶段七：报告生成与审计 - 创建 report_test.go，覆盖报告测试

阶段八：端到端示例 - 创建 main.go，CLI入口和模式选择

阶段八：端到端示例 - 创建 pipeline.go（thin wiring）和 analysis.go

阶段八：端到端示例 - 创建 fake.go，实现fake model和PromptIter workers

阶段八：端到端示例 - 创建 trace_smoke.go，实现trace回放模式

阶段八：端到端示例 - 创建 agent.go，定义候选agent

阶段八：端到端示例 - 创建配置文件（config/）

阶段八：端到端示例 - 创建示例数据（data/promptiter-regression-app/）

阶段八：端到端示例 - 创建示例报告输出（output/）

阶段八：端到端示例 - 创建 pipeline_test.go，端到端测试

阶段九：测试验证 - 运行单元测试

阶段九：测试验证 - 运行集成测试

阶段九：测试验证 - 运行端到端测试

阶段九：测试验证 - 执行端到端示例（fake模式）

阶段九：测试验证 - 执行端到端示例（trace-smoke模式）

阶段九：测试验证 - 运行lint检查

阶段十：全量验收 - 验证验收标准1：6条样例case可运行

阶段十：全量验收 - 验证验收标准2：决策准确率≥80%

阶段十：全量验收 - 验证验收标准3：过拟合场景拒绝候选

阶段十：全量验收 - 验证验收标准4：归因准确率≥75%

阶段十：全量验收 - 验证验收标准5：fake/trace耗时≤3分钟

阶段十：全量验收 - 验证验收标准6：报告完整性

阶段十一：技术债清理 - 补充Go doc注释（覆盖率≥80%）

阶段十一：技术债清理 - 支持归因规则可配置化

阶段十一：技术债清理 - 增强配置验证逻辑

阶段十一：技术债清理 - 性能优化（map索引）
