# GAIA Benchmark for trpc-agent-go

[GAIA](https://arxiv.org/abs/2311.12983) is a benchmark for evaluating augmented LLMs (with tool use, web search, code execution, etc.). This directory contains a complete implementation for running GAIA evaluation using the trpc-agent-go framework.

## Directory Structure

```
benchmark/gaia/
├── README.md                    # This file
├── data/                        # Dataset directory (Git LFS)
│   ├── gaia_2023_level1_validation.json
│   ├── gaia_sample.json
│   └── 2023/
│       └── validation/         # Level 1 validation set attachments (images/audio/documents)
├── results/                     # Evaluation results output directory
├── skills/                      # Agent Skills
│   └── whisper/                # Audio transcription skill
├── skill_workspaces/           # Skill runtime workspace
└── trpc-agent-go-impl/         # Evaluation program implementation
    ├── main.go
    ├── go.mod
    └── go.sum
```

> **Note**: Currently only Level 1 evaluation is supported. Level 2 and Level 3 datasets are not included.

## Prerequisites

### 1. Install Git LFS

Files in the `data` directory are managed with Git LFS (contains binary files such as images, audio, documents, etc.).

```bash
# Ubuntu/Debian
sudo apt-get install git-lfs

# CentOS/RHEL
sudo yum install git-lfs

# macOS
brew install git-lfs

# Initialize Git LFS
git lfs install
```

### 2. Pull LFS Files

After cloning the repository, you need to pull the LFS files:

```bash
# Option 1: Clone with automatic LFS pull
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# Option 2: Already cloned but LFS files not pulled
git lfs pull

# Option 3: Pull only LFS files in the data directory
git lfs pull --include="benchmark/gaia/data/**"
```

Verify that LFS files are correctly pulled:
```bash
# Check LFS file status
git lfs ls-files

# Check if files contain actual content (not LFS pointers)
head -c 100 benchmark/gaia/data/2023/validation/cca530fc-4052-43b2-b130-b30968d8aa44.png
# If binary content is displayed instead of "version https://git-lfs.github.com/spec/v1", the pull was successful
```

### 3. Configure Model

The evaluation program uses `deepseek-v3-local-II` model by default. To use a different model, specify it via command line:

```bash
-model <model-name>
```

Ensure the model service is properly configured and accessible.

### 4. Install Whisper (Audio Transcription)

Some GAIA tasks require audio file processing. Install OpenAI Whisper:

```bash
pip install openai-whisper
```

## Running the Evaluation

### Basic Usage

```bash
cd benchmark/gaia/trpc-agent-go-impl

# Run Level 1 validation set (all tasks)
go run main.go

# Run a specific number of tasks
go run main.go -tasks 10

# Run a specific task (by index)
go run main.go -task-id 28

# Run a specific task (by task_id)
go run main.go -task-id "e1fc63a2-da7a-432f-be78-7c4a95598703"

# Specify model
go run main.go -model gpt-4o

# Enable Ralph Loop (outer loop verification)
go run main.go -ralph-loop
```

### Command Line Arguments

| Argument | Default | Description |
|----------|---------|-------------|
| `-dataset` | `../data/gaia_2023_level1_validation.json` | Path to GAIA dataset |
| `-data-dir` | `../data` | Directory containing data files (attachments) |
| `-output` | `../results/trpc-agent-go.json` | Path for output results |
| `-tasks` | `0` | Number of tasks to run (0 means all) |
| `-model` | `deepseek-v3-local-II` | Model name to use |
| `-task-id` | `""` | Run specific task (by index or task_id) |
| `-ralph-loop` | `false` | Enable Ralph Loop outer loop verification |
| `-ralph-max-iterations` | `3` | Max Ralph Loop iterations (only when `-ralph-loop` is enabled) |

### Dataset Description

| File | Description |
|------|-------------|
| `gaia_2023_level1_validation.json` | Level 1 validation set (53 tasks) |
| `gaia_sample.json` | Small sample for quick testing |

> **Note**: Only Level 1 evaluation is currently supported.

## Output Format

Evaluation results are saved in JSON format:

```json
{
  "framework": "trpc-agent-go",
  "total_tasks": 53,
  "correct_count": 30,
  "accuracy": 0.5556,
  "avg_steps": 5.2,
  "avg_time_ms": 45000,
  "avg_tokens": 8500,
  "avg_tool_calls": 3.5,
  "detailed_results": [...]
}
```

## Ralph Loop A/B Results (gpt-5)

This benchmark can be run in two modes:

- **Baseline**: default `react` planner (`-ralph-loop=false`)
- **Ralph Loop (runner-level outer loop)**: wraps the agent with
  `runner.WithRalphLoop(...)` (`-ralph-loop=true`)

When Ralph Loop is enabled, the runner will keep re-running the agent until a
completion condition is met (or `-ralph-max-iterations` is reached). In this
GAIA implementation, the completion condition is a simple verifier: the last
assistant message must contain a `FINAL ANSWER: <answer>` line.

Results (GAIA Level 1 validation, 53 tasks, run date: 2026-01-30):

| Run | Mode | Correct | Accuracy | Avg steps | Avg time | Avg tokens | Avg tool calls |
|-----|------|--------:|---------:|----------:|---------:|-----------:|---------------:|
| A | react | 41/53 | 77.36% | 7.26 | 201.2s | 22,588 | 6.15 |
| B | react + ralph-loop (runner) | 39/53 | 73.58% | 7.85 | 207.5s | 20,516 | 6.83 |

Notes:

- Run B had **2 tasks fail** with transient model API `429 Too Many Requests`
  errors; those tasks are counted as incorrect.
- If you exclude those 2 error tasks and compare only the remaining 51 tasks,
  both runs are **39/51 (76.47%)**. In this setup, enabling Ralph Loop did not
  show a clear accuracy improvement, and slightly increased steps / tool calls.

Raw outputs are stored in `benchmark/gaia/results/` (kept out of git by
default):

- `gpt-5_react_baseline.json`
- `gpt-5_react_ralph_runner.json`

## Agent Capabilities

The evaluation agent has the following capabilities:

- **Web Search**: DuckDuckGo search, web page fetching
- **Knowledge Retrieval**: Wikipedia, arXiv academic search
- **File Processing**: PDF/Excel/CSV/DOCX/TXT document reading
- **Code Execution**: Python code execution (standard library only)
- **Audio Transcription**: Whisper speech-to-text
- **Image Understanding**: Multimodal vision (model-dependent)
- **File Operations**: Directory listing, file search, content search

## Troubleshooting

### LFS Files Not Properly Pulled

```bash
# Check if LFS is installed
git lfs version

# Re-pull all LFS files
git lfs fetch --all
git lfs checkout
```

### Audio Transcription Fails

```bash
# Verify Whisper installation
python3 -c "import whisper; print(whisper.__version__)"

# If not installed, install it
pip install openai-whisper
```

### Attachment Files Not Found

Ensure the `data-dir` argument points to the correct data directory and that LFS files have been fully pulled.

## References

- [GAIA Paper](https://arxiv.org/abs/2311.12983)
- [GAIA Leaderboard](https://huggingface.co/spaces/gaia-benchmark/leaderboard)
- [GAIA Dataset (HuggingFace)](https://huggingface.co/datasets/gaia-benchmark/GAIA)
- [trpc-agent-go GitHub](https://github.com/trpc-group/trpc-agent-go)
