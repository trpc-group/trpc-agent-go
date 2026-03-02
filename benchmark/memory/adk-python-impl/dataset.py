"""LoCoMo dataset loader, aligned with the Go implementation."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

# Category mapping: int -> string (aligned with LoCoMo paper).
_CATEGORY_MAP = {
    1: "single-hop",
    2: "multi-hop",
    3: "temporal",
    4: "open-domain",
    5: "adversarial",
}

_ADVERSARIAL_ANSWER_FALLBACK = "The information is not available."


@dataclass
class Turn:
    speaker: str
    text: str


@dataclass
class Session:
    session_id: str
    session_date: str
    turns: list[Turn]
    observation: str = ""
    summary: str = ""


@dataclass
class QAItem:
    question_id: str
    question: str
    answer: str
    category: str
    evidence: list[str] = field(default_factory=list)


@dataclass
class LoCoMoSample:
    sample_id: str
    speakers: list[str]
    conversation: list[Session]
    qa: list[QAItem]

    def build_full_conversation(self) -> str:
        """Build the full conversation transcript."""
        parts: list[str] = []
        for sess in self.conversation:
            if sess.session_date:
                header = (
                    f"[Session {sess.session_id}"
                    f" - {sess.session_date}]"
                )
            else:
                header = f"[Session {sess.session_id}]"
            parts.append(header)
            for t in sess.turns:
                parts.append(f"{t.speaker}: {t.text}")
            parts.append("")
        return "\n".join(parts)


def load_samples(data_dir: str, filename: str) -> list[LoCoMoSample]:
    """Load LoCoMo-10 dataset samples."""
    path = Path(data_dir) / filename
    with open(path, encoding="utf-8") as f:
        raw = json.load(f)
    if isinstance(raw, list):
        return _parse_locomo10_samples(raw)
    raise ValueError(f"Unsupported dataset format in {path}")


def _parse_locomo10_samples(
    raw_list: list[dict[str, Any]],
) -> list[LoCoMoSample]:
    """Parse LoCoMo-10 raw format."""
    results: list[LoCoMoSample] = []
    for idx, raw in enumerate(raw_list):
        sample_id = raw.get("sample_id", f"locomo10_{idx + 1}")
        conv_raw = raw.get("conversation", raw)
        summary_raw = raw.get("session_summary", raw)

        speakers = _parse_speakers(conv_raw)
        if not speakers:
            speakers = _infer_speakers(conv_raw)
        sessions = _parse_sessions(conv_raw, speakers, summary_raw)
        qa_items = _parse_qa(raw.get("qa", []), sample_id)
        results.append(LoCoMoSample(
            sample_id=sample_id,
            speakers=speakers,
            conversation=sessions,
            qa=qa_items,
        ))
    return results


def _parse_speakers(raw: dict) -> list[str]:
    """Extract speaker_a and speaker_b."""
    speakers: list[str] = []
    for key in ("speaker_a", "speaker_b"):
        val = raw.get(key)
        if isinstance(val, str) and val:
            speakers.append(val)
    return speakers


def _infer_speakers(raw: dict) -> list[str]:
    """Infer speakers from session turn data."""
    seen: set[str] = set()
    out: list[str] = []
    for idx in _extract_session_indexes(raw):
        key = f"session_{idx}"
        turns = raw.get(key, [])
        if not isinstance(turns, list):
            continue
        for t in turns:
            if not isinstance(t, dict):
                continue
            spk = t.get("speaker", "")
            if spk and spk not in seen:
                seen.add(spk)
                out.append(spk)
    return out


def _extract_session_indexes(raw: dict) -> list[int]:
    """Extract sorted session indexes from keys like session_N."""
    indexes: list[int] = []
    pat = re.compile(r"^session_(\d+)$")
    for key in raw:
        m = pat.match(key)
        if m:
            indexes.append(int(m.group(1)))
    indexes.sort()
    return indexes


def _parse_sessions(
    raw: dict,
    speakers: list[str],
    summary_raw: dict,
) -> list[Session]:
    """Parse sessions from the raw conversation dict."""
    sessions: list[Session] = []
    for idx in _extract_session_indexes(raw):
        key = f"session_{idx}"
        turns_raw = raw.get(key, [])
        if not isinstance(turns_raw, list):
            continue
        turns: list[Turn] = []
        first_dia_id = ""
        for t in turns_raw:
            if not isinstance(t, dict):
                continue
            text = t.get("text", "")
            if not text:
                continue
            turns.append(Turn(
                speaker=t.get("speaker", ""),
                text=text,
            ))
            if not first_dia_id:
                dia_id = t.get("dia_id", "")
                if dia_id:
                    first_dia_id = dia_id.split(":")[0]
        if not turns:
            continue
        session_id = first_dia_id or f"session_{idx}"
        date_key = f"session_{idx}_date_time"
        session_date = raw.get(date_key, "")
        if not isinstance(session_date, str):
            session_date = str(session_date)
        summary_key = f"session_{idx}_summary"
        summary = summary_raw.get(summary_key, "")
        if not isinstance(summary, str):
            summary = ""
        sessions.append(Session(
            session_id=session_id,
            session_date=session_date,
            turns=turns,
            summary=summary,
        ))
    return sessions


def _parse_qa(
    qa_raw: list[dict],
    sample_id: str,
) -> list[QAItem]:
    """Parse QA items from raw list."""
    items: list[QAItem] = []
    for idx, qa in enumerate(qa_raw):
        cat_int = qa.get("category", 0)
        category = _CATEGORY_MAP.get(cat_int, f"category_{cat_int}")
        answer = _decode_answer(
            qa.get("answer"),
            qa.get("adversarial_answer"),
            category,
        )
        evidence = _normalize_evidence(qa.get("evidence", []))
        items.append(QAItem(
            question_id=f"{sample_id}_q_{idx + 1}",
            question=qa.get("question", ""),
            answer=answer,
            category=category,
            evidence=evidence,
        ))
    return items


def _decode_answer(
    answer: Any,
    adversarial: Any,
    category: str,
) -> str:
    """Decode answer field (can be str, int, float, bool)."""
    if category == "adversarial":
        decoded = _to_str(answer)
        return decoded if decoded else _ADVERSARIAL_ANSWER_FALLBACK
    decoded = _to_str(answer)
    if decoded:
        return decoded
    return _to_str(adversarial)


def _to_str(val: Any) -> str:
    if val is None:
        return ""
    if isinstance(val, str):
        return val
    if isinstance(val, bool):
        return "true" if val else "false"
    if isinstance(val, (int, float)):
        return str(val)
    return ""


def _normalize_evidence(evidence: list) -> list[str]:
    """Normalize evidence IDs (keep session part only)."""
    result: list[str] = []
    for item in evidence:
        if not isinstance(item, str):
            continue
        parts = item.split(":")
        if parts and parts[0]:
            result.append(parts[0])
    return result
