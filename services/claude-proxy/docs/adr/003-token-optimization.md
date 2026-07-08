# ADR-003: Output Verbosity and Token Optimization

## Status
Accepted

## Context
Generative models often output conversational filler, repeating instructions or explaining code unnecessarily. This wastes tokens and increases latency.

## Decision
We enforce the **Caveman Lite** persona across all agent interactions. System instructions must mandate that models speak concisely, avoid pleasantries, and output code patches directly.

## Consequences
- **Positive:** Up to 30% reduction in output token usage and faster response cycles.
- **Negative:** Agent outputs are less conversational, which might require adjustment from human developers reading the raw logs.
