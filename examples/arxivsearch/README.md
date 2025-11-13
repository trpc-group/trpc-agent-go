# ArXiv Search Tool Example

This example demonstrates how to use the ArXiv search tool with an AI agent for interactive conversations. The tool uses ArXiv's API to search for scholarly articles in STEM fields including physics, mathematics, computer science, quantitative biology, quantitative finance, statistics, electrical engineering, systems science, and economics.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work. The AI agent will use the ArXiv search tool to provide scholarly article information.

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |

## Features

### 🔍 ArXiv Search Tool (`arxiv_search`)

The tool provides access to scholarly articles from arXiv repository:

**Input:**
```json
{
  "query": "string",
  "id_list": ["string"],
  "max_results": 5,
  "sort_by": "relevance",
  "sort_order": "descending",
  "read_arxiv_papers": false
}
```

**Output:**
```json
[
    {
      "title": "string",
      "id": "string",
      "entry_id": "string",
      "authors": ["string"],
      "primary_category": "string",
      "categories": ["string"],
      "published": "string",
      "pdf_url": "string",
      "links": ["string"],
      "summary": "string",
      "comment": "string",
      "content": [
        {
          "page": 1,
          "text": "string"
        }
      ]
    }
]
```

**What Works (Scholarly Article Search):**
- **Keyword Search**: Search by research topics, concepts, techniques
- **Author Search**: Find papers by specific authors or research groups
- **Category Search**: Browse by arXiv categories (cs.AI, cs.CV, math.NA, etc.)
- **ID Search**: Look up specific arXiv IDs (e.g., "2401.12345")
- **Date Filtering**: Search by publication date ranges
- **PDF Content**: Optionally read and extract text from PDF articles
- **Multi-field Search**: Combine title, abstract, author searches

**What Doesn't Work (Limitations):**
- **Real-time Updates**: arXiv updates daily, but search results reflect indexed content
- **Full-text Search**: Limited to metadata and abstracts without PDF reading
- **Citation Analysis**: No built-in citation metrics or impact factors
- **Journal Information**: arXiv is a preprint server, not peer-reviewed journals

## Running the Example

### Using environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Optional
go run main.go
```

### Using custom model:

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go -model gpt-4o-mini
```

### Example with different base URL (for OpenAI-compatible APIs):

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://generativelanguage.googleapis.com/v1beta/"
go run main.go -model gemini-2.5-flash
```

## Example Session

```
🚀 ArXiv Search Chat Demo
Model: gemini-2.5-flash
Type 'exit' to end the conversation
Available tools: arxiv_search
==================================================
✅ ArXiv chat ready! Session: arxiv-session-1762592478

💡 Try asking questions like:
   - Search for machine learning papers from 2024
   - Find recent papers about transformers in NLP
   - Look up papers by author Yann LeCun
   - Search for quantum computing research papers
   - Find computer vision papers from CVPR 2024
   - Search for arXiv ID 2401.12345

ℹ️  Note: ArXiv contains scholarly articles in STEM fields

👤 You: Search for arXiv ID 2401.12345
🤖 Assistant: 🔍 ArXiv initiated:
   • arxiv_search (ID: function-call-1086826686015953936)
     Query: {"read_arxiv_papers":false,"search":{"id_list":["2401.12345"]}}

🔄 Searching arXiv...
✅ Search results (ID: function-call-1086826686015953936): [{"title":"Distributionally Robust Receive Combining","id":"2401.12345v3","entry_id":"http://arxiv.org/abs/2401.12345v3","authors":["Shixiong Wang","Wei Dai","Geoffrey Ye Li"],"primary_category":"eess.SP","categories":["eess.SP"],"published":"2024-01-22T20:20:48Z","pdf_url":"http://arxiv.org/pdf/2401.12345v3","links":["http://dx.doi.org/10.1109/TSP.2025.3582082","http://arxiv.org/abs/2401.12345v3","http://arxiv.org/pdf/2401.12345v3"],"summary":"This article investigates signal estimation in wireless transmission (i.e.,\nreceive combining) from the perspective of statistical machine learning, where\nthe transmit signals may be from an integrated sensing and communication\nsystem; that is, 1) signals may be not only discrete constellation points but\nalso arbitrary complex values; 2) signals may be spatially correlated.\nParticular attention is paid to handling various uncertainties such as the\nuncertainty of the transmit signal covariance, the uncertainty of the channel\nmatrix, the uncertainty of the channel noise covariance, the existence of\nchannel impulse noises, the non-ideality of the power amplifiers, and the\nlimited sample size of pilots. To proceed, a distributionally robust receive\ncombining framework that is insensitive to the above uncertainties is proposed,\nwhich reveals that channel estimation is not a necessary operation. For optimal\nlinear estimation, the proposed framework includes several existing combiners\nas special cases such as diagonal loading and eigenvalue thresholding. For\noptimal nonlinear estimation, estimators are limited in reproducing kernel\nHilbert spaces and neural network function spaces, and corresponding\nuncertainty-aware solutions (e.g., kernelized diagonal loading) are derived. In\naddition, we prove that the ridge and kernel ridge regression methods in\nmachine learning are distributionally robust against diagonal perturbation in\nfeature covariance.","comment":"","content":null}]

