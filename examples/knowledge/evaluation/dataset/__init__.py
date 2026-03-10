"""
Dataset Module - provides a registry of available datasets.
"""

from typing import Any, Dict, Optional

from dataset.base import BaseDataset

AVAILABLE_DATASETS = {
    "huggingface": "HuggingFace documentation QA (m-ric/huggingface_doc)",
    "rgb": "RGB benchmark (chen700564/RGB) - Retrieval-Augmented Generation Benchmark",
    "multihop-rag": "MultiHop-RAG (yixuantt/MultiHop-RAG) - Multi-hop queries across documents",
}


def create_dataset(name: str, **kwargs: Any) -> BaseDataset:
    """Factory function to create a dataset instance by name.

    Parameters
    ----------
    name : str
        Dataset identifier.  One of ``huggingface``, ``rgb``, ``multihop-rag``.
    **kwargs
        Dataset-specific keyword arguments forwarded to the constructor.

        RGB accepts:
            subset (str): language/task subset (``en``, ``zh``, ``en_int``, etc.).

        MultiHop-RAG accepts:
            question_types (list[str] or None): filter by question type.
    """
    if name == "huggingface":
        from dataset.huggingface.loader import HuggingFaceDocDataset
        return HuggingFaceDocDataset(**kwargs)

    if name == "rgb":
        from dataset.rgb.loader import RGBDataset
        return RGBDataset(**kwargs)

    if name == "multihop-rag":
        from dataset.multihop_rag.loader import MultiHopRAGDataset
        return MultiHopRAGDataset(**kwargs)

    raise ValueError(
        f"Unknown dataset '{name}'. Available: {list(AVAILABLE_DATASETS.keys())}"
    )
