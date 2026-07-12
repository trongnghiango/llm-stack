---
name: ka-execute
description: Orchestrator that routes execution intents from ka-plan to concrete implementation skills (ka-be, ka-fe, ka-db-schema, ka-test, ka-ops). Use when the user wants to run, deploy, apply, or otherwise execute a plan.
metadata:
  user-invocable: true
  version: 1.4.0
  auto-trigger-keywords:
    - execution
    - orchestrate
    - deploy
    - run
    - apply
    - start
    - launch
    - trigger
---
# Ka Execute – Orchestration / Routing Skill

## Purpose
Dispatches `ka-plan` outcomes. Takes a high‑level intent and forwards it to the appropriate concrete implementation skill. **Never** edits repository files itself; it only forwards arguments via the `Skill` tool and returns the target skill’s response.

## Interaction Flow (Hard Stops)

### Step 0 — Check for a Locked Handoff (run this before Step 1)
Look for the most recent `.agents/memory/decisions.json` entry where `"status": "locked"` and no `"consumed_at"` field is present yet.

- **If NOT found** → proceed directly to Step 1 as normal (nothing changes for standalone use).
- **If found** → present it instead of asking for action/options from scratch:
```
🗣️ Tìm thấy quyết định đã chốt từ ka-plan:
"<decision.summary>"
→ Action đề xuất: <execution_hint.action>
→ Options đề xuất: <execution_hint.options>
→ Risk tier: <execution_hint.risk_tier>
Dùng luôn thông tin này? (yes / no, tôi muốn nhập lại từ đầu)
```
🛑 **HARD STOP** – Wait for the user's reply.
- On **yes** → adopt `action`, `options`, and `risk_tier` from `execution_hint`, then **skip Steps 1–4** and go straight to Step 5 (Confirm Execution).
- On **no** → discard the handoff for this run and continue with Step 1 through 4 exactly as below.

Regardless of yes/no, once this run reaches Step 7 (Return Response) and the handoff was used, write `"consumed_at": "<ISO timestamp>"` back into that `decisions.json` entry so it is not re-offered on a future run.

1. **Ask for Action** – Prompt the user to choose one of the supported actions.
```
🗣️ Which action do you want to execute? Options: backend, frontend, schema, test, ops
```
🛑 **HARD STOP** – Wait for the user’s reply.
2. **Validate Action** – If the reply is not a supported action, respond:
```
❌ Unknown action: <action>. Supported: backend, frontend, schema, test, ops.
```
🛑 **HARD STOP** – Ask again until a valid action is received. If 3 consecutive invalid replies occur, stop looping and reply: `Execution cancelled — no valid action provided after 3 attempts.`
3. **Ask for Options** – Request a JSON payload describing the execution details.
```
🗣️ Please provide a JSON object with the options for `<action>`.
```
🛑 **HARD STOP** – Wait for the user’s JSON input.
4. **Parse Options** – Attempt to parse the supplied string as JSON. On failure reply:
```
⚠️ Could not parse options as JSON. Send a valid JSON object.
```
🛑 **HARD STOP** – Prompt again for correct JSON. If 3 consecutive parse failures occur, stop looping and reply: `Execution cancelled — could not obtain valid JSON after 3 attempts.`

4b. **Validate Options Against Schema** – Once parsed, validate the JSON against the schema for `<action>` in the **Option Schemas** table below. Check required fields are present and types match.
```
⚠️ Missing/invalid field(s) for `<action>`: <field1>, <field2>. Expected: <schema summary>.
```
🛑 **HARD STOP** – Prompt again for a corrected JSON object. This check also applies to options adopted from a Step 0 handoff — a malformed `execution_hint.options` is not exempt and must still pass validation before Step 5.

5. **Determine Risk Tier & Confirm Execution**
 – Look up the action's risk tier (from `execution_hint.risk_tier` if this came from Step 0, otherwise use the default table below). Confirmation strength scales with tier:

| Risk Tier | Actions (default) | Confirmation required |
|---|---|---|
| 1 (low) | frontend, test | Simple: `Execute <action> with these options? (yes/no)` |
| 2 (medium) | backend | Simple: `Execute <action> with these options? (yes/no)` |
| 3 (high) | schema | Must type the action name back exactly, e.g. `Type "schema" to confirm this database change.` — a plain "yes" is not accepted. |
| 4 (highest) | ops | Must type the action name back exactly **and** the summary must first restate: which environment, whether a rollback plan exists. Only proceed once both are explicitly acknowledged. |

**[🛑 HARD STOP]** at whichever confirmation format applies. On anything other than the required confirmation, abort and reply "Execution cancelled."

6. **Route to Target Skill** – Call the appropriate skill via `Skill`:
```typescript
if (action === 'backend') {
  await Skill({skill: 'ka-be', args: JSON.stringify(options)});
} else if (action === 'frontend') {
  await Skill({skill: 'ka-fe', args: JSON.stringify(options)});
} else if (action === 'schema') {
  await Skill({skill: 'ka-db-schema', args: JSON.stringify(options)});
} else if (action === 'test') {
  await Skill({skill: 'ka-test', args: JSON.stringify(options)});
} else if (action === 'ops') {
  await Skill({skill: 'ka-ops', args: JSON.stringify(options)});
}
```
If the target skill returns an error, prefix it with `⚠️` and forward it.
7. **Return Response** – Pass the target skill’s output verbatim back to the user. If Step 0's handoff was used, also write `consumed_at` as described above.

