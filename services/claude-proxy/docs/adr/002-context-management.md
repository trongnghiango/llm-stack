# ADR-002: Context Management and Retrieval Strategy

## Status
Accepted

## Context
AI agents often fail because their context windows get saturated with verbose log messages, redundant files, and large project trees.

## Decision
We prioritize lossless context extraction over lossy summarization. We use **Memory Graphs** to index file dependencies and use **RTK** to compress shell outputs, logs, and directory trees without losing critical debugging details.

## Consequences
- **Positive:** Higher accuracy in bug localization, fewer token-limit crashes.
- **Negative:** Requires initial indexing overhead before running deep agent workflows.
