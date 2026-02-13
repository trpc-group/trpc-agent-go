"""
CrewAI Knowledge Base Implementation.

This module provides a RAG knowledge base using CrewAI (v1.9.0+) native Knowledge
system with ChromaDB for answering questions.
"""

import os
import shutil
import sys
from typing import List, Optional, Tuple

import litellm

# Register model with function calling support BEFORE importing CrewAI.
# This fixes the issue where LiteLLM doesn't recognize deepseek-v3.2
# and defaults to ReAct mode instead of function calling.
litellm.register_model({
    "openai/deepseek-v3.2": {
        "max_tokens": 8192,
        "max_input_tokens": 65536,
        "max_output_tokens": 8192,
        "litellm_provider": "openai",
        "mode": "chat",
        "supports_function_calling": True,
        "supports_parallel_function_calling": True,
    }
})

from crewai import Agent, Task, Crew, LLM, Process
from crewai.tools import tool

# Monkey patch to fix CrewAI bug where tool_calls are ignored when content is present.
# deepseek-v3.2 returns both content AND tool_calls, but CrewAI's logic:
#   if (not tool_calls or not available_functions) and text_response:
#       return text_response
# prioritizes text_response over tool_calls when available_functions is None.
from crewai.events.event_bus import crewai_event_bus
from crewai.events.types.llm_events import LLMCallType

def _patched_handle_non_streaming_response(self, params, callbacks, available_functions, from_task, from_agent, response_model):
    """Patched version that prioritizes tool_calls over text_response."""
    # Make the completion call
    response = litellm.completion(**params)

    response_message = response.choices[0].message
    text_response = response_message.content or ""

    # Track token usage
    if hasattr(response, "usage") and response.usage:
        self._track_token_usage_internal(response.usage)

    # Handle callbacks
    if callbacks and len(callbacks) > 0:
        for callback in callbacks:
            if hasattr(callback, "log_success_event"):
                usage_info = getattr(response, "usage", None)
                if usage_info:
                    callback.log_success_event(
                        kwargs=params,
                        response_obj={"usage": usage_info},
                        start_time=0,
                        end_time=0,
                    )

    tool_calls = getattr(response_message, "tool_calls", [])

    # PATCHED: Prioritize tool_calls over text_response
    # Return tool_calls if present, regardless of text_response
    if tool_calls and not available_functions:
        # Emit event before returning tool_calls to maintain event pairing
        self._handle_emit_call_events(
            response=tool_calls,
            call_type=LLMCallType.TOOL_CALL,
            from_task=from_task,
            from_agent=from_agent,
            messages=params["messages"],
        )
        return tool_calls

    if tool_calls and available_functions:
        tool_result = self._handle_tool_call(
            tool_calls, available_functions, from_task, from_agent
        )
        if tool_result is not None:
            return tool_result

    # Only return text_response if no tool_calls
    self._handle_emit_call_events(
        response=text_response,
        call_type=LLMCallType.LLM_CALL,
        from_task=from_task,
        from_agent=from_agent,
        messages=params["messages"],
    )
    return text_response

# Apply the patch
LLM._handle_non_streaming_response = _patched_handle_non_streaming_response

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase, SearchResult


AGENT_INSTRUCTIONS = """You are a helpful assistant that answers questions using a knowledge base search tool.

CRITICAL RULES(IMPORTANT !!!):
1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.
2. Answer ONLY using information retrieved from the search tool.
3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
6. Be concise and stick strictly to the facts from the retrieved information.
7. Give only the direct answer."""


