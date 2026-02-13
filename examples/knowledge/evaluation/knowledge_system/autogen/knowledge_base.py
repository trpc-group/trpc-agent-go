"""
AutoGen (AG2) Knowledge Base Implementation.

Uses a tool-calling agent pattern with PGVectorDB backend, consistent with
the LangChain / CrewAI / Agno implementations. The agent is given a search
tool and the same 7-rule system prompt so that evaluation is comparable.
"""

import os
import sys
from typing import Callable, List, Optional, Tuple

import numpy as np
import tiktoken
import openai

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase, SearchResult


SYSTEM_PROMPT = (
    "You are a helpful assistant that answers questions using a knowledge base search tool.\n\n"
    "CRITICAL RULES(IMPORTANT !!!):\n"
    "1. You MUST call the search tool AT LEAST ONCE before answering. "
    "NEVER answer without searching first.\n"
    "2. Answer ONLY using information retrieved from the search tool.\n"
    "3. Do NOT add external knowledge, explanations, or context not found "
    "in the retrieved documents.\n"
    "4. Do NOT provide additional details, synonyms, or interpretations "
    "beyond what is explicitly stated in the search results.\n"
    "5. Use the search tool at most 3 times. If you haven't found the answer "
    "after 3 searches, provide the best answer from what you found.\n"
    "6. Be concise and stick strictly to the facts from the retrieved information.\n"
    "7. Give only the direct answer.\n"
    "8. IMPORTANT: After giving your final answer, you MUST reply with TERMINATE "
    "as the very last word of your message. Example: 'The answer is X. TERMINATE'"
)


def _create_openai_embedding_function(
    model: str,
    api_key: str,
    base_url: str,
) -> Callable:
    """Create an embedding function using OpenAI-compatible API."""
    client = openai.OpenAI(api_key=api_key, base_url=base_url)

    def embed(texts, **kwargs):
        single = isinstance(texts, str)
        if single:
            texts = [texts]
        response = client.embeddings.create(input=texts, model=model)
        result = [item.embedding for item in response.data]
        return np.array(result[0]) if single else np.array(result)

    return embed


def _safe_count_token(text: str, model: str = "gpt-3.5-turbo-0613") -> int:
    """Token counter that handles special tokens like <|endoftext|>."""
    enc = tiktoken.get_encoding("cl100k_base")
    return len(enc.encode(text, disallowed_special=()))


def _patch_autogen_token_counter():
    """Patch autogen's internal count_token to handle special tokens.

    This avoids needing custom_text_split_function, so autogen's native
    split_text_to_chunks (multi_lines mode with overlap) is used instead
    of a naive fixed-size chunking.
    """
    import autogen.token_count_utils as _tc
    import autogen.retrieve_utils as _ru

    def _patched_num_token_from_text(text, model="gpt-3.5-turbo-0613"):  # noqa: ARG001
        enc = tiktoken.get_encoding("cl100k_base")
        return len(enc.encode(text, disallowed_special=()))

    _tc._num_token_from_text = _patched_num_token_from_text
    _ru.count_token = _tc.count_token


_patch_autogen_token_counter()


