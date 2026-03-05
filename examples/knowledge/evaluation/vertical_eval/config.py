"""
Experiment configurations for vertical evaluation of trpc-agent-go knowledge system.

Each experiment defines a set of tunable parameters for the Go knowledge service.
The vertical_eval runner iterates over these configs, starts a Go service instance
per config, runs evaluation, and collects results for comparison.
"""

from dataclasses import dataclass
from typing import List


@dataclass
class ExperimentConfig:
    """A single experiment configuration."""

    name: str
    description: str

    # Hybrid search weights
    hybrid_vector_weight: float = 0.99999
    hybrid_text_weight: float = 0.00001

    # Retrieval parameters
    retrieval_k: int = 4

    # PGVector table name (each experiment uses its own table to avoid collision)
    pg_table: str = ""

    # Go service port (auto-assigned if 0)
    port: int = 0

    def go_flags(self) -> List[str]:
        """Build Go service command-line flags from this config."""
        flags = [
            f"--hybrid-vector-weight={self.hybrid_vector_weight}",
            f"--hybrid-text-weight={self.hybrid_text_weight}",
        ]
        if hasattr(self, 'use_rrf') and getattr(self, 'use_rrf'):
            flags.append("--use-rrf=true")
        if self.pg_table:
            flags.append(f"--pg-table={self.pg_table}")
        if self.port > 0:
            flags.append(f"--port={self.port}")
        return flags


# ── Pre-defined experiment suites ──────────────────────────────────────────

HYBRID_RRF_EXPERIMENTS = [
    ExperimentConfig(
        name="hybrid_rrf",
        description="Hybrid: Reciprocal Rank Fusion (RRF) with default k=60",
        pg_table="veval_hw_rrf",
    ),
]
# dynamically set use_rrf since it's not a standard field but checked in go_flags
for exp in HYBRID_RRF_EXPERIMENTS:
    setattr(exp, 'use_rrf', True)

HYBRID_WEIGHT_EXPERIMENTS = [
    ExperimentConfig(
        name="hybrid_v100_t0",
        description="Hybrid: vector=1.0, text=0.0 (pure vector via hybrid path)",
        hybrid_vector_weight=1.0,
        hybrid_text_weight=0.0,
        pg_table="veval_hw_v100_t0",
    ),
    ExperimentConfig(
        name="hybrid_v90_t10",
        description="Hybrid: vector=0.9, text=0.1",
        hybrid_vector_weight=0.9,
        hybrid_text_weight=0.1,
        pg_table="veval_hw_v90_t10",
    ),
    ExperimentConfig(
        name="hybrid_v80_t20",
        description="Hybrid: vector=0.8, text=0.2",
        hybrid_vector_weight=0.8,
        hybrid_text_weight=0.2,
        pg_table="veval_hw_v80_t20",
    ),
    ExperimentConfig(
        name="hybrid_v70_t30",
        description="Hybrid: vector=0.7, text=0.3",
        hybrid_vector_weight=0.7,
        hybrid_text_weight=0.3,
        pg_table="veval_hw_v70_t30",
    ),
    ExperimentConfig(
        name="hybrid_v60_t40",
        description="Hybrid: vector=0.6, text=0.4",
        hybrid_vector_weight=0.6,
        hybrid_text_weight=0.4,
        pg_table="veval_hw_v60_t40",
    ),
    ExperimentConfig(
        name="hybrid_v50_t50",
        description="Hybrid: vector=0.5, text=0.5 (equal weight)",
        hybrid_vector_weight=0.5,
        hybrid_text_weight=0.5,
        pg_table="veval_hw_v50_t50",
    ),
    ExperimentConfig(
        name="hybrid_v40_t60",
        description="Hybrid: vector=0.4, text=0.6",
        hybrid_vector_weight=0.4,
        hybrid_text_weight=0.6,
        pg_table="veval_hw_v40_t60",
    ),
    ExperimentConfig(
        name="hybrid_v30_t70",
        description="Hybrid: vector=0.3, text=0.7",
        hybrid_vector_weight=0.3,
        hybrid_text_weight=0.7,
        pg_table="veval_hw_v30_t70",
    ),
    ExperimentConfig(
        name="hybrid_v20_t80",
        description="Hybrid: vector=0.2, text=0.8",
        hybrid_vector_weight=0.2,
        hybrid_text_weight=0.8,
        pg_table="veval_hw_v20_t80",
    ),
    ExperimentConfig(
        name="hybrid_v10_t90",
        description="Hybrid: vector=0.1, text=0.9",
        hybrid_vector_weight=0.1,
        hybrid_text_weight=0.9,
        pg_table="veval_hw_v10_t90",
    ),
    ExperimentConfig(
        name="hybrid_v0_t100",
        description="Hybrid: vector=0.0, text=1.0 (pure text via hybrid path)",
        hybrid_vector_weight=0.0,
        hybrid_text_weight=1.0,
        pg_table="veval_hw_v0_t100",
    ),
]

RETRIEVAL_K_EXPERIMENTS = [
    ExperimentConfig(
        name="k2",
        description="Retrieve top-2 documents",
        retrieval_k=2,
        pg_table="veval_k2",
    ),
    ExperimentConfig(
        name="k4",
        description="Retrieve top-4 documents (default)",
        retrieval_k=4,
        pg_table="veval_k4",
    ),
    ExperimentConfig(
        name="k6",
        description="Retrieve top-6 documents",
        retrieval_k=6,
        pg_table="veval_k6",
    ),
    ExperimentConfig(
        name="k8",
        description="Retrieve top-8 documents",
        retrieval_k=8,
        pg_table="veval_k8",
    ),
    ExperimentConfig(
        name="k10",
        description="Retrieve top-10 documents",
        retrieval_k=10,
        pg_table="veval_k10",
    ),
    ExperimentConfig(
        name="k12",
        description="Retrieve top-12 documents",
        retrieval_k=12,
        pg_table="veval_k12",
    ),
    ExperimentConfig(
        name="k14",
        description="Retrieve top-14 documents",
        retrieval_k=14,
        pg_table="veval_k14",
    ),
    ExperimentConfig(
        name="k16",
        description="Retrieve top-16 documents",
        retrieval_k=16,
        pg_table="veval_k16",
    ),
]

# All experiment suites keyed by name
EXPERIMENT_SUITES = {
    "hybrid_weight": HYBRID_WEIGHT_EXPERIMENTS,
    "hybrid_rrf": HYBRID_RRF_EXPERIMENTS,
    "retrieval_k": RETRIEVAL_K_EXPERIMENTS,
}
