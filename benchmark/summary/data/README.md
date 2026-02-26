# Summary Benchmark Data

This directory contains evaluation datasets for session summary benchmarking.

## MT-Bench-101

MT-Bench-101 is a fine-grained multi-turn dialogue benchmark (ACL 2024).

To download the dataset, run:

```bash
./download_datasets.sh
```

Or manually download from: https://github.com/mtbench101/mt-bench-101

### Expected Structure

```
data/
└── mt-bench-101/
    └── subjective/
        └── mtbench101.jsonl
```

## References

- [MT-Bench-101 Paper](https://arxiv.org/abs/2402.14762)
- [MT-Bench-101 GitHub](https://github.com/mtbench101/mt-bench-101)
