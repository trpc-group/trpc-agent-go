"""
Main entry point for RAG evaluation with different evaluators.
"""

import argparse
import json
import os
import platform
import time
from typing import Any, Dict, List, Optional

from dataset.base import BaseDataset
from knowledge_system.base import KnowledgeBase
from evaluator.base import Evaluator, EvaluationSample


def _normalize_query(query: Any) -> Optional[str]:
    """Normalize a query value to a non-empty string."""
    if isinstance(query, str):
        normalized = query.strip()
        if normalized:
            return normalized
    return None


def _extract_query_from_tool_call_arguments(arguments: Any) -> Optional[str]:
    """Extract query text from tool-call arguments."""
    if isinstance(arguments, dict):
        return _normalize_query(arguments.get("query"))

    if isinstance(arguments, str):
        argument_text = arguments.strip()
        if not argument_text:
            return None
        try:
            payload = json.loads(argument_text)
        except json.JSONDecodeError:
            return None
        if isinstance(payload, dict):
            return _normalize_query(payload.get("query"))
    return None


def _dedupe_queries(queries: List[str]) -> List[str]:
    """Dedupe queries while preserving original order."""
    seen = set()
    deduped = []
    for query in queries:
        if query in seen:
            continue
        seen.add(query)
        deduped.append(query)
    return deduped


def extract_retrieval_queries(
    question: str,
    search_results: List[Any],
    fallback_trace: Optional[dict] = None,
) -> List[str]:
    """Extract retrieval queries from search-result metadata or agent trace."""
    queries: List[str] = []

    # CrewAI path: tool query is attached to per-result metadata.
    for result in search_results:
        metadata = getattr(result, "metadata", None)
        if not isinstance(metadata, dict):
            continue
        normalized = _normalize_query(metadata.get("tool_query"))
        if normalized:
            queries.append(normalized)

    # tRPC-Agent-Go path: tool query is embedded in trace.tool_calls[*].arguments.
    trace: Optional[dict] = None
    for result in search_results:
        candidate = getattr(result, "trace", None)
        if isinstance(candidate, dict):
            trace = candidate
            break

    if trace is None and isinstance(fallback_trace, dict):
        trace = fallback_trace

    if isinstance(trace, dict):
        tool_queries = trace.get("tool_queries", [])
        if isinstance(tool_queries, list):
            for query in tool_queries:
                normalized = _normalize_query(query)
                if normalized:
                    queries.append(normalized)

        tool_calls = trace.get("tool_calls", [])
        if isinstance(tool_calls, list):
            for call in tool_calls:
                if not isinstance(call, dict):
                    continue
                extracted = _extract_query_from_tool_call_arguments(call.get("arguments"))
                if extracted:
                    queries.append(extracted)

    return _dedupe_queries(queries)


def build_run_manifest(
    kb_name: str,
    evaluator_name: str,
    dataset_name: str,
    retrieval_k: int,
    skip_load: bool,
) -> Dict[str, Any]:
    """Build a manifest capturing all key configuration for reproducibility."""
    from util import get_config
    config = get_config()
    return {
        "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "platform": platform.platform(),
        "python_version": platform.python_version(),
        "knowledge_base": kb_name,
        "evaluator": evaluator_name,
        "dataset": dataset_name,
        "model_name": config.get("model_name", ""),
        "eval_model_name": config.get("eval_model_name", ""),
        "embedding_model": config.get("embedding_model", ""),
        "retrieval_k": retrieval_k,
        "skip_load": skip_load,
    }


