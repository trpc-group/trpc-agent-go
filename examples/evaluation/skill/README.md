# Skill Call Evaluation Example

> This demo intentionally pins the agent to an older skill execution
> surface so the tool-trajectory test can assert against its specific
> tool names. For the recommended way to wire Agent Skills in new
> code, see [docs/mkdocs/en/skill.md](../../../docs/mkdocs/en/skill.md);
> the evaluation patterns shown here transfer over unchanged.

This example demonstrates how to evaluate whether an agent correctly uses **Agent Skills** tools with the existing evaluation pipeline.

It uses the **tool trajectory** evaluator (`tool_trajectory_avg_score`) to verify:
- The agent loads the expected skill first.
- The agent then invokes the skill.
- Skill execution succeeds (`exit_code == 0`).

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Run

```bash
cd trpc-agent-go/examples/evaluation/skill
go run . \
  -model "deepseek-chat" \
  -skills-dir "./skills" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "skill-call-basic" \
  -runs 1
```

## Layout

```text
skill/
  agent.go
  main.go
  skills/
    write-ok/
      SKILL.md
      scripts/write_ok.sh
  data/
    skill-eval-app/
      skill-call-basic.evalset.json
      skill-call-basic.metrics.json
  output/
    skill-eval-app/
      *.evalset_result.json
```