🤖 Assistant: The article "Distributionally Robust Receive Combining" (arXiv ID: 2401.12345) by Shixiong Wang, Wei Dai, and Geoffrey Ye Li, published on January 22, 2024, investigates signal estimation in wireless transmission from the perspective of statistical machine learning. The authors propose a distributionally robust receive combining framework to handle various uncertainties such as transmit signal covariance, channel matrix, channel noise covariance, channel impulse noises, non-ideality of power amplifiers, and limited pilot sample size. The framework shows that channel estimation is not a necessary operation. For optimal linear estimation, it includes existing combiners like diagonal loading and eigenvalue thresholding as special cases. For optimal nonlinear estimation, uncertainty-aware solutions like kernelized diagonal loading are derived for estimators in reproducing kernel Hilbert spaces and neural network function spaces. The article also proves that ridge and kernel ridge regression methods are distributionally robust against diagonal perturbation in feature covariance.

You can access the PDF at http://arxiv.org/pdf/2401.12345v3.

👤 You: Search for machine learning papers from 2024
🤖 Assistant: 🔍 ArXiv initiated:
   • arxiv_search (ID: function-call-3584367795297975575)
     Query: {"read_arxiv_papers":false,"search":{"max_results":10,"query":"cat:cs.LG AND submittedDate:[20240101 TO 20241231]","sort_by":"submittedDate","sort_order":"descending"}}

