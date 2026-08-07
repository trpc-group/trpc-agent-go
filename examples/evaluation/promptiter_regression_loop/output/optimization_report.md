# Optimization & Regression Audit Report

**Execution Mode**: `fake_deterministic`  
**Overall Status**: 🟢 **PROMOTED (Accepted)**  
**Baseline Val Score**: `0.8500`  
**Best Candidate Val Score**: `0.9833` (Delta: `+0.1333`)  
**Total Cost**: `$0.0600` | **Total Duration**: `0.15s`  

## Executive Summary

Candidate Round 1 (system_prompt patch) passed all acceptance gates with validation gain +0.1333 and zero hard fails. Promoted to production.

## Round History & Gate Decisions

| Round | Target Surface | Val Score | Score Delta | Gate Decision | Reason |
|-------|----------------|-----------|-------------|---------------|--------|
| 1 | `system_prompt` | `0.9833` | `+0.1333` | ✅ ACCEPTED | Candidate passed all gates with validation score gain +0.1333 |
| 2 | `tool_desc_calc` | `0.8500` | `-0.1333` | ❌ REJECTED | Candidate introduced new hard fail on case 'val_opt_01' |
| 3 | `router_prompt` | `0.6667` | `-0.3167` | ❌ REJECTED | Candidate introduced new hard fail on case 'val_opt_02' |

## Failure Attribution Summary

| Round | Case ID | Category | Severity | Explanation |
|-------|---------|----------|----------|-------------|
| 1 | `train_opt_01` | `final_response_mismatch` | `medium` | Final assistant text response did not match target expectation. |
| 1 | `train_opt_02` | `tool_argument_error` | `high` | Tool argument type mismatch or invalid parameter format. |
| 3 | `val_opt_02` | `route_error` | `high` | Request routed to wrong sub-agent or tool handler. |