class CrewAIKnowledgeBase(KnowledgeBase):
    """A knowledge base using CrewAI native Knowledge system with ChromaDB."""

    def __init__(
        self,
        embedding_model: Optional[str] = None,
        llm_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        chromadb_path: Optional[str] = None,
        chroma_api_key: Optional[str] = None,
        chroma_api_base: Optional[str] = None,
        collection_name: str = "crewai_docs",
        chunk_size: int = 500,
        chunk_overlap: int = 50,
        max_results: int = 4,
    ):
        """
        Initialize the knowledge base.

        Args:
            embedding_model: OpenAI embedding model name. Defaults to EMBEDDING_MODEL env var.
            llm_model: OpenAI LLM model name. Defaults to MODEL_NAME env var.
            base_url: OpenAI API base URL. Defaults to OPENAI_BASE_URL env var.
            api_key: OpenAI API key. Defaults to OPENAI_API_KEY env var.
            chromadb_path: Path to ChromaDB storage. Defaults to CHROMADB_PATH env var.
            collection_name: Name of the collection in ChromaDB.
            chunk_size: Size of text chunks for splitting.
            chunk_overlap: Overlap between chunks.
            max_results: Default number of documents to retrieve per search.
        """
        config = get_config()

        self._embedding_model = embedding_model or config["embedding_model"]
        self._llm_model = llm_model or config["model_name"]
        self._base_url = base_url or config["base_url"]
        self._api_key = api_key or config["api_key"]
        self._chromadb_path = chromadb_path or config["chromadb"]["path"]
        self._chroma_api_key = chroma_api_key or config["chromadb"].get("api_key", self._api_key)
        self._chroma_api_base = chroma_api_base or config["chromadb"].get("api_base", self._base_url)
        self._collection_name = collection_name
        self._chunk_size = chunk_size
        self._chunk_overlap = chunk_overlap
        self._max_results = max_results

        # Initialize CrewAI LLM (v1.9.0+ uses LLM class)
        self._llm = LLM(
            model=f"openai/{self._llm_model}",
            base_url=self._base_url,
            api_key=self._api_key,
            temperature=0,
        )

        # Embedder configuration for CrewAI native knowledge
        self._embedder_config = {
            "provider": "openai",
            "config": {
                "model": self._embedding_model,
                "api_key": self._api_key,
                "api_base": self._base_url,
            }
        }

        self._last_search_results: List[SearchResult] = []
        self._loaded_file_paths: List[str] = []
        self.last_trace: Optional[dict] = None

    def _split_text(self, text: str) -> List[str]:
        """Split text into chunks with overlap similar to LangChain splitter."""
        if not text:
            return []
        chunks = []
        step = self._chunk_size - self._chunk_overlap
        if step <= 0:
            step = self._chunk_size
        for start in range(0, len(text), step):
            end = start + self._chunk_size
            chunks.append(text[start:end])
            if end >= len(text):
                break
        return chunks

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """
        Load documents from file paths into the knowledge base with chunking.

        Args:
            file_paths: List of file paths to load.
            metadatas: Optional list of metadata dicts for each file.
        """
        import chromadb
        from chromadb.utils import embedding_functions

        knowledge_storage_path = os.path.join(self._chromadb_path, "knowledge")

        if os.path.exists(knowledge_storage_path):
            try:
                shutil.rmtree(knowledge_storage_path)
                print(f"   Cleared existing knowledge storage at {knowledge_storage_path}")
            except Exception as e:
                print(f"   Warning: Could not clear storage: {e}")
        
        os.makedirs(knowledge_storage_path, exist_ok=True)

        self._loaded_file_paths = file_paths

        embedding_fn = embedding_functions.OpenAIEmbeddingFunction(
            model_name=self._embedding_model,
            api_key=self._chroma_api_key,
            api_base=self._chroma_api_base,
        )
        client = chromadb.PersistentClient(path=knowledge_storage_path)
        collection = client.get_or_create_collection(
            name=self._collection_name,
            embedding_function=embedding_fn,
            metadata={"source": "crewai"},
        )

        documents: List[str] = []
        metadatas_to_store: List[dict] = []
        ids: List[str] = []

        for i, filepath in enumerate(file_paths):
            with open(filepath, "r", encoding="utf-8") as f:
                content = f.read()

            base_meta = {"source": os.path.basename(filepath)}
            if metadatas and i < len(metadatas) and metadatas[i]:
                base_meta.update(metadatas[i])

            chunks = self._split_text(content)
            for chunk_idx, chunk in enumerate(chunks):
                documents.append(chunk)
                meta = dict(base_meta)
                meta["chunk_index"] = chunk_idx
                metadatas_to_store.append(meta)
                ids.append(f"{os.path.basename(filepath)}-{i}-{chunk_idx}")

        if documents:
            # Add documents in batches to avoid request body size limit
            batch_size = 50
            total = len(documents)
            total_batches = (total + batch_size - 1) // batch_size
            print(f"   Split {len(file_paths)} files into {len(documents)} chunks")
            
            for i in range(0, total, batch_size):
                batch_docs = documents[i:i + batch_size]
                batch_metas = metadatas_to_store[i:i + batch_size]
                batch_ids = ids[i:i + batch_size]
                try:
                    collection.add(
                        documents=batch_docs,
                        metadatas=batch_metas,
                        ids=batch_ids
                    )
                    print(f"   Added batch {i // batch_size + 1}/{total_batches} ({len(batch_docs)} chunks)")
                except Exception as e:
                    print(f"   ERROR in batch {i // batch_size + 1}/{total_batches}: {e}")
                    if batch_docs:
                        print(f"   First doc length: {len(batch_docs[0])} chars")
                    raise
        else:
            print("   Warning: No documents were loaded (empty files or no content)")

    def search(self, query: str, k: Optional[int] = None) -> List[SearchResult]:
        """
        Search for relevant documents with similarity scores.

        Note: CrewAI native knowledge doesn't expose direct search API.
        This method uses ChromaDB directly for search functionality.

        Args:
            query: Search query string.
            k: Number of documents to return.

        Returns:
            List of SearchResult objects.
        """
        try:
            import chromadb
            from chromadb.utils import embedding_functions
            
            effective_k = k or self._max_results

            knowledge_path = os.path.join(self._chromadb_path, "knowledge")
            if not os.path.exists(knowledge_path):
                return []
            
            client = chromadb.PersistentClient(path=knowledge_path)
            embedding_fn = embedding_functions.OpenAIEmbeddingFunction(
                model_name=self._embedding_model,
                api_key=self._chroma_api_key,
                api_base=self._chroma_api_base,
            )
            collection = client.get_or_create_collection(
                name=self._collection_name,
                embedding_function=embedding_fn,
            )
            if collection.count() == 0:
                return []

            search_results = collection.query(
                query_texts=[query],
                n_results=min(effective_k, collection.count()),
                include=["documents", "distances", "metadatas"],
            )

            docs = search_results.get("documents", [[]])[0]
            distances = search_results.get("distances", [[]])[0]
            metadatas = search_results.get("metadatas", [[]])[0]

            results: List[SearchResult] = []
            for i, doc in enumerate(docs):
                distance = distances[i] if i < len(distances) else None
                score = 1.0 - distance if distance is not None else 0.5
                if score < 0:
                    score = 0.0
                if score > 1:
                    score = 1.0
                meta = metadatas[i] if i < len(metadatas) else {}
                results.append(SearchResult(content=doc, score=score, metadata=meta))

            return results
        except Exception as e:
            print(f"   Search error: {e}")
            return []

    def answer(self, question: str, k: Optional[int] = None) -> Tuple[str, List[SearchResult]]:
        """
        Answer a question using CrewAI Agent with native Knowledge.

        Args:
            question: The question to answer.
            k: Number of documents to retrieve for context.

        Returns:
            Tuple of (answer_text, search_results).
        """
        self._last_search_results = []
        last_results = self._last_search_results
        effective_k = k or self._max_results
        tool_queries: List[str] = []

        # Create search tool that uses ChromaDB directly
        @tool("search_knowledge_base")
        def search_knowledge_base(query: str) -> str:
            """this is a search tool that help search information you need. It's your knowledgebase, you search information by the tool to answer user's question."""
            normalized_query = query.strip()
            if normalized_query:
                tool_queries.append(normalized_query)
            results = self.search(query, k=effective_k)
            for result in results:
                metadata = dict(result.metadata) if isinstance(result.metadata, dict) else {}
                if normalized_query:
                    metadata["tool_query"] = normalized_query
                last_results.append(
                    SearchResult(
                        content=result.content,
                        score=result.score,
                        metadata=metadata,
                    )
                )

            if not results:
                return "No relevant documents found."

            return "\n\n---\n\n".join(
                f"[Score: {r.score:.3f}]\n{r.content}" for r in results
            )

        # NOTE: Pre-fetch disabled to match other implementations' behavior
        # # Pre-fetch search results to ensure we always have context
        # # This guarantees last_results is populated even if Agent skips tool call
        # initial_results = self.search(question, k=effective_k)
        # last_results.extend(initial_results)

        # Create CrewAI agent with native knowledge sources
        researcher = Agent(
            role="Knowledge Base Researcher",
            goal="",
            backstory=AGENT_INSTRUCTIONS,
            tools=[search_knowledge_base],
            llm=self._llm,
            verbose=False,
            allow_delegation=False,
            max_iter=5,
            embedder=self._embedder_config,
        )

        # Create task
        research_task = Task(
            description=f"Answer this question: {question}",
            expected_output="A concise answer based strictly on the knowledge base search results.",
            agent=researcher,
        )

        # Create and run crew with embedder config
        crew = Crew(
            agents=[researcher],
            tasks=[research_task],
            verbose=False,
            process=Process.sequential,
            embedder=self._embedder_config,
        )

        result = crew.kickoff()
        answer = str(result) if result else ""

        trace = {"tool_queries": tool_queries}
        self.last_trace = trace
        for search_result in self._last_search_results:
            search_result.trace = trace

        return answer, self._last_search_results
