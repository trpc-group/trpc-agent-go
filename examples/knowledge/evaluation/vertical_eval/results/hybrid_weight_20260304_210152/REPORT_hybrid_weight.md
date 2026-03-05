# Vertical Evaluation Report: hybrid_weight

| Experiment | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall | QA Time (avg) |
|---|---|---|---|---|---|---|---|---|
| hybrid_v0_t100 | 0.7625 | 0.6862 | 0.5830 | 0.6785 | 0.4046 | 0.6000 | 0.3500 | 19.4s |
| hybrid_v100_t0 | 1.0000 | 0.8787 | 0.7648 | 0.7493 | 0.7665 | 1.0000 | 0.5500 | 15.9s |
| hybrid_v10_t90 | 0.8417 | 0.6090 | 0.6260 | 0.6840 | 0.5358 | 0.8000 | 0.5500 | 15.8s |
| hybrid_v20_t80 | 0.8500 | 0.6804 | 0.5279 | 0.6691 | 0.5258 | 0.8000 | 0.5000 | 16.1s |
| hybrid_v30_t70 | 0.9750 | 0.6744 | 0.4706 | 0.6622 | 0.5624 | 0.8000 | 0.4500 | 15.9s |
| hybrid_v40_t60 | 0.8800 | 0.7348 | 0.5657 | 0.6963 | 0.6109 | 0.9000 | 0.5000 | 21.6s |
| hybrid_v50_t50 | 0.8667 | 0.7296 | 0.5921 | 0.6817 | 0.5795 | 0.8000 | 0.5500 | 17.7s |
| hybrid_v60_t40 | 0.9000 | 0.8126 | 0.6955 | 0.7086 | 0.6223 | 0.9000 | 0.5500 | 22.2s |
| hybrid_v70_t30 | 0.9000 | 0.7929 | 0.6240 | 0.7045 | 0.6787 | 0.9000 | 0.4500 | 13.6s |
| hybrid_v80_t20 | 0.9000 | 0.8044 | 0.6305 | 0.7021 | 0.7018 | 0.9000 | 0.4700 | 14.6s |
| hybrid_v90_t10 | 1.0000 | 0.8544 | 0.6543 | 0.7232 | 0.7257 | 0.9000 | 0.5750 | 21.9s |

## Experiment Configurations

### hybrid_v0_t100
- **Description**: Hybrid: vector=0.0, text=1.0 (pure text via hybrid path)
- **Hybrid weights**: vector=0.0, text=1.0
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=193.61s, eval=370.38s

### hybrid_v100_t0
- **Description**: Hybrid: vector=1.0, text=0.0 (pure vector via hybrid path)
- **Hybrid weights**: vector=1.0, text=0.0
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=159.23s, eval=313.66s

### hybrid_v10_t90
- **Description**: Hybrid: vector=0.1, text=0.9
- **Hybrid weights**: vector=0.1, text=0.9
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=157.47s, eval=381.57s

### hybrid_v20_t80
- **Description**: Hybrid: vector=0.2, text=0.8
- **Hybrid weights**: vector=0.2, text=0.8
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=161.24s, eval=370.53s

### hybrid_v30_t70
- **Description**: Hybrid: vector=0.3, text=0.7
- **Hybrid weights**: vector=0.3, text=0.7
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=159.03s, eval=358.97s

### hybrid_v40_t60
- **Description**: Hybrid: vector=0.4, text=0.6
- **Hybrid weights**: vector=0.4, text=0.6
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=216.3s, eval=357.6s

### hybrid_v50_t50
- **Description**: Hybrid: vector=0.5, text=0.5 (equal weight)
- **Hybrid weights**: vector=0.5, text=0.5
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=176.85s, eval=376.25s

### hybrid_v60_t40
- **Description**: Hybrid: vector=0.6, text=0.4
- **Hybrid weights**: vector=0.6, text=0.4
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=222.2s, eval=368.59s

### hybrid_v70_t30
- **Description**: Hybrid: vector=0.7, text=0.3
- **Hybrid weights**: vector=0.7, text=0.3
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=135.81s, eval=337.37s

### hybrid_v80_t20
- **Description**: Hybrid: vector=0.8, text=0.2
- **Hybrid weights**: vector=0.8, text=0.2
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=145.71s, eval=324.55s

### hybrid_v90_t10
- **Description**: Hybrid: vector=0.9, text=0.1
- **Hybrid weights**: vector=0.9, text=0.1
- **Retrieval k**: 4
- **Samples**: 10, Errors: 0
- **Timing**: QA total=219.39s, eval=371.06s