class AutoGenKnowledgeBase(KnowledgeBase):
    """A knowledge base using AutoGen tool-calling agent with PGVectorDB."""

    def __init__(
        self,
        embedding_model: Optional[str] = None,
        llm_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        pg_connection: Optional[str] = None,
        collection_name: str = "autogen_docs",
        chunk_size: int = 500,
        chunk_overlap: int = 50,
        max_results: int = 4,
    ):
        config = get_config()

        self._embedding_model = embedding_model or config["embedding_model"]
        self._llm_model = llm_model or config["model_name"]
        self._base_url = base_url or config["base_url"]
        self._api_key = api_key or config["api_key"]
        self._collection_name = collection_name
        self._chunk_size = chunk_size
        self._chunk_overlap = chunk_overlap
        self._max_results = max_results

        pg_conn = pg_connection or config["pg_connection"]
        self._pg_connection = pg_conn.replace("postgresql+psycopg://", "postgresql://")

        self._embedding_function = _create_openai_embedding_function(
            model=self._embedding_model,
            api_key=self._api_key,
            base_url=self._base_url,
        )

        self._init_vectordb()

        self._last_search_results: List[SearchResult] = []

    def _init_vectordb(self):
        """Initialize PGVectorDB."""
        from autogen.agentchat.contrib.vectordb.pgvectordb import PGVectorDB

        self._db = PGVectorDB(
            connection_string=self._pg_connection,
            embedding_function=self._embedding_function,
        )
        self._db.create_collection(
            collection_name=self._collection_name,
            overwrite=False,
            get_or_create=True,
        )

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """Load documents using RetrieveUserProxyAgent's built-in chunking."""
        from autogen.agentchat.contrib.retrieve_user_proxy_agent import RetrieveUserProxyAgent

        if not file_paths:
            return

        llm_config = {
            "config_list": [{
                "model": self._llm_model,
                "api_key": self._api_key,
                "base_url": self._base_url,
            }],
            "temperature": 0,
            "timeout": 120,
        }

        retrieve_config = {
            "task": "qa",
            "vector_db": self._db,
            "collection_name": self._collection_name,
            "embedding_function": self._embedding_function,
            "overwrite": True,
            "get_or_create": False,
            "custom_token_count_function": _safe_count_token,
            "chunk_token_size": self._chunk_size,
            "must_break_at_empty_line": False,
            "docs_path": file_paths,
        }

        ragproxyagent = RetrieveUserProxyAgent(
            name="ragproxyagent",
            human_input_mode="NEVER",
            max_consecutive_auto_reply=1,
            retrieve_config=retrieve_config,
            code_execution_config=False,
        )

        print(f"   Loading {len(file_paths)} files via RetrieveUserProxyAgent...")
        ragproxyagent._init_db()
        print("   Documents loaded and indexed.")

    def search(self, query: str, k: int = 4) -> List[SearchResult]:
        """Search PGVectorDB for relevant documents."""
        results = self._db.retrieve_docs(
            queries=[query],
            collection_name=self._collection_name,
            n_results=k,
        )

        search_results = []
        if results and len(results) > 0:
            for doc, distance in results[0]:
                content = doc.get("content", "")
                if not content:
                    continue
                search_results.append(SearchResult(
                    content=content,
                    score=1.0 - distance if distance <= 1.0 else 1.0 / (1.0 + distance),
                    metadata=doc.get("metadata", {}),
                ))

        return search_results

    def answer(self, question: str, k: int = 4) -> Tuple[str, List[SearchResult]]:
        """
        Answer a question using a tool-calling agent with a search tool.

        Uses the same pattern as LangChain/CrewAI: the agent decides when
        to call the search tool, and all retrieved contexts are accumulated.
        """
        from autogen import AssistantAgent, UserProxyAgent, register_function

        self._last_search_results = []

        def search_knowledge_base(query: str) -> str:
            """Search the knowledge base for information relevant to the query."""
            results = self.search(query, k=k)
            self._last_search_results.extend(results)

            if not results:
                return "No relevant documents found."

            return "\n\n---\n\n".join(
                f"[Score: {r.score:.3f}]\n{r.content}" for r in results
            )

        llm_config = {
            "config_list": [{
                "model": self._llm_model,
                "api_key": self._api_key,
                "base_url": self._base_url,
            }],
            "temperature": 0,
            "timeout": 120,
        }

        assistant = AssistantAgent(
            name="assistant",
            system_message=SYSTEM_PROMPT,
            llm_config=llm_config,
        )

        user_proxy = UserProxyAgent(
            name="user_proxy",
            human_input_mode="NEVER",
            max_consecutive_auto_reply=5,
            code_execution_config=False,
        )

        register_function(
            search_knowledge_base,
            caller=assistant,
            executor=user_proxy,
            name="search_knowledge_base",
            description="Search the knowledge base for information relevant to the query.",
        )

        result = user_proxy.initiate_chat(
            assistant,
            message=question,
            silent=True,
            max_turns=5,
        )

        # Debug: print chat history structure
        print(f"   [AutoGen] Chat history ({len(result.chat_history)} messages):")
        for idx, msg in enumerate(result.chat_history):
            role = msg.get("role", "?")
            name = msg.get("name", "")
            content = msg.get("content", "")
            has_tc = bool(msg.get("tool_calls"))
            preview = (content[:120] + "...") if content and len(content) > 120 else content
            print(f"      [{idx}] role={role} name={name} tool_calls={has_tc} content={preview}")

        # Extract final answer from chat history.
        # In autogen, chat_history is from user_proxy's perspective:
        #   - Messages FROM the assistant agent have name="assistant"
        #   - Messages FROM user_proxy have name="user_proxy"
        # So we match on name="assistant" (not role) to find the LLM's answers.
        answer_text = ""
        for msg in reversed(result.chat_history):
            name = msg.get("name", "")
            content = msg.get("content", "")
            tool_calls = msg.get("tool_calls")
            if name == "assistant" and content and content.strip() and not tool_calls:
                answer_text = content.strip()
                break

        # Strip TERMINATE and any trailing tokens (e.g. deepseek's EOS token)
        term_idx = answer_text.find("TERMINATE")
        if term_idx >= 0:
            answer_text = answer_text[:term_idx].strip()

        print(f"   [AutoGen] Answer: {answer_text[:200]}{'...' if len(answer_text) > 200 else ''}")
        print(f"   [AutoGen] Contexts count: {len(self._last_search_results)}")
        for i, sr in enumerate(self._last_search_results, 1):
            preview = sr.content[:150] if sr.content else ""
            print(f"   [AutoGen] Context[{i}] (score={sr.score:.3f}): {preview}...")

        return answer_text, self._last_search_results
