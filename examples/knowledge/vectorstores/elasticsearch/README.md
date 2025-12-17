# Elasticsearch Vector Store Example

Demonstrates using Elasticsearch for scalable vector search.

## Prerequisites

1. Start Elasticsearch:

```bash
# Docker setup (recommended)
docker run -d \
  --name elasticsearch \
  -p 9200:9200 \
  -p 9300:9300 \
  -e "discovery.type=single-node" \
  -e "xpack.security.enabled=false" \
  docker.elastic.co/elasticsearch/elasticsearch:8.11.0
```

2. Set environment variables:

```bash
export ELASTICSEARCH_HOSTS=http://localhost:9200
export ELASTICSEARCH_INDEX_NAME=trpc_agent_go
export ELASTICSEARCH_VERSION=v9  # or v7, v8
export OPENAI_API_KEY=your-api-key
```

## Run

```bash
go run main.go
```

## Version Support

- **v7**: Elasticsearch 7.x
- **v8**: Elasticsearch 8.0-8.7
- **v9**: Elasticsearch 8.8+

## Benefits

- **High performance**: Optimized for large-scale searches
- **Distributed**: Scalable across multiple nodes
- **Advanced queries**: Rich filtering and aggregation
- **Production-ready**: Battle-tested in enterprise environments
