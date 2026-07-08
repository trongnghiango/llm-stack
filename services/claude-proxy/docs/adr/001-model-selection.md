# ADR-001: Model Selection for Specialized Agent Roles

## Status
Accepted

## Context
When running long-running software engineering agents, using a single LLM leads to high token costs, performance bottlenecks, and inconsistent code style.

## Decision
We split core tasks across four specialized models mapped to active endpoints in Claude Code:
- **Planner & Reviewer:** GPT-OSS-120B (Mapped to: OPUS)
- **Primary codebase editor:** GLM-5.2 (Mapped to: SONNET)
- **Algorithm & Test writer:** DeepSeek (Mapped to: HAIKU)
- **Docs & Knowledge base manager:** MiniMax (Mapped to: CUSTOM)

## Consequences
- **Positive:** Reduced runtime costs, faster execution loops, and better adherence to architectural boundaries.
- **Negative:** Increased complexity in maintaining multiple API keys and orchestration logic.
