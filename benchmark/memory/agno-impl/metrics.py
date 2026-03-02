"""Evaluation metrics aligned with LoCoMo paper and Go implementation."""

from __future__ import annotations

import math
import re
from collections import Counter
from dataclasses import dataclass, field

# Stop words aligned exactly with Go implementation.
_STOP_WORDS = frozenset({
    "a", "an", "the", "is", "are", "was", "were", "be", "been",
    "being", "have", "has", "had", "do", "does", "did", "will",
    "would", "could", "should", "may", "might", "must", "shall",
    "i", "you", "he", "she", "it", "we", "they", "me", "him",
    "her", "us", "them", "my", "your", "his", "its", "our",
    "their", "this", "that", "these", "those", "and", "or", "but",
    "if", "because", "as", "until", "while", "of", "at", "by",
    "for", "with", "about", "against", "between", "into",
    "through", "during", "before", "after", "above", "below",
    "to", "from", "up", "down", "in", "out", "on", "off", "over",
    "under", "again", "further", "then", "once",
})

# Regex matching Go's [^\w\s] punctuation removal.
_PUNCT_RE = re.compile(r"[^\w\s]")

# Special token produced by some models.
_END_OF_SENTENCE_TOKEN = "<｜end▁of▁sentence｜>"


def _normalize_text(text: str) -> list[str]:
    """Lowercase, remove punctuation, remove stop words, tokenize.

    Aligned with Go normalizeAndTokenize.
    """
    text = text.replace(_END_OF_SENTENCE_TOKEN, " ")
    text = text.lower()
    text = _PUNCT_RE.sub(" ", text)
    tokens = text.split()
    return [t for t in tokens if t and t not in _STOP_WORDS]


@dataclass
class QAMetrics:
    """Metrics for a single QA pair."""
    f1: float = 0.0
    precision: float = 0.0
    recall: float = 0.0
    bleu: float = 0.0


def compute_f1(prediction: str, reference: str) -> QAMetrics:
    """Compute token-level F1, precision, and recall."""
    pred_tokens = _normalize_text(prediction)
    ref_tokens = _normalize_text(reference)
    if not pred_tokens or not ref_tokens:
        if not pred_tokens and not ref_tokens:
            return QAMetrics(f1=1.0, precision=1.0, recall=1.0)
        return QAMetrics()
    pred_counter = Counter(pred_tokens)
    ref_counter = Counter(ref_tokens)
    common = sum((pred_counter & ref_counter).values())
    if common == 0:
        return QAMetrics()
    precision = common / len(pred_tokens)
    recall = common / len(ref_tokens)
    f1 = 2 * precision * recall / (precision + recall)
    return QAMetrics(
        f1=f1,
        precision=precision,
        recall=recall,
    )


def compute_bleu1(prediction: str, reference: str) -> float:
    """Compute BLEU-1 score with brevity penalty."""
    pred_tokens = _normalize_text(prediction)
    ref_tokens = _normalize_text(reference)
    if not pred_tokens:
        if not ref_tokens:
            return 1.0
        return 0.0
    pred_counter = Counter(pred_tokens)
    ref_counter = Counter(ref_tokens)
    clipped = sum((pred_counter & ref_counter).values())
    precision = clipped / len(pred_tokens)
    # Brevity penalty (standard BLEU, aligned with Go).
    bp = 1.0
    if len(pred_tokens) < len(ref_tokens):
        bp = math.exp(1 - len(ref_tokens) / len(pred_tokens))
    return bp * precision


@dataclass
class CategoryMetrics:
    """Aggregated metrics for a category."""
    count: int = 0
    f1: float = 0.0
    bleu: float = 0.0
    llm_score: float = 0.0


class CategoryAggregator:
    """Aggregates metrics by category."""

    def __init__(self) -> None:
        self._counts: dict[str, int] = {}
        self._f1_sums: dict[str, float] = {}
        self._bleu_sums: dict[str, float] = {}
        self._llm_sums: dict[str, float] = {}
        self._llm_counts: dict[str, int] = {}

    def add(
        self,
        category: str,
        f1: float,
        bleu: float,
        llm_score: float = 0.0,
    ) -> None:
        self._counts[category] = self._counts.get(category, 0) + 1
        self._f1_sums[category] = (
            self._f1_sums.get(category, 0.0) + f1
        )
        self._bleu_sums[category] = (
            self._bleu_sums.get(category, 0.0) + bleu
        )
        if llm_score > 0:
            self._llm_sums[category] = (
                self._llm_sums.get(category, 0.0) + llm_score
            )
            self._llm_counts[category] = (
                self._llm_counts.get(category, 0) + 1
            )

    def get_category_metrics(self) -> dict[str, CategoryMetrics]:
        result: dict[str, CategoryMetrics] = {}
        for cat, count in self._counts.items():
            llm_count = self._llm_counts.get(cat, 0)
            result[cat] = CategoryMetrics(
                count=count,
                f1=self._f1_sums[cat] / count if count else 0.0,
                bleu=self._bleu_sums[cat] / count if count else 0.0,
                llm_score=(
                    self._llm_sums.get(cat, 0.0) / llm_count
                    if llm_count else 0.0
                ),
            )
        return result

    def get_overall(self) -> CategoryMetrics:
        total = sum(self._counts.values())
        if total == 0:
            return CategoryMetrics()
        f1 = sum(self._f1_sums.values()) / total
        bleu = sum(self._bleu_sums.values()) / total
        llm_total = sum(self._llm_counts.values())
        llm = (
            sum(self._llm_sums.values()) / llm_total
            if llm_total else 0.0
        )
        return CategoryMetrics(
            count=total,
            f1=f1,
            bleu=bleu,
            llm_score=llm,
        )


# LLM-as-Judge prompt.
_LLM_JUDGE_PROMPT = """You are an expert evaluator. Given a question, \
a reference answer, and a predicted answer, judge whether the \
predicted answer is semantically correct.

Question: {question}
Reference Answer: {reference}
Predicted Answer: {prediction}

Evaluate the predicted answer:
- "correct" if the prediction conveys the same meaning as the \
reference (even if worded differently).
- "incorrect" if it contradicts, is irrelevant, or misses key facts.

Respond with ONLY a JSON object:
{{"correct": true/false, "confidence": 0.0-1.0, "reason": "..."}}"""


def build_llm_judge_prompt(
    question: str,
    reference: str,
    prediction: str,
) -> str:
    return _LLM_JUDGE_PROMPT.format(
        question=question,
        reference=reference,
        prediction=prediction,
    )


def parse_llm_judge_response(response: str) -> float:
    """Parse LLM judge response, return weighted score.

    Aligned with Go implementation: when correct=true, the score
    equals the confidence value (0.0-1.0); when correct=false,
    the score is 0.
    """
    default_confidence = 0.5
    # Try to extract JSON.
    try:
        match = re.search(r"\{[^}]+\}", response)
        if match:
            import json
            obj = json.loads(match.group())
            if obj.get("correct", False):
                confidence = float(
                    obj.get("confidence", default_confidence)
                )
                # Normalize to [0, 1].
                confidence = abs(confidence)
                if confidence > 1:
                    confidence = 1.0
                return confidence
            return 0.0
    except (json.JSONDecodeError, KeyError, ValueError):
        pass
    # Fallback: check for "correct": true.
    if '"correct": true' in response.lower():
        return default_confidence
    return 0.0
