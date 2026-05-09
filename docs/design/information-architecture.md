# cotel — Information Architecture

> Audience: CTO + frontend implementer. Single-user, self-hosted, Pi-friendly.
> Scope: MVP v1 — no multi-tenant, no auth, no real-time push.

---

## Navigation model

**Top horizontal nav — 4 items.** No sidebar. No nesting in the primary nav.
Every screen carries a global **time range picker** (7d / 30d / 90d / custom) that
filters all data on the page.

```
cotel        Overview  |  Sessions  |  Costs  |  Tools          [30d ▾]
```

Session detail is a drill-down from Sessions (back-link, no separate nav entry).

---

## Screen hierarchy

```
cotel
├── Overview                 /
│   ├── Key metrics row (sessions, cost, input tokens, output tokens)
│   ├── Cost sparkline (daily, last N days matching time range)
│   └── Recent sessions table (last 5–10 rows) → link to Sessions
│
├── Sessions                 /sessions
│   ├── Filter bar (model, date range, search)
│   ├── Sessions table (paginated, 20/page)
│   └── → Session Detail (row click)
│
│   Session Detail           /sessions/:session_id
│   ├── Session header (id, time, duration, model, total cost)
│   ├── Span timeline (ordered by start_time, grouped by type)
│   ├── Cost + token summary cards
│   └── Raw attributes viewer (collapsible JSON)
│
├── Costs                    /costs
│   ├── Total cost headline
│   ├── Daily cost bar chart (from daily_usage)
│   ├── By-model breakdown table
│   └── By-session top-10 table
│
└── Tools                    /tools
    ├── Total calls headline
    ├── Tool stats table (calls, avg duration, fail rate, usage bar)
    └── Usage trend mini-chart (top 2 tools by default, selectable)
```

---

## Data residency (what lives where)

| UI concept       | Source table    | Key columns needed              |
|------------------|-----------------|---------------------------------|
| Session list row | `spans` (agg)   | session_id, min(start), max(end), model, SUM(cost_usd), COUNT(*) |
| Session timeline | `spans` (raw)   | span_id, parent_span_id, name, start_time, end_time, tool_name, input_tokens, output_tokens, cost_usd, attributes |
| Daily cost chart | `daily_usage`   | day, SUM(total_cost_usd)        |
| By-model table   | `spans` (agg)   | model, COUNT(*), SUM(tokens), SUM(cost_usd) |
| By-session table | `spans` (agg)   | session_id, SUM(cost_usd)       |
| Tool stats table | `spans` (agg)   | tool_name, COUNT(*), AVG(duration_ms), fail_count/total |

---

## Global filter: time range

One time range picker applies to all pages.  
Default: **last 30 days** (matches existing backend window).  
Options: 7d, 30d, 90d.  
Implementation: pass `?since=<ISO-date>` query param; backend filters `WHERE start_time >= ?`.

---

## API gaps (flagged for CTO)

The current backend exposes one query page (GET /) with three hardcoded queries.
MVP frontend needs these additional endpoints:

| Endpoint                     | Purpose                          | New?  |
|------------------------------|----------------------------------|-------|
| `GET /sessions`              | Session list with per-session agg| ✅ new |
| `GET /sessions/:id`          | All spans for one session        | ✅ new |
| `GET /costs/timeseries`      | Daily totals from daily_usage    | ✅ new |
| `GET /costs/by-session`      | Top sessions by cost             | ✅ new |
| `GET /tools/stats`           | Tool calls + duration + fail rate| ✅ new |
| `GET /overview`              | Summary metrics (already exists, refine) | 🔧 refine |

### Schema gaps to discuss with CTO

1. **No `project` field** — Claude Code may emit `project.path` in `resource_attrs`
   JSON; need to extract and index if we want project-level filtering.
2. **No error/status field** — fail rate for tools requires OTLP span status
   (`STATUS_CODE_ERROR`) or an error attribute. Need to extract into `spans` table.
3. **Session status** — "active" vs "completed" can be inferred from whether the
   session span's `end_time` is set, but this needs an explicit query pattern.
4. **`daily_usage` lacks per-session granularity for cost history** — the current
   schema has `session_id` in the primary key so per-session daily cost is queryable;
   just needs a new endpoint.

---

## Interaction patterns

| Pattern             | Used on                        | Notes |
|---------------------|--------------------------------|-------|
| Drill-down (row click) | Sessions table → Session detail | Standard; URL changes, back button works |
| Global filter       | Time range picker in nav       | Query param; page reloads (no JS state) |
| Column filter       | Sessions filter bar            | Model dropdown, date range; form GET |
| Pagination          | Sessions list                  | 20/page, prev/next links |
| Collapsible section | Raw attributes in session detail | HTML `<details>` element, no JS needed |

---

## Pi-friendly constraints

- **No heavy SPA.** Go templates + vanilla HTML. Charts: inline SVG or a lightweight
  charting library (Chart.js ~60 KB min+gz, or pure CSS bars for the MVP).
- **No WebSocket / SSE for MVP.** Data refreshes on page load only.
- **No client-side routing.** Full-page navigation; back button must work.
- **Page weight target:** < 100 KB HTML+CSS per page (excl. chart lib).
- **Query latency target:** p95 < 500ms per page load on Pi hardware.
