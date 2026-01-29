# GAIA Benchmark Example (Skills + Files)

This example runs a small GAIA benchmark dataset using `trpc-agent-go`,
including tasks that require:

- Audio transcription (via the `whisper` skill)
- Image OCR (via the `ocr` skill)
- Local file access (restricted to `-data-dir`)

The goal is to show how to combine:

- `skill_load` / `skill_run` (run scripts inside an isolated workspace)
- File tools (list/search files under `-data-dir`)
- `workspace://...` references to pass skill outputs across tools

## What You Get

- A runnable GAIA evaluation driver (`main.go`)
- A `./data/` placeholder (gitignored) for GAIA JSON/files
- Two example skills under `./skills` (`whisper`, `ocr`)

## Prerequisites

### Go + Model Endpoint

- Go 1.24+
- An OpenAI-compatible model endpoint
  - `OPENAI_API_KEY` must be set
  - `OPENAI_BASE_URL` is optional (set it when not using OpenAI)

### Python (Optional, for Audio/Image Tasks)

`skill_run` executes scripts with `python3` from your `PATH`. Install
dependencies into the same Python environment that `python3` points to.

1) Create a Python environment (pick one)

- `venv`:

  ```bash
  cd examples/skill
  python3 -m venv .venv
  source .venv/bin/activate
  python3 -m pip install -U pip
  ```

- `conda`:

  ```bash
  conda create -n trpc-skill python=3.11
  conda activate trpc-skill
  python3 -m pip install -U pip
  ```

2) Install Python packages

```bash
python3 -m pip install openai-whisper pillow pytesseract
```

3) Install system dependencies

- `whisper` needs `ffmpeg`
- `ocr` needs the `tesseract` binary

Common installs:

- macOS (Homebrew): `brew install ffmpeg tesseract`
- Ubuntu/Debian: `sudo apt-get install ffmpeg tesseract-ocr`

4) Quick sanity checks

```bash
which python3
python3 -c "import whisper; print('whisper ok')"
python3 -c "import pytesseract; from PIL import Image; print('ocr ok')"
ffmpeg -version | head -n 1
tesseract --version | head -n 1
```

See the skill docs:

- `skills/whisper/SKILL.md`
- `skills/ocr/SKILL.md`

## Data Layout

This example expects (you need to download/populate these files locally):

- Dataset JSON: `./data/gaia_2023_level1_validation.json`
- Attachments: `./data/2023/validation/*`

`./data` is intentionally gitignored, so you can put benchmark data
there without accidentally committing it.

Quick sanity check after you prepare the data:

```bash
cd examples/skill
ls -la data/gaia_2023_level1_validation.json
ls -la data/2023/validation | head
```

### Downloading the Full Dataset (Optional)

GAIA is gated on Hugging Face. To download it, you must request access
and create a Hugging Face access token.

The script checks `HF_TOKEN`, `HUGGINGFACE_TOKEN`, and
`HUGGINGFACE_HUB_TOKEN`.

The downloader uses only the Python standard library (no extra pip
packages needed).

From the repo root:

```bash
export HF_TOKEN="hf_..."
python3 examples/skill/scripts/download_gaia_2023_level1_validation.py
```

Or from `examples/skill`:

```bash
cd examples/skill
export HF_TOKEN="hf_..."
python3 scripts/download_gaia_2023_level1_validation.py
```

This downloads only the JSON metadata file by default:

- `examples/skill/data/gaia_2023_level1_validation.json`

To also download attachment files referenced by `file_path`, run:

```bash
python3 scripts/download_gaia_2023_level1_validation.py --with-files
```

Attachments are saved under `examples/skill/data/` (for example,
`examples/skill/data/2023/validation/*.mp3`).

## Running

From the repo root:

```bash
cd examples/skill
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
go run . \
  -data-dir ./data \
  -dataset ./data/gaia_2023_level1_validation.json \
  -model "your-model-name" \
  -task-id 31
```

Notes:

- `-task-id` accepts either a task UUID or a 1-based index (e.g. `31`).
- Results are written to `../results/trpc-agent-go.json` by default.
- Skill workspaces are created under `./skill_workspaces/` (safe to delete).

## How Skill Outputs Work (Important)

`skill_run` executes in an isolated workspace. Files written there are not
automatically visible to normal file tools unless they are exported.

Recommended patterns:

- When calling `skill_run`, write outputs under `out/` and set
  `output_files` so the tool returns text file contents inline.
  Non-text files (like images) are returned as metadata only.
- When passing an output file to other tools, use `output_files[*].ref`
  (a `workspace://...` reference), not a host filesystem path.
