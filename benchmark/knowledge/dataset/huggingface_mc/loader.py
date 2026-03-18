#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
HuggingFace Multi-Context Dataset Loader for RAG Evaluation.

This module loads the huggingface_doc dataset for the knowledge base
and pre-generated multi-context QA pairs (from RAGAS TestsetGenerator) for evaluation.
The QA pairs require synthesizing information from multiple documents.
"""

import json
import os
import shutil
import sys
from typing import Any, List, Optional, cast

from datasets import load_dataset

sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from base import BaseDataset, QAItem


class HuggingFaceMultiContextDataset(BaseDataset):
    """Loader for HuggingFace documentation with multi-context QA pairs."""

    DOC_DATASET = "m-ric/huggingface_doc"
    _QA_FILE = "mc_qa_data/qa_pairs.json"

    def __init__(self):
        """Initialize the dataset loader."""
        self._doc_dataset: Any = None
        self._qa_data: Optional[list] = None
        self._tmp_dir: Optional[str] = None

    def load_documents(self, force_reload: bool = True, **kwargs) -> str:
        """
        Load markdown documents from huggingface_doc dataset to a temporary directory.

        Reuses the same document corpus as the standard HuggingFace dataset.

        Args:
            force_reload: If True (default), clear and reload documents.

        Returns:
            Path to the temporary directory containing markdown files.
        """
        # Reuse the same hf_docs directory as the standard huggingface dataset
        hf_dir = os.path.join(
            os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
            "huggingface",
            "hf_docs",
        )
        self._tmp_dir = hf_dir

        if not force_reload and os.path.exists(hf_dir) and os.listdir(hf_dir):
            existing_count = len(
                [f for f in os.listdir(hf_dir) if os.path.isfile(os.path.join(hf_dir, f))]
            )
            print(f"   Reusing existing {existing_count} documents in {hf_dir}")
            return hf_dir

        if self._doc_dataset is None:
            self._doc_dataset = load_dataset(self.DOC_DATASET, split="train")

        if os.path.exists(hf_dir):
            shutil.rmtree(hf_dir)
        os.makedirs(hf_dir, exist_ok=True)

        dataset = self._doc_dataset
        dataset = dataset.filter(lambda ex: ex["source"].endswith(".md"))
        print(f"   Filtered to {len(dataset)} markdown documents")

        docs_loaded = 0
        for item in dataset:
            source = cast(str, item["source"])
            filename = source.replace("/", "_").replace("\\", "_")
            filepath = os.path.join(hf_dir, filename)
            with open(filepath, "w", encoding="utf-8") as f:
                f.write(cast(str, item["text"]))
            docs_loaded += 1

        print(f"   Loaded {docs_loaded} documents")
        return hf_dir

    def _load_qa(self) -> list:
        """Load QA data from pre-generated JSON file."""
        qa_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), self._QA_FILE)

        if not os.path.exists(qa_path):
            raise FileNotFoundError(
                f"Multi-context QA data not found at {qa_path}. "
                f"Run 'python generate_qa.py' first to generate QA pairs."
            )

        with open(qa_path, "r", encoding="utf-8") as f:
            self._qa_data = json.load(f)

        return self._qa_data

    def load_qa_items(self) -> List[QAItem]:
        """
        Load multi-context QA items from pre-generated data.

        Returns:
            List of QAItem objects.
        """
        if self._qa_data is None:
            self._qa_data = self._load_qa()

        items: List[QAItem] = []
        for item in self._qa_data:
            items.append(
                QAItem(
                    question=item["question"],
                    answer=item["answer"],
                    context=item.get("context", ""),
                    source_doc=item.get("source_doc", "multi_context"),
                )
            )

        return items

    def cleanup(self):
        """No cleanup needed as we reuse the huggingface hf_docs directory."""
        pass
