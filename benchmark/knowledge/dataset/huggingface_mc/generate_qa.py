#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
Generate multi-context QA pairs from HuggingFace documentation using RAGAS TestsetGenerator.

This script loads documents from the m-ric/huggingface_doc dataset, builds a knowledge graph,
and generates QA pairs that require information from multiple documents (multi-context).

Usage:
    python generate_qa.py [--size 50] [--output mc_qa_data/qa_pairs.json]
"""

import argparse
import json
import os
import sys
import time

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))

from langchain_core.documents import Document as LCDocument
from langchain_openai import ChatOpenAI, OpenAIEmbeddings
from openai import APIConnectionError, APITimeoutError, InternalServerError, RateLimitError
from pydantic import SecretStr
from ragas.run_config import RunConfig
from ragas.testset import TestsetGenerator
from ragas.testset.synthesizers import (
    MultiHopAbstractQuerySynthesizer,
    MultiHopSpecificQuerySynthesizer,
)

from util import get_config


def load_hf_documents(max_docs: int = 0, max_chars: int = 20000) -> list[LCDocument]:
    """Load markdown documents from HuggingFace dataset as LangChain Documents.

    Long documents are truncated to max_chars to stay within model context limits.
    RAGAS internally uses HeadlinesExtractor + HeadlineSplitter, so documents
    should not be pre-chunked.
    """
    from datasets import load_dataset

    dataset = load_dataset("m-ric/huggingface_doc", split="train")
    dataset = dataset.filter(lambda ex: ex["source"].endswith(".md"))
    print(f"Filtered to {len(dataset)} markdown documents")

    docs = []
    truncated = 0
    for item in dataset:
        text = item["text"]
        if len(text) > max_chars:
            text = text[:max_chars]
            truncated += 1
        doc = LCDocument(
            page_content=text,
            metadata={"source": item["source"]},
        )
        docs.append(doc)
        if max_docs > 0 and len(docs) >= max_docs:
            break

    print(f"Loaded {len(docs)} documents ({truncated} truncated to {max_chars} chars)")
    return docs


def _patch_headline_splitter():
    """Patch HeadlineSplitter to skip nodes missing 'headlines' instead of raising."""
    from ragas.testset.transforms.splitters.headline import HeadlineSplitter

    if getattr(HeadlineSplitter, "_patched", False):
        return

    _original_split = HeadlineSplitter.split

    async def _tolerant_split(self, node):
        if node.get_property("headlines") is None:
            return [node], []
        return await _original_split(self, node)

    HeadlineSplitter.split = _tolerant_split
    HeadlineSplitter._patched = True


def _patch_run_async_tasks():
    """Patch run_async_tasks to not re-raise the first exception after all tasks complete.

    RAGAS collects individual task exceptions but then raises the first one at the end,
    which kills the entire transform pipeline. With Qwen, LLM-based extractors
    occasionally return non-JSON, causing OutputParserException. This patch lets
    the pipeline continue — failed nodes simply miss that property.
    """
    import ragas.async_utils as _au
    import ragas.testset.transforms.engine as _engine

    if getattr(_au, "_patched_run_async", False):
        return

    _original = _au.run_async_tasks

    def _tolerant_run_async_tasks(tasks, **kwargs):
        import ragas.async_utils as au

        original_run = au.run

        async def _run_no_raise():
            from ragas.async_utils import as_completed, process_futures
            from ragas.utils import ProgressBarManager

            total_tasks = len(tasks)
            results = []
            show_progress = kwargs.get("show_progress", True)
            desc = kwargs.get("progress_bar_desc", "Running async tasks")
            max_workers = kwargs.get("max_workers", -1)
            cancel_check = kwargs.get("cancel_check", None)
            pbm = ProgressBarManager(desc, show_progress)

            with pbm.create_single_bar(total_tasks) as pbar:
                async for result in process_futures(
                    as_completed(tasks, max_workers, cancel_check=cancel_check)
                ):
                    if isinstance(result, Exception):
                        import logging
                        logging.getLogger(__name__).warning(
                            "Task failed (non-fatal): %s: %s",
                            type(result).__name__, str(result)[:200],
                        )
                    results.append(result)
                    pbar.update(1)
            return results

        return original_run(_run_no_raise())

    _au.run_async_tasks = _tolerant_run_async_tasks
    _engine.run_async_tasks = _tolerant_run_async_tasks
    _au._patched_run_async = True


def _build_transforms(llm, embedding_model):
    """Build custom transforms that skip CustomNodeFilter to avoid LLM JSON parse errors."""
    from ragas.testset.graph import NodeType
    from ragas.testset.transforms.engine import Parallel
    from ragas.testset.transforms.extractors import (
        EmbeddingExtractor,
        HeadlinesExtractor,
        SummaryExtractor,
    )
    from ragas.testset.transforms.extractors.llm_based import NERExtractor, ThemesExtractor
    from ragas.testset.transforms.relationship_builders import (
        CosineSimilarityBuilder,
        OverlapScoreBuilder,
    )
    from ragas.testset.transforms.splitters import HeadlineSplitter
    from ragas.utils import num_tokens_from_string

    def filter_doc_500(node):
        return (
            node.type == NodeType.DOCUMENT
            and num_tokens_from_string(node.properties["page_content"]) > 500
        )

    def filter_doc_500_with_summary(node):
        return filter_doc_500(node) and isinstance(node.get_property("summary"), str)

    def filter_doc_500_with_summary_embedding(node):
        return filter_doc_500(node) and node.get_property("summary_embedding") is not None

    def filter_chunks(node):
        return node.type == NodeType.CHUNK

    def filter_chunks_with_entities(node):
        return filter_chunks(node) and node.get_property("entities") is not None

    return [
        HeadlinesExtractor(llm=llm, filter_nodes=filter_doc_500),
        HeadlineSplitter(min_tokens=500),
        SummaryExtractor(llm=llm, filter_nodes=filter_doc_500),
        # Skip CustomNodeFilter — Qwen often returns non-JSON, causing parse failures.
        Parallel(
            EmbeddingExtractor(
                embedding_model=embedding_model,
                property_name="summary_embedding",
                embed_property_name="summary",
                filter_nodes=filter_doc_500_with_summary,
            ),
            ThemesExtractor(llm=llm, filter_nodes=filter_chunks),
            NERExtractor(llm=llm, filter_nodes=filter_chunks),
        ),
        Parallel(
            CosineSimilarityBuilder(
                property_name="summary_embedding",
                new_property_name="summary_similarity",
                threshold=0.7,
                filter_nodes=filter_doc_500_with_summary_embedding,
            ),
            OverlapScoreBuilder(threshold=0.01, filter_nodes=filter_chunks_with_entities),
        ),
    ]


def _is_retryable_generation_error(error: Exception) -> bool:
    """Return True for transient upstream/model-service failures."""
    retryable_types = (
        APIConnectionError,
        APITimeoutError,
        InternalServerError,
        RateLimitError,
        ConnectionError,
        TimeoutError,
        OSError,
    )
    if isinstance(error, retryable_types):
        return True

    message = str(error).lower()
    retryable_markers = (
        "connect call failed",
        "cannot connect to host",
        "timed out",
        "timeout",
        "rate limit",
        "server_error",
        "internalservererror",
        "temporarily unavailable",
        "connection reset",
        "service unavailable",
    )
    return any(marker in message for marker in retryable_markers)


def _generate_testset_with_retries(
    generator: TestsetGenerator,
    documents: list[LCDocument],
    testset_size: int,
    transforms,
    query_distribution,
    run_config: RunConfig,
    retry_attempts: int,
    retry_wait_seconds: int,
):
    """Retry full testset generation on transient upstream failures."""
    last_error: Exception | None = None

    for attempt in range(1, retry_attempts + 1):
        try:
            if attempt > 1:
                print(f"\nRetrying QA generation (attempt {attempt}/{retry_attempts})...")
            return generator.generate_with_langchain_docs(
                documents=documents,
                testset_size=testset_size,
                transforms=transforms,
                query_distribution=query_distribution,
                run_config=run_config,
                with_debugging_logs=True,
                raise_exceptions=False,
            )
        except Exception as error:
            last_error = error
            if not _is_retryable_generation_error(error) or attempt == retry_attempts:
                raise

            sleep_seconds = retry_wait_seconds * attempt
            print(
                f"\nTransient generation failure on attempt {attempt}/{retry_attempts}: "
                f"{type(error).__name__}: {str(error)[:300]}"
            )
            print(f"Waiting {sleep_seconds}s before retrying full generation...")
            time.sleep(sleep_seconds)

    if last_error is not None:
        raise last_error
    raise RuntimeError("QA generation failed without a recorded exception")


def generate_multi_context_qa(
    documents: list[LCDocument],
    testset_size: int = 50,
    max_workers: int = 10,
    retry_attempts: int = 3,
    retry_wait_seconds: int = 30,
    request_retries: int = 15,
    request_max_wait: int = 120,
) -> list[dict]:
    """Generate multi-context QA pairs using RAGAS TestsetGenerator."""
    _patch_headline_splitter()
    _patch_run_async_tasks()
    config = get_config()

    llm = ChatOpenAI(
        model=config["eval_model_name"],
        temperature=0,
        api_key=SecretStr(config["eval_api_key"]) if config["eval_api_key"] else None,
        base_url=config["eval_base_url"],
        max_tokens=4096,
    )
    embeddings = OpenAIEmbeddings(
        model=config["embedding_model"],
        api_key=SecretStr(config["eval_api_key"]) if config["eval_api_key"] else None,
        base_url=config["eval_base_url"],
        tiktoken_enabled=False,
        check_embedding_ctx_length=False,
    )

    generator = TestsetGenerator.from_langchain(
        llm=llm,
        embedding_model=embeddings,
    )

    transforms = _build_transforms(generator.llm, generator.embedding_model)

    # Use only multi-hop query synthesizers for multi-context QA
    query_distribution = [
        (MultiHopAbstractQuerySynthesizer(llm=generator.llm), 0.5),
        (MultiHopSpecificQuerySynthesizer(llm=generator.llm), 0.5),
    ]

    run_config = RunConfig(
        max_workers=max_workers,
        timeout=600,
        max_retries=request_retries,
        max_wait=request_max_wait,
        log_tenacity=True,
    )

    print(f"\nGenerating {testset_size} multi-context QA pairs...")
    print(f"  LLM: {config['eval_model_name']}")
    print(f"  Embedding: {config['embedding_model']}")
    print(f"  Query distribution: 50% MultiHopAbstract + 50% MultiHopSpecific")
    print()

    print(
        f"  Request retries: {request_retries}, max wait: {request_max_wait}s, "
        f"full-run attempts: {retry_attempts}"
    )
    print()

    testset = _generate_testset_with_retries(
        generator=generator,
        documents=documents,
        testset_size=testset_size,
        transforms=transforms,
        query_distribution=query_distribution,
        run_config=run_config,
        retry_attempts=retry_attempts,
        retry_wait_seconds=retry_wait_seconds,
    )

    df = testset.to_pandas()
    print(f"\nGenerated {len(df)} QA pairs")

    qa_items = []
    for _, row in df.iterrows():
        contexts = row.get("reference_contexts", [])
        if isinstance(contexts, list):
            context_str = "\n\n---\n\n".join(contexts)
        else:
            context_str = str(contexts) if contexts else ""

        qa_items.append({
            "question": str(row.get("user_input", "")),
            "answer": str(row.get("reference", "")),
            "context": context_str,
            "source_doc": "multi_context",
            "synthesizer_name": str(row.get("synthesizer_name", "")),
        })

    return qa_items


def main():
    parser = argparse.ArgumentParser(description="Generate multi-context QA pairs")
    parser.add_argument(
        "--size",
        type=int,
        default=50,
        help="Number of QA pairs to generate (default: 50)",
    )
    parser.add_argument(
        "--max-docs",
        type=int,
        default=0,
        help="Maximum number of documents to load (0=all, default: 0)",
    )
    parser.add_argument(
        "--output",
        type=str,
        default=None,
        help="Output JSON file path (default: mc_qa_data/qa_pairs.json in this directory)",
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=10,
        help="Number of concurrent workers (default: 10)",
    )
    parser.add_argument(
        "--retry-attempts",
        type=int,
        default=3,
        help="Number of full QA-generation retry attempts on transient failures (default: 3)",
    )
    parser.add_argument(
        "--retry-wait",
        type=int,
        default=30,
        help="Base wait time in seconds between full-generation retries (default: 30)",
    )
    parser.add_argument(
        "--request-retries",
        type=int,
        default=15,
        help="Max retries per individual RAGAS/LLM request (default: 15)",
    )
    parser.add_argument(
        "--request-max-wait",
        type=int,
        default=120,
        help="Max wait time in seconds between request retries (default: 120)",
    )
    args = parser.parse_args()

    output_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mc_qa_data")
    os.makedirs(output_dir, exist_ok=True)
    output_file = args.output or os.path.join(output_dir, "qa_pairs.json")

    print("=== Multi-Context QA Generation ===\n")

    # Step 1: Load documents
    print("1. Loading HuggingFace documents...")
    documents = load_hf_documents(max_docs=args.max_docs)

    # Step 2: Generate QA pairs
    print(f"\n2. Generating {args.size} multi-context QA pairs...")
    qa_items = generate_multi_context_qa(
        documents=documents,
        testset_size=args.size,
        max_workers=args.workers,
        retry_attempts=args.retry_attempts,
        retry_wait_seconds=args.retry_wait,
        request_retries=args.request_retries,
        request_max_wait=args.request_max_wait,
    )

    # Step 3: Save results
    print(f"\n3. Saving {len(qa_items)} QA pairs to {output_file}")
    with open(output_file, "w", encoding="utf-8") as f:
        json.dump(qa_items, f, indent=2, ensure_ascii=False)

    print(f"\nDone! Generated {len(qa_items)} multi-context QA pairs.")
    print(f"Output: {output_file}")

    # Print sample
    if qa_items:
        print(f"\n--- Sample QA ---")
        sample = qa_items[0]
        print(f"Q: {sample['question'][:200]}")
        print(f"A: {sample['answer'][:200]}")
        print(f"Synthesizer: {sample['synthesizer_name']}")


if __name__ == "__main__":
    main()
