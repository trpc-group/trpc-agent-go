"""
Main entry point for RAG evaluation with different evaluators.
"""

import argparse
import json
import os
import time
from typing import Optional

from dataset.base import BaseDataset
from knowledge_system.base import KnowledgeBase
from evaluator.base import Evaluator, EvaluationSample


def run_evaluation(
    kb: KnowledgeBase,
    dataset: BaseDataset,
    evaluator: Evaluator,
    max_docs: Optional[int] = 100,
    max_qa_items: Optional[int] = 10,
    retrieval_k: int = 4,
    skip_load: bool = False,
    full_log: bool = False,
    output_file: Optional[str] = None,
    force_reload: bool = True,
) -> str:
    """
    Run RAG evaluation with specified evaluator.

    Args:
        kb: Knowledge base instance implementing KnowledgeBase interface.
        dataset: Dataset instance implementing BaseDataset interface.
        evaluator: Evaluator instance implementing Evaluator interface.
        max_docs: Maximum documents to load into knowledge base.
        max_qa_items: Maximum QA items for evaluation.
        retrieval_k: Number of documents to retrieve per query.
        skip_load: If True, skip loading documents into knowledge base.
        full_log: If True, print full answer results for each question.
        output_file: Optional path to save evaluation results as JSON.
        force_reload: If True (default), force reload documents even if already cached.

    Returns:
        Evaluation results as formatted string.
    """
    print("=== RAG Evaluation ===\n")

    # Step 1: Load QA items
    print("1. Loading QA items...")
    qa_items = dataset.load_qa_items(max_qa_items, filter_extensions=[".md"])
    print(f"   Loaded {len(qa_items)} QA items.\n")

    # Step 2: Load documents if needed
    if skip_load:
        print("2. Skipping document loading (--skip-load enabled)...\n")
    else:
        print("2. Loading documents...")
        doc_dir = dataset.load_documents(max_docs, filter_extensions=[".md"], force_reload=force_reload)

        file_paths = []
        for filename in os.listdir(doc_dir):
            filepath = os.path.join(doc_dir, filename)
            if os.path.isfile(filepath):
                file_paths.append(filepath)

        print(f"   Found {len(file_paths)} documents.")

        print("3. Building knowledge base...")
        kb.load(file_paths)
        print("   Knowledge base built.\n")

    # Step 3: Run Q&A
    print("4. Running Q&A with fresh sessions...")
    samples = []
    errors = []
    qa_times = []
    qa_start_total = time.time()

    for i, qa in enumerate(qa_items):
        print(f"\n   [{i + 1}/{len(qa_items)}] Q: {qa.question[:80]}...")
        qa_start = time.time()

        try:
            answer, search_results = kb.answer(qa.question, k=retrieval_k)
            contexts = [r.content for r in search_results]

            if not contexts:
                print("   ⚠️  No contexts retrieved, using placeholder")
                contexts = ["No relevant context found."]

            qa_elapsed = time.time() - qa_start
            qa_times.append(qa_elapsed)

            print(f"   A: {answer[:200]}{'...' if len(answer) > 200 else ''}")
            print(f"   Retrieved {len(search_results)} contexts, took {qa_elapsed:.2f}s")

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
        except Exception as e:
            qa_elapsed = time.time() - qa_start
            print(f"   ❌ Error: {e} (took {qa_elapsed:.2f}s)")
            errors.append({"question": qa.question, "error": str(e), "time": qa_elapsed})
            continue

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
            "timestamp": time.strftime("%Y-%m-%d %H:%M:%S"),
            "config": {
                "max_docs": max_docs,
                "max_qa_items": max_qa_items,
                "retrieval_k": retrieval_k,
            },
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
        "--max-docs",
        type=int,
        default=None,
        help="Maximum documents to load (default: all documents)",
    )
    parser.add_argument(
        "--max-qa",
        type=int,
        default=None,
        help="Maximum QA items for evaluation (default: all QA items)",
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
        help="Skip loading documents into knowledge base (default: True)",
    )
    parser.add_argument(
        "--load",
        action="store_true",
        help="Force loading documents into knowledge base",
    )
    parser.add_argument(
        "--full-log",
        action="store_true",
        help="Print full answer results for each question",
    )
    parser.add_argument(
        "--output",
        type=str,
        default=None,
        help="Output file path to save evaluation results as JSON",
    )
    parser.add_argument(
        "--cache-document",
        action="store_true",
        help="Reuse cached documents if available (default: always re-pull)",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=6000000000,
        help="Timeout in seconds for evaluation (default: 600)",
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=10,
        help="Number of concurrent workers for evaluation (default: 1)",
    )
    args = parser.parse_args()

    # Initialize dataset
    from dataset.huggingface.loader import HuggingFaceDocDataset
    dataset = HuggingFaceDocDataset()

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
        max_docs=args.max_docs,
        max_qa_items=args.max_qa,
        retrieval_k=args.k,
        skip_load=skip_load,
        full_log=args.full_log,
        output_file=args.output,
        force_reload=not args.cache_document,
    )


if __name__ == "__main__":
    main()
