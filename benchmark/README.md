# Benchmarks

The benchmark suites that used to live in this directory have moved to the dedicated [`trpc-agent-go-benchmark`](https://github.com/trpc-group/trpc-agent-go-benchmark) repository.

This keeps the main `trpc-agent-go` repository focused on the framework itself, while benchmark datasets, evaluation assets, and benchmark-specific runners are maintained separately.

## Benchmark Repository

- Repository: https://github.com/trpc-group/trpc-agent-go-benchmark
- Main entry: https://github.com/trpc-group/trpc-agent-go-benchmark/blob/main/README.md

## Available Suites

- [`anthropic_skills`](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/anthropic_skills): Agent Skills compatibility, token usage, and prompt cache benchmarks.
- [`gaia`](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/gaia): GAIA benchmark implementation, datasets, and evaluation runner.
- [`knowledge`](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/knowledge): Knowledge-system evaluation scripts and reports.
- [`memory`](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/memory): Long-context and memory-backend benchmark suites.
- [`summary`](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/summary): Session summarization evaluation assets and runner.
- [`toolsearch`](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/toolsearch): Tool-search evaluation assets and runner.

## Relationship to `trpc-agent-go`

The benchmark repository is designed for evaluating or demonstrating `trpc-agent-go`. See the benchmark repository README for suite-specific setup and usage.

For framework documentation, examples, and API usage, continue to use the main repository:

- https://github.com/trpc-group/trpc-agent-go
