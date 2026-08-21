# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## High-Level Architecture

`llm-stack` is a unified Docker Compose stack that acts as a proxy chain and router for Large Language Models (LLMs) used by Codex. The primary flow routes requests from local instance of Codex to Cloudflare Workers AI:

1.  **Codex-proxy (`services/Codex-proxy`)**: Listens on `:20129`. It's a Model Rewriter built natively for Codex. It translates custom pseudo-model names (e.g., `swe.engineer`, `swe.architect`) to internal abstract aliases (e.g., `ka.base`, `ka.reason`). Written in Go.
2.  **9router (`services/9router`)**: Listens on `:20128`. Acts as a routing layer and UI admin (accessible at `http://localhost:20128`). Routes the `ka.*` requests to the appropriate upstream model via `cf-ai-proxy`. Based on the `decolua/9router` Docker image.
3.  **cf-ai-proxy (`services/cf-ai-proxy`)**: Listens on `:20127` (internal). Converts Anthropic API formats to Cloudflare AI REST payload structures natively. Manages Load Balancing among multiple Cloudflare accounts. Written in Go.
4.  **Redis (`data/redis`)**: Tracks sessions and quotas internally.

Data mapping goes: `Codex env var/pseudo model` -> `Codex-proxy alias (ka.*)` -> `cf-ai-proxy model (e.g., qwen-2.5-coder)` -> `Cloudflare models (@cf/...)`

## Important Operations and Commands

The repository uses a shell script utility `./stack` for managing operations.

*   **Start the full stack:**
    ```bash
    ./stack start
    ```

*   **Check container status:**
    ```bash
    ./stack status
    ```

*   **Restart a specific service** (e.g., to pick up changes):
    ```bash
    ./stack restart <service_name> # e.g., ./stack restart cf-ai-proxy
    ```

*   **Stop the stack:**
    ```bash
    ./stack stop
    ```

*   **View Logs:**
    ```bash
    ./stack logs               # All logs
    ./stack logs <service_name> # Logs for a specific service
    ```

*   **Sync NVIDIA NIM accounts & reset cache:**
    ```bash
    ./stack sync-nim
    ```

*   **Update a service image/code implementation:**
    ```bash
    ./stack update <service_name> # Automatically backups if updating 9router
    ```

## Updating the Codebase

- The internal Go applications (`cf-ai-proxy`, `Codex-proxy`) map configuration using `.csv` for quick routing checks and `init.sql` for seeding initial databases internally, mostly within `data/9router/db/init.sql`.
- Restart the modified service via `./stack restart <service_name>` to apply codebase changes directly to the running Docker instance.
