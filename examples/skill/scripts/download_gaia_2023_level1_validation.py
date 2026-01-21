#!/usr/bin/env python3
"""
Download GAIA 2023 level1 validation split from Hugging Face (gated)
into examples/skill/data and generate a JSON file compatible with
examples/skill/main.go.

Why a token is required:
GAIA is gated on Hugging Face to prevent dataset scraping. You must:
1) Request access on the dataset page
2) Accept the gating terms
3) Create a Hugging Face access token

Usage:
  export HF_TOKEN="hf_..."

  # Default: only generate the JSON metadata file (no attachments).
  python3 examples/skill/scripts/download_gaia_2023_level1_validation.py

  # Optional: also download attachment files referenced by file_path.
  python3 examples/skill/scripts/download_gaia_2023_level1_validation.py \
    --with-files

Then run the Go example from examples/skill:
  go run . -data-dir ./data -dataset ./data/gaia_2023_level1_validation.json
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

from urllib.error import HTTPError


HF_DATASET = "gaia-benchmark/GAIA"
HF_CONFIG = "2023_level1"
HF_SPLIT = "validation"

DEFAULT_DATASET_JSON = "gaia_2023_level1_validation.json"


class DownloadError(RuntimeError):
    pass


def _hf_token() -> str:
    for k in ("HF_TOKEN", "HUGGINGFACE_TOKEN", "HUGGINGFACE_HUB_TOKEN"):
        v = os.environ.get(k, "").strip()
        if v:
            return v
    raise DownloadError(
        "Missing Hugging Face token. Set HF_TOKEN (or "
        "HUGGINGFACE_TOKEN / HUGGINGFACE_HUB_TOKEN)."
    )


def _http_get_json(url: str, token: Optional[str] = None) -> Any:
    headers = {"User-Agent": "trpc-agent-go/examples-skill"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            data = resp.read()
    except HTTPError as e:
        body = ""
        try:
            body = e.read().decode("utf-8", errors="replace")
        except Exception:
            body = ""
        msg = f"HTTP {e.code} for {url}"
        if body:
            msg += f": {body.strip()}"
        raise DownloadError(msg) from e
    return json.loads(data.decode("utf-8"))


def _http_download(url: str, dst: Path, token: str, force: bool) -> None:
    if dst.exists() and not force and dst.stat().st_size > 0:
        return
    dst.parent.mkdir(parents=True, exist_ok=True)
    headers = {"User-Agent": "trpc-agent-go/examples-skill"}
    headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            data = resp.read()
    except HTTPError as e:
        body = ""
        try:
            body = e.read().decode("utf-8", errors="replace")
        except Exception:
            body = ""
        msg = f"HTTP {e.code} downloading {url} -> {dst}"
        if body:
            msg += f": {body.strip()}"
        raise DownloadError(msg) from e
    dst.write_bytes(data)


def _dataset_api() -> Tuple[str, List[str]]:
    url = "https://huggingface.co/api/datasets/" + HF_DATASET
    obj = _http_get_json(url, token=None)
    sha = str(obj.get("sha") or "").strip()
    if not sha:
        raise DownloadError("Failed to read dataset sha from HF API.")
    siblings = obj.get("siblings") or []
    files: List[str] = []
    for s in siblings:
        if isinstance(s, dict) and isinstance(s.get("rfilename"), str):
            files.append(s["rfilename"])
    return sha, files


def _fetch_rows(token: str) -> List[Dict[str, Any]]:
    rows: List[Dict[str, Any]] = []
    offset = 0
    length = 100
    quoted_dataset = urllib.parse.quote(HF_DATASET, safe="")
    quoted_config = urllib.parse.quote(HF_CONFIG, safe="")
    quoted_split = urllib.parse.quote(HF_SPLIT, safe="")
    while True:
        url = (
            "https://datasets-server.huggingface.co/rows"
            f"?dataset={quoted_dataset}"
            f"&config={quoted_config}"
            f"&split={quoted_split}"
            f"&offset={offset}"
            f"&length={length}"
        )
        obj = _http_get_json(url, token=token)
        chunk = obj.get("rows") or []
        if not isinstance(chunk, list) or not chunk:
            break
        for it in chunk:
            if isinstance(it, dict):
                rows.append(it)
        offset += len(chunk)
        total = obj.get("num_rows_total")
        if isinstance(total, int) and offset >= total:
            break
        if len(chunk) < length:
            break
        time.sleep(0.1)
    if not rows:
        raise DownloadError(
            "No rows returned. Ensure your token has access and you "
            "accepted the gating terms on Hugging Face."
        )
    return rows


def _first_value(row: Dict[str, Any], keys: Sequence[str]) -> Any:
    for k in keys:
        if k in row:
            v = row.get(k)
            if v is not None:
                return v
    return ""


def _as_str(v: Any) -> str:
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    return str(v)


def _as_paths(v: Any) -> List[str]:
    if v is None:
        return []
    if isinstance(v, str):
        s = v.strip()
        if not s:
            return []
        return [s]
    if isinstance(v, list):
        out: List[str] = []
        for it in v:
            out.extend(_as_paths(it))
        return out
    if isinstance(v, dict):
        for k in ("file_path", "path", "rfilename", "name", "file_name"):
            if k in v and v[k]:
                return _as_paths(v[k])
    return []


def _build_tasks(rows: List[Dict[str, Any]]) -> Tuple[List[Dict[str, Any]], List[str]]:
    tasks: List[Dict[str, Any]] = []
    file_paths: List[str] = []

    for item in rows:
        row = item.get("row")
        if not isinstance(row, dict):
            continue

        task_id = _as_str(_first_value(row, ("task_id", "id", "taskID")))
        question = _as_str(_first_value(row, ("Question", "question")))
        level = _as_str(_first_value(row, ("Level", "level")))
        final_answer = _as_str(
            _first_value(
                row,
                (
                    "Final answer",
                    "final_answer",
                    "final answer",
                    "answer",
                ),
            )
        )
        file_path_val = _first_value(row, ("file_path", "file", "attachment"))
        paths = _as_paths(file_path_val)
        file_path = paths[0] if paths else ""
        file_name = _as_str(_first_value(row, ("file_name", "filename")))
        if not file_name and file_path:
            file_name = Path(file_path).name

        tasks.append(
            {
                "task_id": task_id,
                "Question": question,
                "Level": level,
                "Final answer": final_answer,
                "file_name": file_name,
                "file_path": file_path,
            }
        )
        if file_path:
            file_paths.append(file_path)

    if not tasks:
        raise DownloadError("Failed to build tasks from dataset rows.")
    return tasks, sorted(set(file_paths))


def _resolve_url(sha: str, rfilename: str) -> str:
    # Keep slashes unescaped so HF understands nested paths.
    quoted = urllib.parse.quote(rfilename, safe="/")
    return (
        "https://huggingface.co/datasets/"
        + HF_DATASET
        + "/resolve/"
        + sha
        + "/"
        + quoted
    )


def _parse_args(argv: Sequence[str]) -> argparse.Namespace:
    base = Path(__file__).resolve().parents[1]
    default_data_dir = base / "data"
    default_dataset = default_data_dir / DEFAULT_DATASET_JSON

    p = argparse.ArgumentParser()
    p.add_argument(
        "--data-dir",
        default=str(default_data_dir),
        help="Where to place GAIA files (default: examples/skill/data).",
    )
    p.add_argument(
        "--dataset-json",
        default=str(default_dataset),
        help="Output JSON path (default: data/gaia_2023_level1_validation.json).",
    )
    p.set_defaults(skip_files=True)
    p.add_argument(
        "--skip-files",
        action="store_true",
        dest="skip_files",
        help="Only write the JSON metadata; do not download attachments "
        "(default).",
    )
    p.add_argument(
        "--with-files",
        action="store_false",
        dest="skip_files",
        help="Also download attachment files referenced by file_path.",
    )
    p.add_argument(
        "--force",
        action="store_true",
        help="Re-download files even if they already exist.",
    )
    return p.parse_args(argv)


def main(argv: Sequence[str]) -> int:
    args = _parse_args(argv)

    try:
        token = _hf_token()
        sha, _ = _dataset_api()

        rows = _fetch_rows(token)
        tasks, file_paths = _build_tasks(rows)

        data_dir = Path(args.data_dir).resolve()
        dataset_json = Path(args.dataset_json).resolve()
        if not dataset_json.is_absolute():
            dataset_json = data_dir / dataset_json

        data_dir.mkdir(parents=True, exist_ok=True)
        dataset_json.parent.mkdir(parents=True, exist_ok=True)
        dataset_json.write_text(
            json.dumps(tasks, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )
        print(f"Wrote dataset JSON: {dataset_json}")

        if not args.skip_files and file_paths:
            print(f"Downloading {len(file_paths)} attachment files...")
            for i, fp in enumerate(file_paths, 1):
                dst = data_dir / fp
                url = _resolve_url(sha, fp)
                _http_download(url, dst, token=token, force=args.force)
                print(f"[{i}/{len(file_paths)}] {fp}")

        print("\nNext:")
        print("  cd examples/skill")
        print(
            "  go run . -data-dir ./data "
            "-dataset ./data/gaia_2023_level1_validation.json "
            "-tasks 1"
        )
        return 0
    except DownloadError as e:
        print("Error:", e, file=sys.stderr)
        print(
            "Dataset page: https://huggingface.co/datasets/gaia-benchmark/GAIA",
            file=sys.stderr,
        )
        return 2
    except Exception as e:
        print("Unexpected error:", e, file=sys.stderr)
        return 3


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
