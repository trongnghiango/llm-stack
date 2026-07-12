# STAX Technical Design & Architecture Extension

> **Auto-loaded** by `ka-plan` when a STAX project is detected.
> This extends general design rules with STAX-specific technology stack and boundaries.

## 1. STAX Technology Stack

| Tier / Part | Technology & Patterns |
|---|---|
| **Frontend** | React + TanStack Router + React Query (domain data) + Zustand (global state only) |
| **BFF** | Express (Proxy only to `/api/*`, serves static assets, NO business logic) |
| **Backend** | NestJS + DDD + Clean Architecture (Presentation, Application, Domain, Infrastructure) |
| **Database** | PostgreSQL + Drizzle ORM |
| **Contracts** | Zod schemas defined in `shared/contracts/` (single source of truth for FE/BE) |
| **Events** | Domain Events published via Async EventBus (out-of-transaction) |
| **Auth** | JWT + Redis Session + ALS (Async Local Storage) |
| **Testing** | PGLite for Repository integration testing |

---

## 2. STAX Backend Tiers

- **Tier 1 (Foundation)**: Shared utility modules (`Rbac`, `AuditLog`, `Notification`, `Storage`). No domain logic.
- **Tier 2 (Domain Core)**: Backbone entities and aggregates (`User`, `Employee`, `OrgStructure`).
- **Tier 3 (Process Flow)**: Core business workflows depending on Tier 2 (`CRM`, `Accounting`, `Contracts`).
- **Rule**: Tier 2 **MUST NOT** import or depend on Tier 3. Cross-tier communication must use Port Interfaces or EventBus.

---

## 3. STAX Rigid Boundaries (Critical Smells to Flag)

Flag these boundary violations immediately during design:

### Frontend & BFF
- **State Boundary**: Local/UI state -> React state. Server/Domain data -> React Query. Global/Cross-page state -> Zustand. Never fetch raw API data directly in components without React Query.
- **BFF Boundary**: Putting business logic, validators, or database calls inside Express BFF `server/` is forbidden. BFF is a pass-through proxy.
- **Contract Boundary**: DTOs or Zod schemas defined locally in modules instead of `shared/contracts/` is a red flag.
- **Server-Driven UI**: UI buttons/actions should read permissions from API response `_actions` object (`_actions.canEdit`, etc.), never hardcoded role checks in frontend templates.
- **Routing**: Use TanStack Router `<Link>` or API hooks; never use standard HTML `<a>` tags or `window.location.href`.

### Backend & Database
- **Domain Purity**: Domain Entities importing `@nestjs/common`, `@nestjs/injectable`, or `drizzle-orm` is forbidden. Keep them pure TS.
- **Dependency DI**: Injecting concrete repository classes instead of Symbol-based Port interfaces into Application Services is forbidden.
- **Tenant Isolation**: Read `organizationId` from authenticated JWT/Session context, never trust an `orgId` passed in query strings or request bodies.
- **Transaction Safety**: Publishing Domain Events or running HTTP calls inside a active database transaction (`runInTransaction`) is forbidden.
- **Audit Logging**: Never `await` audit logging inside business transactions. Audit logging is fire-and-forget (`.catch(() => {})`).
- **Cross-Module Coupling**: Direct import of another module's repository is forbidden. Inject Port interface or communicate asynchronously.
