"""
HuggingFace Dataset Loader for RAG Evaluation.

This module loads the huggingface_doc dataset for knowledge base
and huggingface_doc_qa_eval dataset for RAG evaluation.
"""

import os
import sys
import shutil
import tempfile
import random
from typing import List, Optional, Any, cast, Set

from datasets import load_dataset

sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from base import BaseDataset, QAItem


class HuggingFaceDocDataset(BaseDataset):
    """Loader for HuggingFace documentation datasets."""

    DOC_DATASET = "m-ric/huggingface_doc"
    QA_DATASET = "m-ric/huggingface_doc_qa_eval"

    def __init__(self):
        """Initialize the dataset loader."""
        self._doc_dataset: Any = None
        self._qa_dataset: Any = None
        self._tmp_dir: Optional[str] = None
        self._qa_source_docs: Set[str] = set()

    def load_documents(self, max_docs: Optional[int] = None, filter_extensions: Optional[List[str]] = None, force_reload: bool = False) -> str:
        """
        Load documents from huggingface_doc dataset to a temporary directory.

        Strategy:
        1. Load all QA source documents (ground truth)
        2. Add random documents as distractors (default 30)

        Args:
            max_docs: Maximum number of random distractor documents. Default 30.
            filter_extensions: List of file extensions to include (e.g., ['.md', '.mdx']). None for all.
            force_reload: If True, clear and reload documents. If False, reuse existing documents if present.

        Returns:
            Path to the temporary directory containing markdown files.
        """
        self._tmp_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "hf_docs")

        # Reuse existing documents if present (for fair comparison between RAG systems)
        if not force_reload and os.path.exists(self._tmp_dir) and os.listdir(self._tmp_dir):
            existing_count = len([f for f in os.listdir(self._tmp_dir) if os.path.isfile(os.path.join(self._tmp_dir, f))])
            print(f"   Reusing existing {existing_count} documents in {self._tmp_dir}")
            return self._tmp_dir

        if self._doc_dataset is None:
            self._doc_dataset = load_dataset(self.DOC_DATASET, split="train")

        # Clear directory before loading
        if os.path.exists(self._tmp_dir):
            shutil.rmtree(self._tmp_dir)
        os.makedirs(self._tmp_dir, exist_ok=True)

        # Filter by file extension if specified
        dataset = self._doc_dataset
        if filter_extensions:
            def has_valid_extension(example):
                source = example["source"]
                return any(source.endswith(ext) for ext in filter_extensions)

            dataset = dataset.filter(has_valid_extension)
            print(f"   Filtered to {len(dataset)} documents with extensions: {filter_extensions}")

        # Step 1: Load QA source documents
        qa_docs_loaded = 0
        for item in dataset:
            source = cast(str, item["source"])
            if source in self._qa_source_docs:
                filename = source.replace("/", "_").replace("\\", "_")
                filepath = os.path.join(self._tmp_dir, filename)
                with open(filepath, "w", encoding="utf-8") as f:
                    f.write(cast(str, item["text"]))
                qa_docs_loaded += 1

        print(f"   Loaded {qa_docs_loaded} QA source documents")

        # Step 2: Add random distractor documents
        num_distractors = max_docs if max_docs is not None else 30

        # Get all non-QA documents
        distractor_candidates = [
            item for item in dataset
            if cast(str, item["source"]) not in self._qa_source_docs
        ]

        # Randomly sample distractors
        if len(distractor_candidates) > num_distractors:
            random.seed(42)  # For reproducibility
            distractor_docs = random.sample(distractor_candidates, num_distractors)
        else:
            distractor_docs = distractor_candidates

        for item in distractor_docs:
            source = cast(str, item["source"])
            filename = source.replace("/", "_").replace("\\", "_")
            filepath = os.path.join(self._tmp_dir, filename)
            with open(filepath, "w", encoding="utf-8") as f:
                f.write(cast(str, item["text"]))

        print(f"   Loaded {len(distractor_docs)} random distractor documents")
        print(f"   Total documents: {qa_docs_loaded + len(distractor_docs)}")

        return self._tmp_dir

    def load_qa_items(self, max_items: Optional[int] = None) -> List[QAItem]:
        """
        Load QA items from huggingface_doc_qa_eval dataset.

        Args:
            max_items: Maximum number of QA items to load. None for all.

        Returns:
            List of QAItem objects.
        """
        if self._qa_dataset is None:
            self._qa_dataset = load_dataset(self.QA_DATASET, split="train")

        items: List[QAItem] = []
        dataset = self._qa_dataset
        if max_items is not None:
            dataset = dataset.select(range(min(max_items, len(dataset))))

        for item in dataset:
            source_doc = cast(str, item["source_doc"])
            # Collect source_doc for later document loading
            self._qa_source_docs.add(source_doc)
            items.append(
                QAItem(
                    question=cast(str, item["question"]),
                    answer=cast(str, item["answer"]),
                    context=cast(str, item["context"]),
                    source_doc=source_doc,
                )
            )

        print(f"   Collected {len(self._qa_source_docs)} unique source documents from QA items")
        return items

    def cleanup(self):
        """Remove temporary directory."""
        if self._tmp_dir and os.path.exists(self._tmp_dir):
            shutil.rmtree(self._tmp_dir)
            self._tmp_dir = None