def run_evaluation(
    kb: KnowledgeBase,
    dataset: BaseDataset,
    evaluator: Evaluator,
    retrieval_k: int = 4,
    skip_load: bool = False,
    full_log: bool = True,
    output_file: Optional[str] = None,
    kb_name: str = "unknown",
    evaluator_name: str = "unknown",
    dataset_name: str = "unknown",
) -> str:
    """
    Run RAG evaluation with specified evaluator.

    Args:
        kb: Knowledge base instance implementing KnowledgeBase interface.
        dataset: Dataset instance implementing BaseDataset interface.
        evaluator: Evaluator instance implementing Evaluator interface.
        retrieval_k: Number of documents to retrieve per query.
        skip_load: If True, skip loading documents into knowledge base.
        full_log: If True, print full answer results for each question.
        output_file: Optional path to save evaluation results as JSON.
        kb_name: Name of the knowledge base implementation (for manifest).
        evaluator_name: Name of the evaluator (for manifest).
        dataset_name: Name of the dataset (for manifest).

    Returns:
        Evaluation results as formatted string.
    """
    print("=== RAG Evaluation ===\n")

    manifest = build_run_manifest(
        kb_name=kb_name,
        evaluator_name=evaluator_name,
        dataset_name=dataset_name,
        retrieval_k=retrieval_k,
        skip_load=skip_load,
    )
    print("📋 Run Manifest:")
    for k, v in manifest.items():
        print(f"   {k}: {v}")
    print()

    # Step 1: Load QA items
    print("1. Loading QA items...")
    qa_items = dataset.load_qa_items()
    print(f"   Loaded {len(qa_items)} QA items.\n")

    # Step 2: Load documents if needed
    if skip_load:
        print("2. Skipping document loading (--skip-load enabled)...\n")
    else:
        print("2. Loading documents...")
        doc_dir = dataset.load_documents(force_reload=True)

        file_paths = []
        for filename in sorted(os.listdir(doc_dir)):
            filepath = os.path.join(doc_dir, filename)
            if os.path.isfile(filepath):
                file_paths.append(filepath)

        print(f"   Found {len(file_paths)} documents (sorted for reproducibility).")

        print("3. Building knowledge base...")
        kb.load(file_paths)
        print("   Knowledge base built.\n")

    # Step 3: Run Q&A
    print("4. Running Q&A with fresh sessions...")
    samples = []
    errors = []
    qa_times = []
    sample_debug = []
    qa_start_total = time.time()

    for i, qa in enumerate(qa_items):
        print(f"\n   [{i + 1}/{len(qa_items)}] Q: {qa.question[:80]}...")
        qa_start = time.time()

        try:
            answer, search_results = kb.answer(qa.question, k=retrieval_k)
            contexts = [r.content for r in search_results]

            retrieval_queries = extract_retrieval_queries(
                question=qa.question,
                search_results=search_results,
                fallback_trace=getattr(kb, "last_trace", None),
            )

            if not contexts:
                print("   ⚠️  No contexts retrieved, using placeholder")
                contexts = ["No relevant context found."]

            qa_elapsed = time.time() - qa_start
            qa_times.append(qa_elapsed)

            print(f"   A: {answer[:200]}{'...' if len(answer) > 200 else ''}")
            print(f"   Retrieved {len(search_results)} contexts, took {qa_elapsed:.2f}s")
            if retrieval_queries:
                print(f"   Retrieval queries ({len(retrieval_queries)}):")
                for idx, query in enumerate(retrieval_queries, 1):
                    print(f"      [{idx}] {query}")
            else:
                print("   Retrieval queries: unavailable")

            if full_log:
                print("\n   === Full Answer ===")
                print(answer)
                print("\n   === Contexts ===")
                for j, ctx in enumerate(contexts, 1):
                    print(f"\n   [{j}] {ctx}")

            samples.append(
                EvaluationSample(
                    question=qa.question,
                    answer=answer,
                    contexts=contexts,
                    ground_truth=qa.answer,
                )
            )
            sample_debug.append(
                {
                    "question": qa.question,
                    "retrieval_queries": retrieval_queries,
                }
            )
        except Exception as e:
            qa_elapsed = time.time() - qa_start
            print(f"   ❌ Error: {e} (took {qa_elapsed:.2f}s)")
            errors.append({"question": qa.question, "error": str(e), "time": qa_elapsed})
            # Preserve failed samples with placeholder values so that every
            # framework is evaluated on the exact same question set.
            samples.append(
                EvaluationSample(
                    question=qa.question,
                    answer="Error: failed to generate answer.",
                    contexts=["No relevant context found."],
                    ground_truth=qa.answer,
                )
            )
            sample_debug.append(
                {
                    "question": qa.question,
                    "retrieval_queries": [],
                }
            )

    qa_total_time = time.time() - qa_start_total

    if not samples:
        error_msg = "❌ No samples collected. Cannot run evaluation."
        print(f"\n{error_msg}")
        return error_msg

    avg_time = sum(qa_times) / len(qa_times) if qa_times else 0
    print(f"\n   Collected {len(samples)} samples, {len(errors)} errors")
    print(f"   ⏱️  Q&A total time: {qa_total_time:.2f}s, avg per question: {avg_time:.2f}s")

    # Print all evaluation data
    print("\n" + "=" * 80)
    print("📋 EVALUATION DATA (for RAGAS)")
    print("=" * 80)
    for i, sample in enumerate(samples, 1):
        print(f"\n{'─' * 80}")
        print(f"📝 Sample {i}/{len(samples)}")
        print(f"{'─' * 80}")
        print(f"\n🔹 QUESTION:\n{sample.question}")
        print(f"\n🔹 GROUND TRUTH:\n{sample.ground_truth}")
        print(f"\n🔹 ANSWER:\n{sample.answer}")
        print(f"\n🔹 CONTEXTS ({len(sample.contexts)} items):")
        for j, ctx in enumerate(sample.contexts, 1):
            ctx_preview = ctx[:200] + "..." if len(ctx) > 200 else ctx
            print(f"   [{j}] {ctx_preview}")
    print("\n" + "=" * 80)

    # Step 4: Run evaluation
    print("\n5. Running evaluation...")
    eval_start = time.time()
    try:
        result = evaluator.evaluate(samples)
    except Exception as e:
        error_msg = f"❌ Evaluation failed: {e}"
        print(f"\n{error_msg}")
        return error_msg
    eval_time = time.time() - eval_start

    # Print results
    print(f"\n{result}")
    print("\n--- Timing ---")
    print(f"Q&A total time: {qa_total_time:.2f}s (avg {avg_time:.2f}s/question)")
    print(f"Evaluation time: {eval_time:.2f}s")
    print(f"Total time: {qa_total_time + eval_time:.2f}s")

    # Save results if specified
    if output_file:
        output_data = {
            "manifest": manifest,
            "timing": {
                "qa_total_seconds": round(qa_total_time, 2),
                "qa_avg_seconds": round(avg_time, 2),
                "eval_seconds": round(eval_time, 2),
                "total_seconds": round(qa_total_time + eval_time, 2),
            },
            "samples_count": len(samples),
            "errors_count": len(errors),
            "result": result,
            "errors": errors,
            "sample_debug": sample_debug,
        }
        with open(output_file, "w", encoding="utf-8") as f:
            json.dump(output_data, f, indent=2, ensure_ascii=False)
        print(f"\n📁 Results saved to: {output_file}")

    return result


