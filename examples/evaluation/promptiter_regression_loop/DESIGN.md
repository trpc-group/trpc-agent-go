# 方案设计说明

流水线以同一配置评测baseline的训练与验证集，逐case保存分数、状态、回复、工具、路由、格式、召回、rubric、trace和usage。归因依次检查工具与参数、路由、格式、知识召回，再以否定范围的rubric和回复相似度兜底，确保失败有证据。

优化器以固定seed接入PromptIter的Profile/PatchSet，把训练失败转为instruction候选。候选重跑评测，相对baseline计算逐case、逐指标差值。fake输出用语义SHA-256绑定正文；无效优化也须显式重跑，不能复用ID取高分。

门禁只看验证与预算：要求增益、覆盖和prompt变化，限制新增失败及单case降幅，禁止新增hard fail、关键case退化和预算超限。训练/验证ID隔离；拒绝轮次不成为基线，故训练提升不能掩盖验证退化。

Report原子生成JSON/Markdown，保存每轮prompt、哈希、patch、评测、归因、delta、门禁、seed、引擎和usage。trace核验终态、工具、召回、rubric、调用和时延；源prompt只读。
