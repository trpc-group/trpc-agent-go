#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
Generate comparison reports from vertical evaluation results.
"""

import json
import os
import re
from typing import Any, Dict, List


def parse_ragas_result(result_str: str) -> Dict[str, float]:
    """Parse RAGAS result string into a metrics dict."""
    metrics = {}
    if not isinstance(result_str, str):
        return metrics
    patterns = {
        "faithfulness": r"Faithfulness:\s+([\d.]+)",
        "answer_relevancy": r"Answer Relevancy:\s+([\d.]+)",
        "answer_correctness": r"Answer Correctness:\s+([\d.]+)",
        "answer_similarity": r"Answer Similarity:\s+([\d.]+)",
        "context_precision": r"Context Precision:\s+([\d.]+)",
        "context_recall": r"Context Recall:\s+([\d.]+)",
        "context_entity_recall": r"Context Entity Recall:\s+([\d.]+)",
    }
    for key, pattern in patterns.items():
        match = re.search(pattern, result_str)
        if match:
            metrics[key] = float(match.group(1))
    return metrics


def load_results(output_dir: str) -> List[Dict[str, Any]]:
    """Load all experiment result JSON files from a directory."""
    results = []
    for filename in sorted(os.listdir(output_dir)):
        if not filename.endswith(".json"):
            continue
        filepath = os.path.join(output_dir, filename)
        with open(filepath, "r", encoding="utf-8") as f:
            data = json.load(f)
        if "name" in data:
            data["_metrics"] = parse_ragas_result(data.get("result", ""))
            results.append(data)
    return results


def generate_markdown_report(results: List[Dict[str, Any]], suite_name: str) -> str:
    """Generate a markdown comparison table from experiment results."""
    if not results:
        return "No results to report.\n"

    lines = []
    lines.append(f"# Vertical Evaluation Report: {suite_name}\n")

    # Summary table
    metric_keys = [
        "faithfulness",
        "answer_relevancy",
        "answer_correctness",
        "answer_similarity",
        "context_precision",
        "context_recall",
        "context_entity_recall",
    ]

    header = "| Experiment | " + " | ".join(k.replace("_", " ").title() for k in metric_keys) + " | QA Time (avg) |"
    separator = "|" + "|".join(["---"] * (len(metric_keys) + 2)) + "|"
    lines.append(header)
    lines.append(separator)

    for r in results:
        metrics = r.get("_metrics", {})
        name = r["name"]
        cells = [name]
        for k in metric_keys:
            val = metrics.get(k)
            cells.append(f"{val:.4f}" if val is not None else "N/A")
        qa_avg = r.get("timing", {}).get("qa_avg_seconds", 0)
        cells.append(f"{qa_avg:.1f}s")
        lines.append("| " + " | ".join(cells) + " |")

    lines.append("")

    # Config details
    lines.append("## Experiment Configurations\n")
    for r in results:
        cfg = r.get("config", {})
        lines.append(f"### {r['name']}")
        lines.append(f"- **Description**: {r.get('description', 'N/A')}")
        lines.append(f"- **Hybrid weights**: vector={cfg.get('hybrid_vector_weight', 'N/A')}, text={cfg.get('hybrid_text_weight', 'N/A')}")
        lines.append(f"- **Retrieval k**: {cfg.get('retrieval_k', 'N/A')}")
        lines.append(f"- **Samples**: {r.get('samples_count', 0)}, Errors: {r.get('errors_count', 0)}")
        timing = r.get("timing", {})
        lines.append(f"- **Timing**: QA total={timing.get('qa_total_seconds', 0)}s, eval={timing.get('eval_seconds', 0)}s")
        lines.append("")

    return "\n".join(lines)


def generate_report(output_dir: str, suite_name: str = "vertical_eval") -> str:
    """Load results and generate a markdown report."""
    results = load_results(output_dir)
    report = generate_markdown_report(results, suite_name)

    report_path = os.path.join(output_dir, f"REPORT_{suite_name}.md")
    with open(report_path, "w", encoding="utf-8") as f:
        f.write(report)
    print(f"Report saved: {report_path}")
    return report
