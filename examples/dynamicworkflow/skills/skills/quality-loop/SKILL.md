---
name: quality-loop
description: Use this workflow recipe when a draft, plan, proposal, or other deliverable should be independently reviewed and revised until it satisfies explicit quality criteria.
---

# Bounded Quality Loop

Turn the user's request into a temporary, request-specific workflow. Preserve
the user's subject, constraints, and desired output rather than replacing them
with a fixed example.

## Process

1. Create a writer role that produces the requested deliverable.
2. Create a separate reviewer role. Give it the original request and the latest
   draft. On later reviews, also give it the previous required feedback so it
   can verify that the revision addressed those items. The writer must not
   review its own work.
3. Ask the reviewer for structured output using a small object schema:
   - `approved`: required boolean
   - `feedback`: required array containing only changes that must be made
   - no additional properties
4. If the user did not provide factual values such as dates, URLs, or contacts,
   accept clear placeholders. Do not reject solely because those values are not
   concrete, and do not ask the writer to invent them. Check that the
   placeholders are clear and the required fields or steps are complete.
5. Treat the result as approved only when `approved` is true and `feedback` is
   empty. If the fields disagree, the feedback wins.
6. If approved, stop immediately. If the reviewer rejects without actionable
   feedback, stop as unapproved instead of asking for an empty revision.
7. Otherwise, if another review is still available, pass the complete latest
   draft and every feedback item to the writer, then review the revision again.
8. Allow at most three reviews. After the third rejected review, stop without
   creating an unreviewed revision. This keeps the remaining feedback aligned
   with the returned draft.
9. Return the latest reviewed draft, approval status, number of reviews, and any
   remaining feedback.

## Illustrative workflow shape

Keep the loop explicit and bounded; the request supplies the actual content
and criteria.

```python
review_schema = {
    "type": "object",
    "properties": {
        "approved": {"type": "boolean"},
        "feedback": {"type": "array", "items": {"type": "string"}},
    },
    "required": ["approved", "feedback"],
    "additionalProperties": False,
}

draft = await agent(request, instruction="Write the first draft.", tools=[])
previous_feedback = []
for attempt in range(1, 4):
    review = await agent(
        {
            "request": request,
            "draft": draft["text"],
            "previous_feedback": previous_feedback,
        },
        instruction=(
            "Review against the requested criteria. If the user did not provide "
            "factual values such as dates, URLs, or contacts, clear placeholders "
            "are acceptable: do not reject solely because they are not concrete "
            "and do not ask the writer to invent facts. Check that placeholders "
            "are clear and required fields or steps are complete."
        ),
        schema=review_schema,
        tools=[],
    )
    decision = review["structured"]
    approved = decision["approved"] and not decision["feedback"]
    if approved or attempt == 3 or not decision["feedback"]:
        break
    previous_feedback = decision["feedback"]
    draft = await agent(
        {"draft": draft["text"], "feedback": previous_feedback},
        instruction="Revise the draft using every required change.",
        tools=[],
    )
return {
    "draft": draft["text"],
    "approved": approved,
    "reviews": attempt,
    "remaining_feedback": decision["feedback"],
}
```

On the third rejected review, return the latest reviewed draft and feedback;
do not create a fourth, unreviewed revision. Adapt the role instructions and
inputs to the current request rather than copying this shape as a fixed task.

## Review Discipline

- Review only against the user's original request and its explicit quality
  criteria. Do not invent a broader deliverable.
- When the user did not provide real names, dates, URLs, contacts, tools, or
  organization-specific facts, treat clear placeholders as acceptable. Do not
  reject solely for missing concrete values or ask the writer to invent them;
  check that the placeholders are clear and the required content is complete.
- Return at most three material required changes per review. Do not reject for
  optional polish.
- After a revision, first verify the previous required changes. Do not move the
  goalposts by adding unrelated requirements; add a new item only for a material
  regression introduced by the revision.
- Approve once the required criteria and previous feedback are satisfied.

## Compilation Rules

- Express the review cycle as an explicit bounded loop in one Dynamic Workflow.
- Use separate workflow-local Agent instances for writer and reviewer.
- Keep both roles tool-free unless the user's request clearly needs a capability
  already allowed by the registered Agent template.
- Use model-native structured output for the approval decision. Do not parse a
  JSON-looking text response.
- Read the branch fields from the Agent result's explicit `structured` object.
- Pass task facts and feedback through Agent inputs. Do not rely on a role to
  remember facts that were never provided to it.
