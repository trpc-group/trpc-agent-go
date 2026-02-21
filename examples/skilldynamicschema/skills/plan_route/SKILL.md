---
name: plan_route
description: Plan a route and return distance + ETA (schema + deterministic result).
---

# plan_route

This skill is used by the dynamic structured output demo.

## Output JSON Schema

```json
{
  "type": "object",
  "properties": {
    "route": {
      "type": "string",
      "description": "Route name"
    },
    "distance_km": {
      "type": "number",
      "description": "Distance in kilometers"
    },
    "eta_min": {
      "type": "integer",
      "description": "ETA in minutes"
    }
  },
  "required": [
    "route",
    "distance_km",
    "eta_min"
  ],
  "additionalProperties": false
}
```

## Commands

Print JSON result to stdout:

```bash
cat result.json
```
