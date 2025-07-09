# Large Language Model

*A large language model (LLM) is a language model trained with self-supervised machine learning on a vast amount of text, designed for natural language processing tasks, especially language generation.*

The largest and most capable LLMs are generative pretrained transformers (GPTs), which are largely used in generative chatbots such as ChatGPT, Gemini or Claude. LLMs can be fine-tuned for specific tasks or guided by prompt engineering. These models acquire predictive power regarding syntax, semantics, and ontologies inherent in human language corpora, but they also inherit inaccuracies and biases present in the data they are trained in.

## Table of Contents

- [History](#history)
- [Dataset Preprocessing](#dataset-preprocessing)
- [Training](#training)
- [Architecture](#architecture)

## History

The training compute of notable large models in FLOPs vs publication date over the period 2010–2024 shows the evolution of language models. Before the emergence of transformer-based models in 2017, some language models were considered large relative to the computational and data constraints of their time.

### Early Developments

In the early 1990s, IBM's statistical models pioneered word alignment techniques for machine translation, laying the groundwork for corpus-based language modeling. A smoothed n-gram model in 2001, such as those employing Kneser-Ney smoothing, trained on 300 million words achieved state-of-the-art perplexity on benchmark tests at the time.

During the 2000s, with the rise of widespread internet access, researchers began compiling massive text datasets from the web ("web as corpus") to train statistical language models.

### Deep Learning Era

Following the breakthrough of deep neural networks in image classification around 2012, similar architectures were adapted for language tasks. This shift was marked by the development of word embeddings (e.g., Word2Vec by Mikolov in 2013) and sequence-to-sequence (seq2seq) models using LSTM.

In 2016, Google transitioned its translation service to neural machine translation (NMT), replacing statistical phrase-based models with deep recurrent neural networks.

### Transformer Revolution

At the 2017 NeurIPS conference, Google researchers introduced the transformer architecture in their landmark paper "Attention Is All You Need". This paper's goal was to improve upon 2014 seq2seq technology, and was based mainly on the attention mechanism developed by Bahdanau et al. in 2014.

The following year in 2018, BERT was introduced and quickly became "ubiquitous". Though the original transformer has both encoder and decoder blocks, BERT is an encoder-only model.

### GPT Series and Modern Era

Although decoder-only GPT-1 was introduced in 2018, it was GPT-2 in 2019 that caught widespread attention because OpenAI claimed to have initially deemed it too powerful to release publicly, out of fear of malicious use.

GPT-3 in 2020 went a step further and as of 2025 is available only via API with no offering of downloading the model to execute locally. But it was the 2022 consumer-facing chatbot ChatGPT that received extensive media coverage and public attention.

The 2023 GPT-4 was praised for its increased accuracy and as a "holy grail" for its multimodal capabilities. OpenAI did not reveal the high-level architecture and the number of parameters of GPT-4.

### Recent Developments

Since 2022, source-available models have been gaining popularity, especially at first with BLOOM and LLaMA, though both have restrictions on the field of use. Mistral AI's models Mistral 7B and Mixtral 8x7b have the more permissive Apache License.

Since 2023, many LLMs have been trained to be multimodal, having the ability to also process or generate other types of data, such as images or audio. These LLMs are also called large multimodal models (LMMs).

## Dataset Preprocessing

### Tokenization

As machine learning algorithms process numbers rather than text, the text must be converted to numbers. In the first step, a vocabulary is decided upon, then integer indices are arbitrarily but uniquely assigned to each vocabulary entry, and finally, an embedding is associated to the integer index.

Algorithms include byte-pair encoding (BPE) and WordPiece. There are also special tokens serving as control characters, such as `[MASK]` for masked-out token (as used in BERT), and `[UNK]` ("unknown") for characters not appearing in the vocabulary.

For example, the BPE tokenizer used by GPT-3 (Legacy) would split "tokenizer: texts -> series of numerical 'tokens'" as:

```
token	izer	:	 texts	 ->	series	 of	 numerical	 "	t	ok	ens	"
```

#### BPE (Byte Pair Encoding)

As an example, consider a tokenizer based on byte-pair encoding. In the first step, all unique characters (including blanks and punctuation marks) are treated as an initial set of n-grams (i.e. initial set of uni-grams). Successively the most frequent pair of adjacent characters is merged into a bi-gram and all instances of the pair are replaced by it.

#### Problems with Tokenization

A token vocabulary based on the frequencies extracted from mainly English corpora uses as few tokens as possible for an average English word. However, an average word in another language encoded by such an English-optimized tokenizer is split into a suboptimal amount of tokens.

### Dataset Cleaning

In the context of training LLMs, datasets are typically cleaned by removing low-quality, duplicated, or toxic data. Cleaned datasets can increase training efficiency and lead to improved downstream performance.

With the increasing proportion of LLM-generated content on the web, data cleaning in the future may include filtering out such content.

### Synthetic Data

Training of largest language models might need more linguistic data than naturally available, or that the naturally occurring data is of insufficient quality. In these cases, synthetic data might be used.

## Training

An LLM is a type of foundation model (large X model) trained on language. LLMs can be trained in different ways. In particular, GPT models are first pretrained to predict the next word on a large amount of data, before being fine-tuned.

### Pre-training Cost

The qualifier "large" in "large language model" is inherently vague, as there is no definitive threshold for the number of parameters required to qualify as "large". As time goes on, what was previously considered "large" may evolve.

As technology advanced, large sums have been invested in increasingly large models. For example:
- Training of GPT-2 (1.5-billion-parameters) in 2019 cost $50,000
- Training of PaLM (540-billion-parameters) in 2022 cost $8 million
- Megatron-Turing NLG 530B (2021) cost around $11 million

### Fine-tuning

Before being fine-tuned, most LLMs are next-token predictors. The fine-tuning can make LLM adopt a conversational format where they play the role of the assistant.

#### Instruction Fine-tuning

In 2021, Google Research released FLAN, a new model fine-tuned to follow a wide range of instructions. It could perform a task given a verbal instruction without needing any examples.

#### Reinforcement Learning from Human Feedback

RLHF involves training a reward model to predict which text humans prefer. Then, the LLM can be fine-tuned through reinforcement learning to better satisfy this reward model.

## Architecture

LLMs are generally based on the transformer architecture, which leverages an attention mechanism that enables the model to process relationships between all elements in a sequence simultaneously, regardless of their distance from each other.

### Attention Mechanism and Context Window

In order to find out which tokens are relevant to each other within the scope of the context window, the attention mechanism calculates "soft" weights for each token, more precisely for its embedding, by using multiple attention heads, each with its own "relevance" for calculating its own soft weights.

The largest models, such as Google's Gemini 1.5, presented in February 2024, can have a context window sized up to 1 million tokens. Other models with large context windows includes Anthropic's Claude 2.1, with a context window of up to 200k tokens.

### Mixture of Experts

A mixture of experts (MoE) is a machine learning architecture in which multiple specialized neural networks ("experts") work together, with a gating mechanism that routes each input to the most appropriate expert(s).

### Parameter Size

Typically, LLMs are trained with single- or half-precision floating point numbers (float32 and float16). One float16 has 16 bits, or 2 bytes, and so one billion parameters require 2 gigabytes.

### Quantization

Post-training quantization aims to decrease the space requirement by lowering precision of the parameters of a trained model, while preserving most of its performance.

### Infrastructure

Substantial infrastructure is necessary for training the largest models.
