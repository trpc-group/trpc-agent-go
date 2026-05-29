# Tool Policy Example

This example shows how to combine tool metadata with a per-run permission
policy.

The example has two inventory tools:

- `read_inventory`: marked as read-only and search/read.
- `set_inventory`: marked as destructive.

The permission policy uses those metadata fields:

- `admin`: can run both tools.
- `operator`: can read inventory, but destructive updates return
  `approval_required`.
- `viewer`: can read inventory, but destructive updates return `denied`.

`ask` and `deny` decisions do not execute the tool. They return a structured
tool result to the model, so the assistant can explain what happened. If your
application has a real approval UI, ask the user inside the policy and return
`tool.AllowPermission()` only after approval.

## Run

```bash
export OPENAI_API_KEY="your-api-key"
go run . --role operator
```

Try:

```text
read the inventory
set notebook count to 8
```

Run as admin to allow the update:

```bash
go run . --role admin
```