🔄 Searching arXiv...
✅ Search results (ID: function-call-3584367795297975575): [{"title":"TrajLearn: Trajectory Prediction Learning using Deep Generative Models","id":"2501.00184v2","entry_id":"http://arxiv.org/abs/2501.00184v2","authors":["Amirhossein Nadiri","Jing Li","Ali Faraji","Ghadeer Abuoda","Manos Papagelis"],"primary_category":"cs.LG","categories":["cs.LG","cs.CV","cs.RO"],"published":"2024-12-30T23:38:52Z","pdf_url":"http://arxiv.org/pdf/2501.00184v2","links":["http://arxiv.org/abs/2501.00184v2","http://arxiv.org/pdf/2501.00184v2"],"summary":"Trajectory prediction aims to estimate an entity's future path using its\ncurrent position and historical movement data, benefiting fields like\nautonomous navigation, robotics, and human movement analytics. Deep learning\napproaches have become key in this area, utilizing large-scale trajectory\ndatasets to model movement patterns, but face challenges in managing complex\nspatial dependencies and adapting to dynamic environments. To address these\nchallenges, we introduce TrajLearn, a novel model for trajectory prediction\nthat leverages generative modeling of higher-order mobility flows based on\nhexagonal spatial representation. TrajLearn predicts the next $k$ steps by\nintegrating a customized beam search for exploring multiple potential paths\nwhile maintaining spatial continuity. We conducted a rigorous evaluation of\nTrajLearn, benchmarking it against leading state-of-the-art approaches and\nmeaningful baselines. The results indicate that TrajLearn achieves significant\nperformance gains, with improvements of up to ~40% across multiple real-world\ntrajectory datasets. In addition, we evaluated different prediction horizons\n(i.e., various values of $k$), conducted resolution sensitivity analysis, and\nperformed ablation studies to assess the impact of key model components.\nFurthermore, we developed a novel algorithm to generate mixed-resolution maps\nby hierarchically subdividing hexagonal regions into finer segments within a\nspecified ...

🤖 Assistant: Here are 10 machine learning papers published in 2024:

1.  **TrajLearn: Trajectory Prediction Learning using Deep Generative Models** by Amirhossein Nadiri, Jing Li, Ali Faraji, Ghadeer Abuoda, and Manos Papagelis. Published on December 30, 2024. This paper introduces TrajLearn, a novel model for trajectory prediction that leverages generative modeling of higher-order mobility flows based on hexagonal spatial representation.
2.  **Federated Learning with Workload Reduction through Partial Training of Client Models and Entropy-Based Data Selection** by Hongrui Shi, Valentin Radu, and Po Yang. Published on December 30, 2024. This work proposes FedFT-EDS, a novel approach combining fine-tuning of partial client models with entropy-based data selection to reduce training workloads on edge devices in Federated Learning.
3.  **Class-based Subset Selection for Transfer Learning under Extreme Label Shift** by Akul Goyal and Carl Edwards. Published on December 30, 2024. This paper proposes a new process for few-shot transfer learning that selects and weighs classes from the source domain to optimize the transfer between domains, especially under extreme label shift.
4.  **Urban Water Consumption Forecasting Using Deep Learning and Correlated District Metered Areas** by Kleanthis Malialis, Nefeli Mavri, Stelios G. Vrachimis, Marios S. Kyriakou, Demetrios G. Eliades, and Marios M. Polycarpou. Published on December 30, 2024. This work focuses on short-term forecasting of District Metered Area (DMA) water consumption using deep learning, addressing challenges of limited context and sensor malfunctions by incorporating correlated DMA consumption patterns.
5.  **LASSE: Learning Active Sampling for Storm Tide Extremes in Non-Stationary Climate Regimes** by Grace Jiang, Jiangchao Qiu, and Sai Ravela. Published on December 30, 2024. This paper presents an informative online learning approach to rapidly search for extreme storm tide-producing cyclones using only a few hydrodynamic simulations, which is efficient and scalable for large storm catalogs.
6.  **Detection-Fusion for Knowledge Graph Extraction from Videos** by Taniya Das, Louis Mahon, and Thomas Lukasiewicz. Published on December 30, 2024. This paper proposes a deep-learning-based model for annotating videos with knowledge graphs, predicting pairs of individuals and then the relations between them, and an extension for including background knowledge.
7.  **GroverGPT: A Large Language Model with 8 Billion Parameters for Quantum Searching** by Haoran Wang, Pingzhi Li, Min Chen, Jinglei Cheng, Junyu Liu, and Tianlong Chen. Published on December 30, 2024. This work explores leveraging Large Language Models (LLMs) to simulate the output of a quantum Turing machine using Grover's quantum circuits, introducing GroverGPT, a specialized model based on LLaMA.
8.  **PQD: Post-training Quantization for Efficient Diffusion Models** by Jiaojiao Ye, Zhen Wang, and Linnan Jiang. Published on December 30, 2024. This paper proposes a novel post-training quantization for diffusion models (PQD), a time-aware optimization framework that quantizes full-precision diffusion models into 8-bit or 4-bit models while maintaining comparable performance.
9.  **Post Launch Evaluation of Policies in a High-Dimensional Setting** by Shima Nassiri, Mohsen Bayati, and Joe Cooprider. Published on December 30, 2024. This paper explores practical considerations in applying methodologies inspired by "synthetic control" as an alternative to traditional A/B testing in settings with very large numbers of units, proposing a two-phase approach using nearest neighbor matching and supervised learning.
10. **Text-to-Image GAN with Pretrained Representations** by Xiaozhou Yo...

👤 You: exit
👋 Goodbye!
```

## How It Works

1. **Setup**: The example creates an LLM agent with access to the ArXiv search tool
2. **User Input**: Users can ask questions about scholarly articles and research
3. **Tool Detection**: The AI automatically decides when to use the search tool based on the query
4. **Search Execution**: The ArXiv tool performs the scholarly article search and returns structured results
5. **Response Generation**: The AI uses the search results to provide informed, research-focused responses

## API Design & Capabilities

### ArXiv Repository Coverage

ArXiv provides access to nearly 2.4 million scholarly articles across STEM fields:

**Main Categories:**
- **Computer Science**: AI, ML, CV, NLP, systems, theory
- **Physics**: Condensed matter, high-energy, quantum, astrophysics
- **Mathematics**: Algebra, analysis, statistics, numerical methods
- **Quantitative Biology**: Bioinformatics, computational biology
- **Quantitative Finance**: Financial mathematics, econometrics
- **Statistics**: Statistical theory, applications
- **Electrical Engineering**: Circuits, communications, signal processing
- **Economics**: Theoretical and applied economics

### Search Capabilities

**Basic Search Features:**
- **Keyword Matching**: Search in titles, abstracts, and full text (with PDF reading)
- **Field-specific Queries**: Author names, arXiv IDs, categories
- **Date Ranges**: Filter by publication date
- **Sorting Options**: Relevance, submission date, last updated
- **Multi-category Search**: Combine categories for interdisciplinary research

**Advanced Features:**
- **PDF Content Extraction**: Read and process article content when enabled
- **Citation Context**: Extract references and citation information
- **Multi-format Support**: Various metadata formats and content types

## Interactive Features

- **Streaming Response**: Real-time display of search process and results
- **Tool Visualization**: Clear indication when searches are performed
- **Multi-turn Conversation**: Maintains context across multiple searches
- **Error Handling**: Graceful handling of search failures or empty results
- **Research Context**: Maintains research topic context across conversations

## Research Use Cases

### Academic Research
- **Literature Review**: Find related work and state-of-the-art methods
- **Author Tracking**: Follow specific researchers' latest publications
- **Topic Exploration**: Discover emerging research areas and trends
- **Citation Analysis**: Understand research impact and connections

### Educational Applications
- **Course Material**: Find papers for specific courses or topics
- **Student Projects**: Source research papers for academic projects
- **Self-study**: Explore research areas for personal learning

### Professional Development
- **Industry Research**: Stay updated with academic advancements
- **Technology Trends**: Monitor developments in specific technical areas
- **Collaboration Opportunities**: Identify potential research collaborators

This example showcases how AI agents can be enhanced with scholarly search capabilities to provide accurate, research-focused information from the arXiv repository, making it valuable for academic and research applications.