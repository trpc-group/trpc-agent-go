#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
HuggingFace Dataset Loader for RAG Evaluation.

This module loads the huggingface_doc dataset for knowledge base
and huggingface_doc_qa_eval dataset for RAG evaluation.
"""

import os
import sys
import shutil
from typing import List, Optional, Any, cast

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

    def load_documents(self, force_reload: bool = True, **kwargs) -> str:
        """
        Load markdown documents from huggingface_doc dataset to a temporary directory.

        Args:
            force_reload: If True (default), clear and reload documents. If False, reuse existing documents if present.

        Returns:
            Path to the temporary directory containing markdown files.
        """
        self._tmp_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "hf_docs")

        if not force_reload and os.path.exists(self._tmp_dir) and os.listdir(self._tmp_dir):
            existing_count = len([f for f in os.listdir(self._tmp_dir) if os.path.isfile(os.path.join(self._tmp_dir, f))])
            print(f"   Reusing existing {existing_count} documents in {self._tmp_dir}")
            return self._tmp_dir

        if self._doc_dataset is None:
            self._doc_dataset = load_dataset(self.DOC_DATASET, split="train")

        if os.path.exists(self._tmp_dir):
            shutil.rmtree(self._tmp_dir)
        os.makedirs(self._tmp_dir, exist_ok=True)

        dataset = self._doc_dataset
        dataset = dataset.filter(lambda ex: ex["source"].endswith(".md"))
        print(f"   Filtered to {len(dataset)} markdown documents")

        docs_loaded = 0
        for item in dataset:
            source = cast(str, item["source"])
            filename = source.replace("/", "_").replace("\\", "_")
            filepath = os.path.join(self._tmp_dir, filename)
            with open(filepath, "w", encoding="utf-8") as f:
                f.write(cast(str, item["text"]))
            docs_loaded += 1

        print(f"   Loaded {docs_loaded} documents")

        return self._tmp_dir

    def load_qa_items(self) -> List[QAItem]:
        """
        Load QA items from huggingface_doc_qa_eval dataset.

        Only includes QA items whose source document is a markdown file,
        consistent with the documents loaded by load_documents().

        Returns:
            List of QAItem objects.
        """
        if self._qa_dataset is None:
            self._qa_dataset = load_dataset(self.QA_DATASET, split="train")

        items: List[QAItem] = []
        dataset = self._qa_dataset

        for item in dataset:
            source_doc = cast(str, item["source_doc"])

            if not source_doc.endswith(".md"):
                continue

            items.append(
                QAItem(
                    question=cast(str, item["question"]),
                    answer=cast(str, item["answer"]),
                    context=cast(str, item["context"]),
                    source_doc=source_doc,
                )
            )

        return items

    def cleanup(self):
        """Remove temporary directory."""
        if self._tmp_dir and os.path.exists(self._tmp_dir):
            shutil.rmtree(self._tmp_dir)
            self._tmp_dir = None
