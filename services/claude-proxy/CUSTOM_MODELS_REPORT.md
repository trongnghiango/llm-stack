# Custom Model Configuration Guide

## Overview
Provide template and instructions for adding three custom LLM entries to Claude Code settings (`~/.claude/settings.json`).

## JSON Template
```json
{
  "customModels": [
    {
      "name": "MyModel1",
      "target_api_model": "<Claude‑model‑or‑endpoint>",
      "alias": "CUSTOM1",
      "tier": "Tier-5-Custom"
    },
    {
      "name": "MyModel2",
      "target_api_model": "<Claude‑model‑or‑endpoint>",
      "alias": "CUSTOM2",
      "tier": "Tier-5-Custom"
    },
    {
      "name": "MyModel3",
      "target_api_model": "<Claude‑model‑or‑endpoint>",
      "alias": "CUSTOM3",
      "tier": "Tier-5-Custom"
    }
  ]
}
```

## Field Explanation
- **name**: conceptual identifier used in `model_mappings.json`.
- **target_api_model**: Claude model name (e.g., `claude-3-5-sonnet-latest`) or custom endpoint URL.
- **alias**: short flag for CLI (`--model CUSTOM1`).
- **tier**: optional classification for cost/priority.

## Adding to `~/.claude/settings.json`
1. Open `~/.claude/settings.json` in an editor.
2. Insert each custom model into the `model_mappings.json` file under `"mappings"` using the same keys as `name`.
3. Optionally add a top‑level array `"customModels"` (as shown above) to keep a record.
4. Validate JSON: `jq . ~/.claude/settings.json`.

## CLI Usage Example
```bash
claude --model CUSTOM1   # selects MyModel1
claude --phase PLANNING # uses mapping for GPT‑OSS‑120B (or your custom phase mapping)
```

## Verification
- Run `claude --list-models` – ensure `CUSTOM1`, `CUSTOM2`, `CUSTOM3` appear.
- Execute a simple command with one alias to confirm no errors.
- If validation fails, fix JSON syntax and retry.

---
**Next steps**
- Replace placeholder `<Claude‑model‑or‑endpoint>` with real model identifiers.
- Save file and reload Claude Code (restart session) to apply changes.
