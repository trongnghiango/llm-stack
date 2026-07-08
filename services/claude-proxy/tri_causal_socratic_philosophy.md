---
name: tri-causal-socratic-philosophy
description: Detailed description and diagram of the Nhân‑Duyên‑Quả + Duyên khởi + Socratic reasoning framework.
metadata:
  type: reference
---

# Tri‑Causal Socratic Framework

## Overview

The framework fuses three classical concepts into a single practical method for problem‑solving:

* **Nhân – Duyên – Quả** – a causal triad that records *causes* (nhân), *conditions* (duyên) and *effects* (quả).
* **Duyên khởi** – the Buddhist principle of dependent origination (paticca‑sammuti).  It recognises that every effect can become a new cause, forming a potentially infinite loop.
* **Socratic verification** – a series of probing questions that tests the necessity, sufficiency and truth of each link.

Together they form a **Rule/Skill set** that can be applied to any domain: software, product design, personal decisions, or organisational strategy.

---

## 1. RULES

### 1.1 Nhân (Cause) Rules
| # | Rule | Socratic Prompt | Reason |
|---|------|------------------|--------|
| 1 | Identify the direct cause | “What directly triggers this phenomenon?” | Isolate the immediate driver. |
| 2 | Test necessity | “If this cause is removed, does the phenomenon disappear?” | Verify that the cause is required. |
| 3 | Quantify strength | “What proportion of the outcome is attributable to this cause?” | Prioritise high‑impact causes. |

### 1.2 Duyên (Condition) Rules
| # | Rule | Socratic Prompt | Reason |
|---|------|------------------|--------|
| 4 | List enabling conditions | “Which conditions must exist for the cause to act?” | Capture the environment. |
| 5 | Check independence | “If a condition changes, does the cause still work?” | Spot fragile dependencies. |
| 6 | Identify secondary conditions | “Are there auxiliary factors that amplify or suppress the condition?” | Avoid hidden variables. |

### 1.3 Quả (Effect) Rules
| # | Rule | Socratic Prompt | Reason |
|---|------|------------------|--------|
| 7 | Immediate effect | “What is the short‑term result of the cause+condition pair?” | Establish the direct output. |
| 8 | Long‑term cascade | “What downstream effects follow from this result?” | Map the ripple. |
| 9 | Feedback loop check | “Can this effect become a new cause?” | Detect Duyên khởi cycles. |

### 1.4 Duyên khởi (Dependent Origination) Rules
1. **Cycle detection** – treat every effect that can become a new cause as a potential loop.
2. **Break point** – eliminating *any* node in the loop (cause, condition, or effect) cuts the cycle.
3. **Value of interruption** – each broken loop reduces “dí‑đính” (attachment) and prevents exponential growth of the problem.

### 1.5 Socratic‑Verification Rules
| # | Rule | Socratic Prompt | Reason |
|---|------|------------------|--------|
|10| Reverse the cause | “If the cause never existed, does the problem still appear?” | Test *công‑nghiệp* (necessity). |
|11| Challenge conditions | “What happens if we vary a condition?” | Expose edge cases. |
|12| Demand evidence | “What data proves this causal link?” | Guard against speculation. |

---

## 2. SKILL – **Socratic‑Causal Query**

A reusable prompt template that guides the analyst through the entire chain:

```
Input: brief problem description

1️⃣ Nhân?   – “What directly creates the issue?”
2️⃣ Duyên?  – “Which conditions enable that cause?”
3️⃣ Quả?    – “What immediate outcome do we observe?”
4️⃣ Duyên‑khởi? – “Can this outcome become a new cause? (Is there a loop?)”
5️⃣ Socratic check – “If the cause is absent, does the outcome vanish? If a condition changes, does the cause still fire?”
6️⃣ Action – “Choose ONE element (cause, condition, or effect) to modify and break the loop.”
```

The skill returns a structured JSON:
```json
{
  "cause": "...",
  "conditions": ["..."],
  "effects": ["..."],
  "cycle": true|false,
  "suggested_break": "cause|condition|effect",
  "questions": ["..."]
}
```

---

## 3. Diagram – Overall Flow

```
            Nhân ──► Duyên ──► Quả
              ▲          │      │
              │          ▼      ▼
          (Break) ◄───── Duyên‑khởi ◄─────
               (Socratic verification)
```
*The arrow **Nhân → Duyên → Quả** is the causal chain.
*The **Duyên‑khởi** arrow closes the loop, turning the effect into a new cause.
*The **break** box represents the intervention point chosen after Socratic questioning.

---

## 4. Concrete Examples

### Example 1 – API latency in a Go proxy
| Step | Observation | Application of Rules |
|------|-------------|---------------------|
| Nhân | `http.Client` timeout set to 2 s. | Rule 1 – identify direct cause. |
| Duyên | Network jitter, upstream server slow‑response. | Rule 4 – list conditions that let the timeout fire. |
| Quả | Client receives `504 Gateway Timeout`; users see delays. | Rule 7 – immediate effect. |
| Duyên khởi | Repeated timeouts cause upstream to back‑off, which in turn increases latency further. | Rule 9 – effect becomes new cause (feedback). |
| Socratic check | “If we increase timeout to 5 s, does the error disappear?” – Yes, but latency grows. | Rule 10‑12 – verify necessity and evidence. |
| Action | Implement exponential‑back‑off *and* raise timeout *or* add retry logic. | Break the loop at the **condition** (network jitter) and at the **effect** (timeout error). |

Result: latency reduced, loop broken, no cascading timeout cascade.

### Example 2 – Log‑payload overload
| Step | Observation | Application |
|------|-------------|-------------|
| Nhân | `LOG_PAYLOADS=1` writes a file per request. |
| Duyên | No rotation policy; disk space finite. |
| Quả | `logs/payloads/` fills disk → proxy crashes. |
| Duyên khởi | Crash stops logging, which hides the original cause, leading to silent failures. |
| Socratic check | “If we disable payload logging, does the crash stop?” – Yes. |
| Action | Add a **cleanup daemon** (break at condition) *and* switch to **JSONL** (reduce file count). |

Result: disk stays healthy, monitoring easier, loop eliminated.

---

## 5. How to Apply in Practice
1. **Define the problem** – one‑sentence statement.
2. **Run the Socratic‑Causal Query skill** – either manually using the template, or programmatically via the `Socratic‑Causal Query` skill.
3. **Collect the JSON output** – it lists cause, conditions, effects, and whether a cycle exists.
4. **Choose a break point** – based on impact, effort, and risk.
5. **Implement the fix** – modify code, configuration, process, or policy.
6. **Validate** – re‑run the query; `cycle` should now be `false` and the effect should disappear.
7. **Document** – store the analysis as a Markdown node under `knowledge-base/vault/02_atomic_nodes` with frontmatter `nhan`, `duyen`, `qua` for future reuse.

---

## 6. Conclusion

The **Tri‑Causal Socratic Framework** gives a repeatable, verifiable path from symptom to root cause, through the lens of Buddhist dependent origination and classical Socratic dialogue.  By explicitly recording *nhân*, *duyên* and *quả*, checking for loops, and interrogating each link, teams can break vicious cycles, reduce technical debt, and arrive at solutions that are both logical and mindful.

---
