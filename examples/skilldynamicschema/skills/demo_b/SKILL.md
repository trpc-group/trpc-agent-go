---
name: demo_b
description: Print a JSON Schema and a deterministic JSON result (demo B).
---

# demo_b

This skill is used by the dynamic structured output demo.

## Output JSON Schema

```json
{
  "type": "object",
  "properties": {
    "poi": {
      "type": "string",
      "description": "Point of interest"
    },
    "city": {
      "type": "string",
      "description": "City name"
    },
    "score": {
      "type": "integer",
      "description": "A deterministic score"
    }
  },
  "required": [
    "poi",
    "city",
    "score"
  ],
  "additionalProperties": false
}
```

## Commands

Print JSON result to stdout:

```bash
cat result.json
```
