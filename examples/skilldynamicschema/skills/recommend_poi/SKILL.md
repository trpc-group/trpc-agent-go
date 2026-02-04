---
name: recommend_poi
description: Recommend a POI in a city (schema + deterministic result).
---

# recommend_poi

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
