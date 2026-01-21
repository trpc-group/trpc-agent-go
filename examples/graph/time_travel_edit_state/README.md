## Time Travel: Edit State (Graph)

This example demonstrates checkpoint-based "time travel" state editing:

- Run a graph that interrupts (`graph.Interrupt`)
- Read the checkpoint state via `graph.TimeTravel.GetState`
- Write a new `"update"` checkpoint via `graph.TimeTravel.EditState`
- Resume execution from the updated checkpoint

It uses the in-memory checkpoint saver and does not require any model keys.

Run:

go run ./examples/graph/time_travel_edit_state

What to look for:

- The first run interrupts and creates an interrupt checkpoint.
- The example edits `counter` at that checkpoint and writes a new checkpoint.
- The second run resumes from the updated checkpoint and completes.
