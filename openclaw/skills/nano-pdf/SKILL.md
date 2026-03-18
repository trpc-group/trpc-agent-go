---
name: nano-pdf
description: >
  Edit PDF pages with natural-language instructions using the nano-pdf CLI.
  Supports changing text, fixing typos, updating titles, and modifying content
  on specific pages. Use when asked to edit a PDF, modify PDF content, change
  PDF text, update a PDF page, or fix something in a PDF document.
homepage: https://pypi.org/project/nano-pdf/
metadata:
  {
    "openclaw":
      {
        "emoji": "📄",
        "requires": { "bins": ["nano-pdf"] },
        "install":
          [
            {
              "id": "uv",
              "kind": "uv",
              "package": "nano-pdf",
              "bins": ["nano-pdf"],
              "label": "Install nano-pdf (uv)",
            },
          ],
      },
  }
---

# nano-pdf

Edit a specific page in a PDF using a natural-language instruction.

## Quick start

```bash
nano-pdf edit <file.pdf> <page> "<instruction>"
```

Example:

```bash
nano-pdf edit deck.pdf 1 "Change the title to 'Q3 Results' and fix the typo in the subtitle"
```

## Verification

Open the output PDF and confirm the edit was applied to the correct page and the rest of the document is intact.

## Notes

- Page numbering may be 0-based or 1-based depending on version. if the edit lands on the wrong page, retry with the other numbering.
- Always verify output before sending or committing.
