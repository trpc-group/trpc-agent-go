# Visualization Example

This example shows how to export a graph to a Graphviz DOT file and render a PNG image using the new visualization helpers.

## What it demonstrates

- `graph.DOT(...)`: Build a DOT (Graphviz) representation with styling by node type
- `graph.RenderImage(...)`: Render PNG/SVG via the `dot` binary if available
- Display of
  - normal runtime edges (solid)
  - conditional edges (dashed with labels)
  - declared dynamic destinations from `WithDestinations` (dotted gray, no runtime effect)

## Run

```bash
# From repository root
cd examples/graph/visualization

# Generate DOT; will also try to render PNG if Graphviz is installed
go run .
```

Output files:

- `visualization-<ts>.dot`
- `visualization-<ts>.png` (if Graphviz `dot` is available)

If you don’t have Graphviz installed, install it:

**macOS:**
```bash
brew install graphviz
```

**Linux (Debian/Ubuntu):**
```bash
sudo apt-get install graphviz
```

**Windows:**  
Download the [Graphviz installer](https://www.graphviz.org/download/) and follow the installation instructions.

## Key APIs

- `g.DOT(...)` and `g.WriteDOT(...)` on a compiled `*graph.Graph`
- `g.RenderImage(ctx, format, outputPath, ...)`
- Common options: `WithRankDir(graph.RankDirLR|graph.RankDirTB)`, `WithIncludeDestinations(true|false)`, `WithGraphLabel("...")`
- Common formats: `graph.ImageFormatPNG`, `graph.ImageFormatSVG`

## Notes

- `WithDestinations` is for visualization and static checks only; it does not affect runtime routing.
- Conditional edges are emitted with dashed style and labels for each branch key.
- Virtual `Start` and `End` nodes can be included or hidden via options.