All steps contain explicit **[🛑 HARD STOP]** markers to guarantee user confirmation before any sub‑skill runs.

## Routing Table
| Action   | Target Skill | Default Risk Tier |
|----------|--------------|--------------------|
| backend  | ka-be | 2 |
| frontend | ka-fe | 1 |
| schema   | ka-db-schema | 3 |
| test     | ka-test | 1 |
| ops      | ka-ops | 4 |
| *custom* | *add‑your‑skill* | *assign explicitly — never default to tier 1* |

## Option Schemas

Each action's `options` JSON is validated at Step 4b against the schema below **before** routing. Reject and re-prompt on any missing required field or wrong type — never forward a payload the target skill wasn't designed to receive.

```json
{
  "backend": {
    "required": ["feature", "details"],
    "properties": {
      "feature": { "type": "string", "description": "short slug, e.g. 'auth'" },
      "details": { "type": "string", "description": "what to implement, e.g. 'Add JWT login'" },
      "affected_modules": { "type": "array", "items": { "type": "string" }, "required": false }
    }
  },
  "frontend": {
    "required": ["page", "features"],
    "properties": {
      "page": { "type": "string", "description": "target page/route, e.g. 'dashboard'" },
      "features": { "type": "array", "items": { "type": "string" }, "description": "e.g. ['charts','filters']" }
    }
  },
  "schema": {
    "required": ["table", "change_type"],
    "properties": {
      "table": { "type": "string" },
      "change_type": { "type": "string", "enum": ["add_column", "add_table", "alter_column", "drop_column", "drop_table", "add_index"] },
      "details": { "type": "string", "required": false },
      "migration_reversible": { "type": "boolean", "description": "required for change_type in [drop_column, drop_table]" }
    }
  },
  "test": {
    "required": ["target"],
    "properties": {
      "target": { "type": "string", "description": "module/feature under test, e.g. 'auth'" },
      "test_type": { "type": "string", "enum": ["unit", "integration", "e2e"], "required": false }
    }
  },
  "ops": {
    "required": ["env", "action"],
    "properties": {
      "env": { "type": "string", "enum": ["staging", "production"] },
      "action": { "type": "string", "enum": ["deploy", "rollback", "restart", "scale"] },
      "rollback_plan": { "type": "string", "description": "required if env == 'production'" }
    }
  }
}
```

> If a `custom` action is added to the Routing Table, add its schema here in the same shape before enabling it — an action without a schema entry must be rejected at Step 4b rather than passed through unchecked.

## Testing (Skill‑Creator pattern)
Create `evals/evals.json` with prompts such as:
```json
[
  {"id":1,"prompt":"/ka-execute backend '{\"feature\":\"auth\",\"details\":\"Add JWT login\"}'","expected_output_contains":"feat(auth): implement JWT login","description":"Execute a backend plan for JWT authentication"},
  {"id":2,"prompt":"/ka-execute frontend '{\"page\":\"dashboard\",\"features\":[\"charts\",\"filters\"]}'","expected_output_contains":"Created React component Dashboard","description":"Execute a frontend plan for a dashboard UI"},
  {"id":3,"prompt":"/ka-execute ops '{\"env\":\"production\",\"action\":\"deploy\"}'","expected_output_contains":"Type \"ops\" to confirm","description":"Ops actions must require typed confirmation, not plain yes/no"},
  {"id":4,"prompt":"(decisions.json present with status locked, unconsumed)","expected_output_contains":"Tìm thấy quyết định đã chốt từ ka-plan","description":"Handoff from ka-plan is surfaced before asking for action/options"},
  {"id":5,"prompt":"/ka-execute schema '{\"table\":\"users\"}'","expected_output_contains":"Missing/invalid field(s)","description":"Schema action missing required change_type must be rejected at Step 4b, not routed"},
  {"id":6,"prompt":"/ka-execute ops '{\"env\":\"production\",\"action\":\"deploy\"}'","expected_output_contains":"Missing/invalid field(s)","description":"Production ops deploy without rollback_plan must fail schema validation"}
]
```
Run these evals with `claude-with-access-to-the-skill` (or the internal harness). The skill‑creator workflow will verify that the output contains the expected substring.

## Description Optimization
After the initial version, run the description‑optimization loop (see `skill‑creator`). Generate ~20 trigger queries (mix of should‑trigger / should‑not‑trigger), run `run_loop.py`, adopt the `best_description`, and update the front‑matter.

## References
- `ka-plan` – design‑flow inspiration, hard‑stop pattern, and source of the `decisions.json` handoff consumed in Step 0.
- `skill-creator` – test case generation, evaluation loop, description‑optimization.
