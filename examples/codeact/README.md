# Tool Code Orchestration Agent

This example demonstrates an agent that creates a product quote by using
`execute_tool_code` to orchestrate several dependent tools.

The model sees only `execute_tool_code`. Generated Python can call the
explicitly registered business tools through `await call_tool(...)`:

```text
LLM -> execute_tool_code -> generated Python -> allowlisted Go tools
```

For the quote request, the generated code searches the catalog, checks stock
for every product, filters unavailable items, creates one quote, and returns a
structured result.

One successful run generated code equivalent to the following. The exact code
is model-generated and can vary, but the only host calls available to it are
the explicitly registered `call_tool(...)` calls:

```python
REQUESTED_QTY = 3
QUERY = "ergonomic office chair"
CUSTOMER_ID = "customer-42"

catalog = await call_tool("search_catalog", query=QUERY)
selected = []
rejected = []

for product in catalog.get("products", []):
    inventory = await call_tool("get_inventory", sku=product["sku"])
    item = {
        "sku": product["sku"],
        "name": product["name"],
        "unit_price": product["unit_price"],
        "available": inventory["available"],
        "requested_quantity": REQUESTED_QTY,
    }
    if inventory["available"] >= REQUESTED_QTY:
        selected.append(item)
    else:
        item["reason"] = "insufficient_stock"
        rejected.append(item)

quote = await call_tool(
    "create_quote",
    customer_id=CUSTOMER_ID,
    items=[{"sku": item["sku"], "quantity": REQUESTED_QTY} for item in selected],
)

return {
    "selected_products": selected,
    "rejected_products": rejected,
    "quote": quote,
}
```

## Prerequisites

- Go 1.23.0 or later
- Python 3, used by the local development runtime
- A model endpoint compatible with the configured OpenAI adapter

## Run

From the `examples` module, configure the model credentials and run:

```bash
cd examples
export OPENAI_API_KEY="your-api-key"
go run ./codeact -model gpt-5
```

Set `OPENAI_BASE_URL` when using a compatible endpoint with a non-default base
URL. Pass `-prompt` to replace the default quote request.

## Key Design Point

The business tools are passed explicitly to `toolcode.NewTool`:

```go
orchestrator, err := toolcode.NewTool(
	bridge.LocalRunner{},
	[]tool.CallableTool{searchCatalog, getInventory, createQuote},
)
```

They are not inferred from the agent's direct tool set. The model cannot invoke
them as normal function calls; it can only invoke the outer
`execute_tool_code` tool. The host-side gateway validates each generated tool
call against this allowlist and the tool schemas.

Managed calls are synchronous direct host-capability calls in this first
version. They do not replay the agent callback, retry, or inner tracing
lifecycle, and cannot pause for interactive approval. Keep tools that require
an approval/resume flow out of this registry.

## Security

This example uses `LocalRunner` only to keep the example easy to run. It starts
a local Python process and is not a security sandbox. Use an isolated runtime,
such as a constrained container or an application-provided remote sandbox,
when generated code is not trusted.
