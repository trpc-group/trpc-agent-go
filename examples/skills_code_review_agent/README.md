# Automated Skills-Based Code Review Agent

An enterprise-grade, automated Go code review agent prototype powered by **Agent Skills**, **Workspace Sandbox Execution**, **SQLite Database Persistence**, and **Permission Governance**.

Built for the **Tencent Rhino Bird Open Source Talent Program (Issue #2004)**.

---

## Design Document & System Architecture

### 1. Overview & Motivation
Static code analysis and AI-assisted code reviews often suffer from noise, hallucinated suggestions, lack of persistence, and security risks when running external tools or scripts. This Code Review Agent solves these challenges by combining:
- **Declarative Agent Skills (`SKILL.md`)**: Encapsulates language-specific review standards and rules into a standardized skill specification.
- **Sandboxed Tool Execution (`codeexecutor`)**: Isolates external test and lint commands (`go vet`, `go test`, diff helpers) inside secure workspaces.
- **Pre-execution Permission Governance (`tool.PermissionPolicy`)**: Intercepts high-risk operations (e.g. `rm -rf`, network exfiltration) before they reach the execution runtime.
- **Structured SQLite Persistence**: Stores tasks, sandbox runs, permission decisions, findings, audit events, and metrics for auditing and offline inspection.

### 2. Architecture & Data Flow

```text
Unified Diff / Git Patch
        │
        ▼
   Diff Parser  ──► Structured Hunks & Packages
        │
        ▼
  Skill Engine  ──► Load SKILL.md & Review Guidelines
        │
        ▼
  Rule Engine   ──► Analyze Concurrency, Resources, Secrets, Missing Tests, DB Tx
        │
        ▼
Permission Guard──► Intercept High-Risk Commands (allow / deny / ask)
        │
        ▼
Sandbox Engine  ──► Execute Isolated Checks (`codeexecutor`)
        │
        ▼
SQLite Database ──► Store Task, Run, Decision, Findings & Audit Logs
        │
        ▼
Report Output   ──► Output `review_report.json` & `review_report.md`
```

### 3. Database Schema
- `review_tasks`: Task metadata, repository path, diff summary, status, and timestamps.
- `sandbox_runs`: Command lines, status, exit codes, output snippets, and execution duration.
- `permission_decisions`: Commands, permission governance decisions (`allow`/`deny`/`ask`), and decision rationale.
- `findings`: Severity (`high`/`medium`/`low`/`warning`), category, file, line, title, evidence (secret-redacted), recommendation, confidence, and rule ID.
- `audit_events`: Append-only structured JSON audit trail.

### 4. Security & Noise Reduction
- **Secret Redaction**: Hardcoded secrets (API keys, tokens, passwords) are automatically redacted in findings and reports (`sk****ue`).
- **Deduplication**: Findings are deduplicated on `(file, line, category)` keys to prevent duplicate reports for the same code location.
- **Dry-Run Compatibility**: Runs 100% offline in deterministic rule mode without requiring live LLM API keys.

---

## Quick Start

### 1. Run Unit Tests

```bash
go test -v ./examples/skills_code_review_agent/...
```

### 2. Run the CLI Application

```bash
cd examples/skills_code_review_agent
go run . -diff-file="./testdata/02_security_secret.diff" -db-path="review.db"
```

### 3. Command Line Options

- `-diff-file`: Path to a unified diff file (optional, defaults to sample diff if omitted).
- `-repo-path`: Target repository path (default: `.`).
- `-skills-dir`: Path to skills directory (default: `./skills`).
- `-db-path`: Path to SQLite database file (default: `review_agent.db`).
- `-output-json`: Output JSON report path (default: `review_report.json`).
- `-output-md`: Output Markdown report path (default: `review_report.md`).
- `-use-sandbox`: Enable/disable sandbox execution (default: `true`).
