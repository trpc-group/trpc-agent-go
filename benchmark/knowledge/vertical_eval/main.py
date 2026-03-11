#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
Main entry point for vertical evaluation of trpc-agent-go knowledge system.

Runs a suite of experiments with different configurations (hybrid weights)
against the HuggingFace dataset, and generates a comparison report.

Usage:
    python -m vertical_eval.main --suite hybrid_weight
    python -m vertical_eval.main --suite all
    python -m vertical_eval.main --suite hybrid_weight --pg-table my_table
    python -m vertical_eval.main --suite hybrid_rrf --skip-load --pg-table veval_hw_rrf
"""

import argparse
import json
import os
import sys
import time

EVAL_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, EVAL_ROOT)

from vertical_eval.config import EXPERIMENT_SUITES, ExperimentConfig
from vertical_eval.runner import run_single_experiment
from vertical_eval.report import generate_report


def main():
    parser = argparse.ArgumentParser(
        description="Vertical evaluation of trpc-agent-go knowledge system"
    )
    parser.add_argument(
        "--suite",
        choices=list(EXPERIMENT_SUITES.keys()) + ["all"],
        default="hybrid_weight",
        help="Experiment suite to run (default: hybrid_weight)",
    )
    parser.add_argument(
        "--k",
        type=int,
        default=None,
        help="Override retrieval k for all experiments (default: use per-experiment config)",
    )
    parser.add_argument(
        "--skip-load",
        action="store_true",
        help="Skip document loading (assume already loaded)",
    )
    parser.add_argument(
        "--base-port",
        type=int,
        default=9000,
        help="Base port for Go service instances (default: 9000)",
    )
    parser.add_argument(
        "--output-dir",
        type=str,
        default=None,
        help="Output directory for results (default: vertical_eval/results/<suite>)",
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=30,
        help="Number of concurrent workers for RAGAS evaluation (default: 30)",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=600,
        help="Timeout in seconds for evaluation (default: 600)",
    )
    parser.add_argument(
        "--pg-table",
        type=str,
        default=None,
        help="Override PGVector table name for all experiments (default: use per-experiment config)",
    )
    parser.add_argument(
        "--experiments",
        type=str,
        nargs="*",
        default=None,
        help="Run only specific experiments by name (e.g. --experiments hybrid_v90_t10 hybrid_v50_t50)",
    )
    args = parser.parse_args()

    # Determine which suites to run
    if args.suite == "all":
        suites_to_run = list(EXPERIMENT_SUITES.keys())
    else:
        suites_to_run = [args.suite]

    for suite_name in suites_to_run:
        experiments = EXPERIMENT_SUITES[suite_name]

        # Filter experiments if specified
        if args.experiments:
            experiments = [e for e in experiments if e.name in args.experiments]
            if not experiments:
                print(f"No matching experiments found in suite '{suite_name}' for: {args.experiments}")
                continue

        # Override retrieval_k if specified
        if args.k is not None:
            for exp in experiments:
                exp.retrieval_k = args.k

        # Override pg_table if specified
        if args.pg_table is not None:
            for exp in experiments:
                exp.pg_table = args.pg_table

        # Setup output directory
        base_output_dir = args.output_dir or os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "results", suite_name
        )
        
        # Add timestamp to output directory to prevent overwriting
        timestamp = time.strftime("%Y%m%d_%H%M%S")
        output_dir = f"{base_output_dir}_{timestamp}"
        os.makedirs(output_dir, exist_ok=True)

        print(f"\n{'#'*70}")
        print(f"# Suite: {suite_name}")
        print(f"# Experiments: {len(experiments)}")
        print(f"# Output: {output_dir}")
        print(f"{'#'*70}")

        # Load dataset (HuggingFace)
        from dataset import create_dataset
        dataset = create_dataset("huggingface")
        qa_items = dataset.load_qa_items()
        print(f"Loaded {len(qa_items)} QA items from HuggingFace dataset")

        # Load documents once
        doc_dir = dataset.load_documents(force_reload=False)
        file_count = len([f for f in os.listdir(doc_dir) if os.path.isfile(os.path.join(doc_dir, f))])
        print(f"Document directory: {doc_dir} ({file_count} files)")

        # Initialize evaluator
        from evaluator.ragas.evaluator import RAGASEvaluator
        evaluator = RAGASEvaluator(max_workers=args.workers, timeout=args.timeout)

        # Run experiments
        all_results = []
        suite_start = time.time()
        loaded_tables = set()

        # If --skip-load is used, assume ALL tables in this suite are already loaded
        if args.skip_load:
            for exp in experiments:
                if exp.pg_table:
                    loaded_tables.add(exp.pg_table)

        for i, config in enumerate(experiments):
            print(f"\n>>> Experiment {i+1}/{len(experiments)}: {config.name}")

            should_skip_load = args.skip_load
            if config.pg_table and config.pg_table in loaded_tables:
                print(f"  Table '{config.pg_table}' already loaded, skipping load")
                should_skip_load = True

            try:
                result = run_single_experiment(
                    config=config,
                    qa_items=qa_items,
                    doc_dir=doc_dir,
                    evaluator=evaluator,
                    base_port=args.base_port,
                    skip_load=should_skip_load,
                    output_dir=output_dir,
                )
                if config.pg_table:
                    loaded_tables.add(config.pg_table)
                all_results.append(result)
            except Exception as e:
                print(f"  FAILED: {e}")
                all_results.append({"name": config.name, "error": str(e)})

        suite_time = time.time() - suite_start

        # Save combined results
        combined_path = os.path.join(output_dir, f"_combined_{suite_name}.json")
        with open(combined_path, "w", encoding="utf-8") as f:
            json.dump(
                {
                    "suite": suite_name,
                    "total_time_seconds": round(suite_time, 2),
                    "experiments": all_results,
                },
                f,
                indent=2,
                ensure_ascii=False,
            )
        print(f"\nCombined results: {combined_path}")

        # Generate report
        report = generate_report(output_dir, suite_name)
        print(f"\nSuite '{suite_name}' complete in {suite_time:.0f}s")
        print(report)
        
        # Save Python console output (report) to the output directory as well
        report_log_path = os.path.join(output_dir, f"report_{suite_name}.log")
        with open(report_log_path, "w", encoding="utf-8") as f:
            f.write(f"Suite '{suite_name}' complete in {suite_time:.0f}s\n\n")
            f.write(report)
        print(f"Report saved to: {report_log_path}")


if __name__ == "__main__":
    main()
