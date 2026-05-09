# ADR-0002: Dashboard — React SPA + JSON API (replacing Go SSR)

Date: 2026-05-09  
Status: accepted  
Supersedes: server-rendered dashboard from the MVP scaffold

---

## Context

The cotel MVP (commit `9b90641`) shipped a dashboard baked directly into the Go binary
as server-rendered HTML. The `internal/dashboard/` package contained:

- Six Go HTML templates (`index.html`, `sessions.html`, `session.html`, `costs.html`,
  `tools.html`, `base.html`)
- Route handlers that ran SQL queries inline and rendered the templates via `html/template`
- A custom FuncMap for formatting numbers and timestamps in templates

This approach was fast to prototype but created a growing set of problems:

1. **Coupling:** SQL queries, business logic, and presentation were all in one layer.
   Adding a chart required modifying Go code, rebuilding the binary, and redeploying.
2. **Interactivity ceiling:** Go templates can't deliver the SPA-level interactions the
   design called for (sortable tables, date range pickers, live refresh, heatmaps).
   Each interaction would need a full page reload or hand-written JavaScript.
3. **Design system gap:** The design team produced a token-based visual language and
   component specifications (FLO-8, FLO-32). Implementing these faithfully in Go
   templates would require embedding a CSS build pipeline anyway.
4. **Testability:** Template integration tests are brittle and tied to rendered HTML
   strings. A typed JSON API is far easier to unit-test.

The planned dashboard (six full pages with charts, pagination, sortable tables, and
a session drill-down) made the SSR approach untenable before the MVP even shipped to
real users.

---

## Options considered

### Option A — Keep Go SSR, add htmx/Alpine.js

Augment the existing templates with a lightweight JS library to get partial-page
updates and basic reactivity without a build step.

**Rejected.** The chart requirements (Recharts or equivalent) already mandate a proper
JS bundler. Mixing a heavyweight chart library with htmx would produce the worst of both
worlds: a complex build anyway, plus a non-standard component model.

### Option B — React SPA built by Vite, served as static assets from the Go binary

Build the frontend independently with Vite, embed the compiled assets into the Go
binary via `go:embed`, and add a JSON REST API (`/api/v1/...`) for the SPA to consume.
The dashboard handler serves `index.html` for all non-asset routes (React Router
catch-all).

**Accepted.** Separates concerns cleanly, enables the full design system, and keeps
deployment simple (still one binary, one container).

### Option C — Next.js / Nuxt as a separate service

Run a Node.js frontend server alongside the Go backend.

**Rejected.** Violates the one-container constraint from ADR-0001. Two processes means
two health checks, two restart policies, and a port-forwarding layer. Not worth it for
a personal analytics tool.

### Option D — WASM-compiled Go UI (e.g. Vecty or Gio)

Compile the dashboard to WebAssembly from Go.

**Rejected.** WASM bundle sizes are large, ecosystem is immature, and interoperability
with charting libraries is poor. Hiring/onboarding friction is high.

---

## Decision

**React SPA (Vite + React 18 + TypeScript) served as `go:embed` static assets**,
backed by a typed JSON REST API at `/api/v1/`.

Key design choices within the decision:

| Concern | Choice | Reason |
|---|---|---|
| Build tool | Vite | Fast HMR, first-class TypeScript, tree-shaking |
| Data fetching | SWR | Stale-while-revalidate, auto-refresh, simple cache keys |
| Charts | Recharts | Declarative React components, SVG-based, composable |
| Routing | React Router v6 | Nested routes, typed params |
| Styling | CSS Modules | Scoped styles, no runtime, aligns with design token system |

### Delivery

Implemented across two commits:

- `fa8d7df` (FLO-33): JSON API + React SPA scaffold added alongside existing SSR.
  Both lived simultaneously to allow rollout verification.
- `56fed21` (FLO-39): Legacy SSR handlers, `html/template` FuncMap, and all six
  Go template files removed. The SPA catch-all (`index.html` for every non-asset
  request) became the single dashboard entry point.

---

## Consequences

**Positive:**
- Clean API boundary: the JSON API is independently testable and can serve future
  consumers (CLI tools, CI dashboards, mobile).
- Design system is fully realisable: CSS Modules + design tokens drive every component.
- Frontend changes do not touch Go code; Go changes do not touch frontend code.
- SWR auto-refresh (30 s) gives the overview page a live feel without websockets.

**Negative / trade-offs:**
- Docker build adds a Node.js stage (~1 min on the builder). The final image stays
  pure Go + glibc (no Node runtime in the runtime stage).
- The `internal/dashboard/static/` directory must be pre-populated before `go build`
  runs, enforced by the Dockerfile and CI workflow (`npm ci && npm run build` before
  `go test` and `go build`).
- Bundle size is ~600 kB minified / ~180 kB gzip (dominated by Recharts + React).
  Acceptable for a self-hosted analytics tool accessed over LAN.
- Contributors need Node ≥ 22 for local frontend development in addition to Go.