def main():
    parser = argparse.ArgumentParser(description="RAG Evaluation")
    parser.add_argument(
        "--evaluator",
        choices=["ragas"],
        default="ragas",
        help="Evaluator to use (default: ragas)",
    )
    parser.add_argument(
        "--kb",
        choices=["langchain", "trpc-agent-go", "agno", "crewai", "autogen"],
        default="langchain",
        help="Knowledge base implementation to use (default: langchain)",
    )
    parser.add_argument(
        "--k",
        type=int,
        default=4,
        help="Number of documents to retrieve per query (default: 4)",
    )
    parser.add_argument(
        "--skip-load",
        action="store_true",
        default=False,
        help="Skip loading documents into knowledge base (default: False)",
    )
    parser.add_argument(
        "--load",
        action="store_true",
        help="Force loading documents into knowledge base",
    )
    parser.add_argument(
        "--output",
        type=str,
        default=None,
        help="Output file path to save evaluation results as JSON",
    )
    parser.add_argument(
        "--dataset",
        choices=["huggingface", "rgb", "multihop-rag"],
        default="huggingface",
        help="Dataset to use for evaluation (default: huggingface)",
    )
    parser.add_argument(
        "--rgb-subset",
        choices=["en", "zh", "en_int", "zh_int", "en_fact", "zh_fact"],
        default="en",
        help="RGB dataset subset (default: en). Only used when --dataset=rgb",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=60000000000,
        help="Timeout in seconds for evaluation (default: 600)",
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=30,
        help="Number of concurrent workers for evaluation (default: 30)",
    )
    args = parser.parse_args()

    # Initialize dataset
    from dataset import create_dataset
    dataset_kwargs = {}
    if args.dataset == "rgb":
        dataset_kwargs = {
            "subset": args.rgb_subset,
        }
    dataset = create_dataset(args.dataset, **dataset_kwargs)
    print(f"Using dataset: {args.dataset}")

    # Initialize knowledge base
    if args.kb == "trpc-agent-go":
        from knowledge_system.trpc_agent_go.knowledge_base import TRPCAgentGoKnowledgeBase
        kb = TRPCAgentGoKnowledgeBase(timeout=300000000, auto_start=False)
        print("Using tRPC-Agent-Go knowledge base")
    elif args.kb == "agno":
        from knowledge_system.agno.knowledge_base import AgnoKnowledgeBase
        kb = AgnoKnowledgeBase(max_results=args.k)
        print("Using Agno knowledge base")
    elif args.kb == "crewai":
        from knowledge_system.crewai.knowledge_base import CrewAIKnowledgeBase
        kb = CrewAIKnowledgeBase(max_results=args.k)
        print("Using CrewAI knowledge base")
    elif args.kb == "autogen":
        from knowledge_system.autogen.knowledge_base import AutoGenKnowledgeBase
        kb = AutoGenKnowledgeBase(max_results=args.k)
        print("Using AutoGen knowledge base")
    else:
        from knowledge_system.langchain.knowledge_base import LangChainKnowledgeBase
        kb = LangChainKnowledgeBase()
        print("Using LangChain knowledge base")

    # Initialize evaluator
    if args.evaluator == "ragas":
        from evaluator.ragas.evaluator import RAGASEvaluator
        evaluator = RAGASEvaluator(max_workers=args.workers, timeout=args.timeout)
        print("Using RAGAS evaluator")
    else:
        raise ValueError(f"Unknown evaluator: {args.evaluator}")

    # Run evaluation
    # --load overrides --skip-load
    skip_load = args.skip_load and not args.load
    run_evaluation(
        kb=kb,
        dataset=dataset,
        evaluator=evaluator,
        retrieval_k=args.k,
        skip_load=skip_load,
        full_log=True,
        output_file=args.output,
        kb_name=args.kb,
        evaluator_name=args.evaluator,
        dataset_name=args.dataset,
    )


if __name__ == "__main__":
    main()
