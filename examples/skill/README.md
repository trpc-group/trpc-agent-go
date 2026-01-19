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
- A small dataset file and a few sample attachments under `./data`
- Two example skills under `./skills` (`whisper`, `ocr`)

## Prerequisites

- Go 1.24+
- An OpenAI-compatible model endpoint:
  - `OPENAI_API_KEY` must be set
  - `OPENAI_BASE_URL` is optional (set it when not using OpenAI)

If you want to run audio/image tasks locally, you also need:

- Python 3.8+
- For `whisper`: `openai-whisper` and `ffmpeg`
- For `ocr`: `pytesseract`, `Pillow`, and the `tesseract` binary

See the skill docs:

- `skills/whisper/SKILL.md`
- `skills/ocr/SKILL.md`

## Data Layout

This example expects:

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

If you have access to the GAIA dataset on Hugging Face, you can try:

```bash
python3 scripts/download_gaia_2023_level1_validation.py
```

This downloads only the JSON metadata by default. To also download
attachment files referenced by `file_path`, run:

```bash
python3 scripts/download_gaia_2023_level1_validation.py --with-files
```

If the dataset is gated/private, you will need a Hugging Face token (the
script checks `HF_TOKEN`, `HUGGINGFACE_TOKEN`, `HUGGINGFACE_HUB_TOKEN`).

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
  `output_files` so the tool returns file contents inline.
- When passing an output file to other tools, use `output_files[*].ref`
  (a `workspace://...` reference), not a host filesystem path.
