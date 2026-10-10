# Web IP-context client

## Interaction and privacy

The ready topology report exposes **IP 추가 정보 조회**, a native button beside a
native public-IP selector and the complete selected address in the graph toolbar.
The same action is available in 2D and 3D. Geo has one separate, nonsticky
supplemental region adjacent to its existing detail workspace. Its selected-node
callback updates context selection; unlocated public observations can also be
selected without inventing a map point. Existing Canvas click/alias/drag semantics
and immutable Geo detail text are not repurposed.

Only activating the query button requests `POST /api/v1/ip-context`, with exactly
`{"address":"<selected canonical public IP>"}`. Loading reports, selecting,
hovering, filtering, paging, opening Geo and switching 2D/3D perform no context
requests. No report or capabilities request is used for this feature. The warning
beside the action explains that the selected IP is sent through the API to the
system resolver, RIR RDAP and RIPE RIS/RPKI services.

IP remains the primary identity. PTR/forward status, registration range and
organization/country/events, BGP prefix/origins and individual RPKI outcomes have
separate source/time and missing/error labels. In-place caveats distinguish names
from identity, registration from physical operator/location, RIS observations and
its 8-hour dump freshness from traceroute, and RPKI origin authorization from a
security or service-health verdict. Fetched/expiry timestamps are application
snapshot times, not DNS TTL or provider database update times.

Provider text is assigned with `textContent`, never HTML or navigable links. The
existing case-insensitive credential-reflection policy is applied at display,
not by rewriting validated producer facts. No contact fields, raw errors or
provider URLs are accepted by the schema. Context is memory-only and never added
to report objects, raw JSON downloads, human exports or IP-label storage.

## Wire and lifecycle boundaries

`state.js` exports `parseIPContext(Uint8Array, requestedAddress)` and the separate
`createIPContextController`. Reusing shipped modules preserves the existing ten-
asset deployment closure; there is no new script element, import asset or backend
static allowlist change.

The parser enforces 16,384 wire bytes, strict UTF-8, one JSON document, decoded-key
uniqueness, scalar Unicode, closed nested shapes and exact request-address binding.
Decimal tokens are validated as mathematical integers before JavaScript numeric
conversion; fractions rounded to integers, underflow and overflow cannot pass.
Exact zero with arbitrarily large exponents remains valid within the wire budget.
IP/range/prefix, status/field matrices, sorted unique names/origins, source,
Gregorian UTC millisecond timestamps and stable/transient TTL arithmetic are
validated together. Canonical Go JSON escaping plus its final newline is also
charged. The optional direct-object `normalizeIPContext` boundary checks original
own enumerable data descriptors, prototypes, symbols and dense arrays before any
snapshot/serialization; getters and hooks are not invoked.

Each app has one controller and at most one underlying context fetch/body reader.
It uses a total 10-second deadline, bounded streaming reads, no redirects, no
retries, no cookie credentials and no referrer. An aborted but noncooperative
operation keeps its lease until it actually exits; clicking while that lease is
held cannot start a replacement flight. UI cancellation and timeout do not wait
for cooperation. Late bodies are cancelled and stale success/error/finally cannot
publish, focus, or overwrite a successor.

Authority includes report identity, selected IP, API base and auth revision/token
scope. Selection, cancellation, report replacement, base/auth change, navigation,
clear, unload and destroy revoke delivery. A 64-entry memory LRU is cleared on
report/base/auth replacement and respects server expiry. Reuse is labelled **앱
메모리 캐시** separately from unchanged API `source` and timestamps. Unsupported
404, provider/HTTP failures and invalid context responses stay local; the report,
graph, map, target catalog and sharing remain usable.

## Eligibility and document admission

Candidates come from the validated report, not Geo success or inferred presentation
nodes. Full reports use completed, status-consistent attempts and responsive
positive-hop public-IP observations. Compact reports use positive-hop observed IP
nodes; healthy/degraded status or an actual responsive average retains evidence
without requiring coordinates. Synthetic failures without responsive evidence,
private addresses, hostnames, unknown and local nodes are excluded. Candidates
are canonical, unique, deterministic and capped at 500, with exact omitted counts.

One active panel reuses at most 64 option slots across pages. Controls, hidden
result rows, scripts and all other static/dynamic elements count toward the
unchanged **1,200 whole-document element ceiling**. The entire new fragment is
constructed detached, measured (at most 100 elements), then admitted atomically.
Insufficient capacity adds zero elements and uses an existing bounded status with
navigation/recovery instructions. Only registered predecessor content is removed;
foreign siblings and successor focus/events are preserved. Results update text in
already-admitted rows rather than appending per-IP panels.

Supplemental facts have a native keyboard/touch scroll region. The topology
fullscreen target is the report section so the toolbar action remains accessible;
Geo retains its existing fullscreen view. The new region is not sticky or overlaid
on map markers. Inherited short-height Geo detail-dock occlusion and the previously
recorded Chrome 156 detail-scroll failure are **not fixed or waived** by this work.
Chromium 153 coverage is a separate runtime result, not proof of a 156 fix, physical
device support or screen-reader acceptance.

## Regression gates and integration locators

Canonical `make test` now collects the exact unchanged 192-case producer corpus
(60 accepts / 132 rejects), descriptor/numeric guards, controller races/limits,
actual-app ownership/privacy, 500-address paging and exact-fit DOM admission.
`make web-test-syntax` includes all new tests and the new browser gate;
`make web-test-browser` also invokes `ip-context.browser.cjs`.

The browser script supports two explicit modes:

- `node frontend/ip-context.browser.cjs OUTPUT`: owned HTTP fixture replay,
  labelled controlled data, **not** live producer delivery.
- `node frontend/ip-context.browser.cjs '{"api":"...","ui":"...","frontend":"...","output":"..."}'`:
  parent-owned actual Go API/CORS/verified local TLS-provider integration. This
  mode performs no API response interception. DNS, traceroute and provider data
  are controlled by the parent's Go fixture.

It checks 375×812, 667×375, 760×600 and 1440×900, explicit keyboard/tap invocation,
no implicit requests, sources/caveats, fullscreen, target All/None recovery,
mutation peaks and accessibility-tree action presence. Screenshots are retained.
Set `PLAYWRIGHT_PATH` to the installed module and `IP_CONTEXT_CHROMIUM` to an
explicit executable; the gate records the actual executable hash and version.

Stable UI seams:

- `[data-ip-context-panel]`, with `data-state` (`idle`, `loading`, `ready`,
  `cancelled`, `timeout`, `unsupported`, `invalid-response`, etc.).
- `[data-ip-context-select]`, `[data-ip-context-address]`.
- `[data-ip-context-query]`, `[data-ip-context-cancel]`.
- `[data-ip-context-previous]`, `[data-ip-context-next]`, `[data-ip-context-page]`.
- `[data-ip-context-facts]`, `[data-ip-context-status]`.

No deployment, Docker/Buildx, existing port 3000, physical-device or production
provider-availability acceptance is implied by these local gates.
