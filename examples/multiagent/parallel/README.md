# Parallel Multi-Agent Example

This example demonstrates how to create and coordinate multiple agents working in **true parallel** using the trpc-agent-go framework. It showcases the handling of interleaved event streams from concurrent agents that analyze **different aspects** of the same problem simultaneously.

## Overview

The parallel agent system consists of four specialized agents that work simultaneously on **different perspectives** of a user query:

- **📊 Market Analysis Agent** - Market trends, size, competition, and dynamics
- **⚙️ Technical Assessment Agent** - Technical feasibility, requirements, and implementation  
- **⚠️ Risk Evaluation Agent** - Risks, challenges, compliance, and mitigation strategies
- **🚀 Opportunity Analysis Agent** - Benefits, strategic advantages, and ROI potential

## Why These Agents Are Perfect for Parallel Execution

### **🔄 Truly Independent Analysis**
Unlike sequential agents (Planning → Research → Writing), these agents:
- ✅ **Work on different dimensions** of the same problem
- ✅ **Don't depend on each other's outputs** 
- ✅ **Can all start immediately** with the same input
- ✅ **Provide complementary perspectives** that combine into comprehensive analysis

### **🎯 Business Decision Framework**
This design mirrors real-world business analysis where teams simultaneously evaluate:
- **Market viability** (Is there demand? Who are competitors?)
- **Technical feasibility** (Can we build it? What's required?)
- **Risk assessment** (What could go wrong? How to mitigate?)
- **Opportunity evaluation** (What's the upside? Is it worth it?)

### **⚡ Maximum Parallelism Benefits**
- **Reduced latency**: All analyses happen simultaneously
- **Diverse perspectives**: Multiple expert viewpoints on same topic
- **Comprehensive coverage**: No aspect of the problem is missed
- **Natural load balancing**: Each agent has equal workload

## Key Features

### 1. **Independent Multi-Perspective Analysis**
```go
// Each agent analyzes the SAME input from a DIFFERENT angle
📊 [market-analysis]: The blockchain supply chain market shows 67% CAGR...
⚙️ [technical-assessment]: Implementation requires distributed ledger infrastructure...
⚠️ [risk-evaluation]: Primary risks include regulatory uncertainty and integration complexity...
🚀 [opportunity-analysis]: Potential 15-20% cost reduction in supply chain transparency...
```

### 2. **Clean Parallel Output Display**
Agents work simultaneously and display complete analysis as they finish:
```
📊 [market-analysis] Started analysis...
⚙️ [technical-assessment] Started analysis...
⚠️ [risk-evaluation] Started analysis...
🚀 [opportunity-analysis] Started analysis...

📊 [market-analysis]: The blockchain supply chain market is experiencing robust growth with a 67% CAGR. Major players like Walmart and Maersk have successfully implemented solutions showing 15-30% improvement in traceability...

⚙️ [technical-assessment]: Implementation requires distributed ledger infrastructure with consensus mechanisms. Key technical requirements include: API integrations with existing ERP systems, IoT sensor compatibility...

⚠️ [risk-evaluation]: Primary risks include regulatory uncertainty in 40% of target markets, integration complexity with legacy systems (estimated 6-12 month timeline)...

🚀 [opportunity-analysis]: Strategic advantages include enhanced transparency leading to 15-20% cost reduction, competitive differentiation in premium markets...
```

**Note:** Streaming is disabled for parallel agents to prevent character-level interleaving that would make output unreadable. Each agent provides complete, coherent analysis.

### 3. **Business-Oriented Use Cases**
Perfect for decision-making scenarios:
- Technology adoption evaluations
- Strategic initiative assessments  
- Product launch decisions
- Investment opportunity analysis

## Running the Example

```bash
cd examples/multiagent/parallel
go run . -model deepseek-chat
```

### Example Session

```
⚡ Parallel Multi-Agent Demo
Model: deepseek-chat
Type 'exit' to end the conversation
Agents: Market 📊 | Technical ⚙️ | Risk ⚠️ | Opportunity 🚀
==================================================

💬 You: Should we implement blockchain for supply chain tracking?

🚀 Starting parallel analysis of: "Should we implement blockchain for supply chain tracking?"
📊 Agents analyzing different perspectives...
────────────────────────────────────────────────────────────────────────────────

📊 [market-analysis] Started analysis...
⚙️ [technical-assessment] Started analysis...
⚠️ [risk-evaluation] Started analysis...
🚀 [opportunity-analysis] Started analysis...

📊 [market-analysis]: The blockchain supply chain market is experiencing robust growth with a 67% CAGR. Major players like Walmart and Maersk have successfully implemented solutions showing 15-30% improvement in traceability...

⚙️ [technical-assessment]: Implementation requires distributed ledger infrastructure with consensus mechanisms. Key technical requirements include: API integrations with existing ERP systems, IoT sensor compatibility, smart contract development...

⚠️ [risk-evaluation]: Primary risks include regulatory uncertainty in 40% of target markets, integration complexity with legacy systems (estimated 6-12 month timeline), and potential vendor lock-in concerns...

🚀 [opportunity-analysis]: Strategic advantages include enhanced transparency leading to 15-20% cost reduction, competitive differentiation in premium markets, and potential new revenue streams through verified sustainability claims...

🎯 All parallel analyses completed successfully!
────────────────────────────────────────────────────────────────────────────────
✅ Multi-perspective analysis completed in 4.1s
```

## Comparison: Parallel vs Sequential Agents

### **❌ Sequential Agents (Chain Style)**
```
Planning Agent → Research Agent → Writing Agent
     ↓              ↓               ↓
   Plan A  →    Research A   →   Write A
   
   - Each agent waits for previous
   - Total time = Agent1 + Agent2 + Agent3
   - Sequential dependency
```

### **✅ True Parallel Agents (This Example)**
```
Market Analysis ↘
Technical Assess → [Combined Analysis] 
Risk Evaluation ↗
Opportunity Analysis ↗

- All agents start simultaneously
- Total time = max(Agent1, Agent2, Agent3, Agent4)
- Independent perspectives
```
