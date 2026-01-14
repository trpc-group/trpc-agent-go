"""
Utility functions for RAG evaluation.
"""

import os
from urllib.parse import quote_plus

# Default configuration values
DEFAULT_EMBEDDING_MODEL = "text-embedding-ada-002"
DEFAULT_MODEL_NAME = "gpt-3.5-turbo"
DEFAULT_OPENAI_BASE_URL = "https://api.openai.com/v1"

# PGVector defaults
DEFAULT_PGVECTOR_HOST = "127.0.0.1"
DEFAULT_PGVECTOR_PORT = "5432"
DEFAULT_PGVECTOR_USER = "root"
DEFAULT_PGVECTOR_PASSWORD = ""
DEFAULT_PGVECTOR_DATABASE = "vector"


def get_pg_connection() -> str:
    """
    Build PostgreSQL connection string from environment variables.

    Environment variables:
        PGVECTOR_HOST: PostgreSQL host (default: 127.0.0.1)
        PGVECTOR_PORT: PostgreSQL port (default: 5432)
        PGVECTOR_USER: PostgreSQL user (default: root)
        PGVECTOR_PASSWORD: PostgreSQL password (default: empty)
        PGVECTOR_DATABASE: PostgreSQL database (default: vector)

    Returns:
        PostgreSQL connection string for SQLAlchemy.
    """
    host = os.environ.get("PGVECTOR_HOST", DEFAULT_PGVECTOR_HOST)
    port = os.environ.get("PGVECTOR_PORT", DEFAULT_PGVECTOR_PORT)
    user = os.environ.get("PGVECTOR_USER", DEFAULT_PGVECTOR_USER)
    password = os.environ.get("PGVECTOR_PASSWORD", DEFAULT_PGVECTOR_PASSWORD)
    database = os.environ.get("PGVECTOR_DATABASE", DEFAULT_PGVECTOR_DATABASE)

    if not password:
        print(f"WARNING: PGVECTOR_PASSWORD not set, using empty password")
        print(f"  Current env vars: PGVECTOR_HOST={host}, PGVECTOR_USER={user}, PGVECTOR_DATABASE={database}")

    encoded_user = quote_plus(user)
    encoded_password = quote_plus(password)

    return f"postgresql+psycopg://{encoded_user}:{encoded_password}@{host}:{port}/{database}"


def get_config():
    """
    Get configuration from environment variables with defaults.

    Environment variables:
        EMBEDDING_MODEL: Embedding model name (default: text-embedding-ada-002)
        MODEL_NAME: LLM model name (default: gpt-3.5-turbo)
        OPENAI_API_KEY: OpenAI API key (required)
        OPENAI_BASE_URL: OpenAI API base URL (default: https://api.openai.com/v1)
        PGVECTOR_*: PostgreSQL connection parameters

    Returns:
        Dict with configuration values.
    """
    return {
        "embedding_model": os.environ.get("EMBEDDING_MODEL", DEFAULT_EMBEDDING_MODEL),
        "model_name": os.environ.get("MODEL_NAME", DEFAULT_MODEL_NAME),
        "api_key": os.environ.get("OPENAI_API_KEY", ""),
        "base_url": os.environ.get("OPENAI_BASE_URL", DEFAULT_OPENAI_BASE_URL),
        "pg_connection": get_pg_connection(),
    }
