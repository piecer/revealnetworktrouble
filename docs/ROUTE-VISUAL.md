# Route / RTT visual redesign

## Visual plan (independent review pending)

Baseline: detached 2aff022; main has caller-owned compose.yaml change, untouched.
Inspected actual 04e3521 frontend/app.js (map renderer lines 399–465): luminous lime/cyan/rose route curves, halo circles, centered labels, per-destination legend. Its fixed shared gradient invented colors; do not reproduce that flaw. Live localhost:3000 inspected read-only in Chromium: dark olive shell, lime controls, no populated report on initial load. No diagnostic submitted to live service.

1. Restore luminous curved route strokes and meaningful halo circles within current Canvas ownership/budgets.
2. Route means original report result_index; all attempts of that result share a hue. Original immutable result identity (not filtered/draw index) selects a fixed 20-color palette; stable across filtering/reordering within a report, not across new independently ordered reports. Different accepted targets (maximum 20) have distinct palette entries.
3. Shared directed edges derive membership only from consecutive node IDs on actual routes. Draw at most four parallel curved lanes, explicitly disclose additional memberships in details. No per-attempt color explosion. Unassigned links stay neutral; folded/bypass connectors remain neutral dashed presentation, not observed links.
4. Node fill encodes existing latency_ms_avg: observed mean RTT to that node, NOT link delay. Fixed thresholds <10 / <50 / <100 / >=100 ms; zero is measured, missing gray. Keep reported status independent; white rings/icons denote origin, reached destination, unknown/folded, and nonhealthy state. Dashed purple ring remains inferred ASN context with existing disclaimer.
5. Preserve cached/raw immutable data, annotations, node drag and attached curves, keyboard/hover, 2D/3D, and Geo map. No production assets added, no continuous animation or new API requests. Hard ceilings remain 500 nodes / 1000 total connections / 1200 document elements; max four colored strokes per connection.
6. RED/GREEN semantics + real Chromium 375/1440 2D/3D exact-byte producer fixture and separately labeled captured replay. Visually inspect pixels. Finish docs and full make test/build/vet/web-test-syntax; no Docker or main changes.

## Historical pre-density evidence (superseded by correction below)

- Chromium verifier: `PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright node frontend/route-visual.browser.cjs /tmp/route-visual-evidence /home/piecer/.hermes/cache/terminal-output/out-1788758982-6755-4d50.log`. Own ephemeral loopback server closes on completion; nine served assets compared byte-for-byte. Eight producer/captured × 375/1440 × 2D/3D cases, plus four existing interaction cases, passed before final canonical rerun. These are intercepted report replays, NOT new live diagnostic evidence.
- Producer fixture now requires six completed attempts across all three results. Initial fixture incorrectly exhausted a one-slot supervisor and mislabeled unknown-hop attempts healthy, silently losing routes. Three slots and truthful degraded status fix that; exact producer-byte freshness remains mandatory.
- Observed RED/GREEN: labels previously crossed node circles; reserve full ring bounds in label placement. Aliases get at most 25 candidates (ordinary labels nine). Overlapping node role glyphs are suppressed, not stacked; full roles remain in hover/focus/inspector. The unchanged browser glyph-overlap and saved-alias assertions pass.
- Current source inventory: **Web 326 tests**, plus application contract script. Maximum report DOM measured 729 initial / 762 expanded; report/label navigation peak 1169, guard 1200. Earlier 699/732 and ASN 320 are historical records, not current readings.
- Screenshots and machine evidence: `/tmp/route-visual-evidence/results.json`, `interaction/results.json`, `{producer-route-RTT,captured-existing-network-replay}-{375,1440}-{2d,3d}[-canvas].png`, and `legend-{375,1440}.png`. Actual screenshots inspected, including dense captured replay. Desktop clearly restores luminous route curves, RTT circles and independent ASN rings.
- **Visual acceptance limitation:** 129-node captured all-target replay is still severely crowded at 375px; circles and edges overlap and most ordinary labels are omitted. Even the small mobile producer graph omits labels to protect rings. Zoom/filter/hover/keyboard inspector remain available, but this is not a claim of solved dense-mobile layout or pixel parity with 04e3521. Shared hue identity is stable only within a report; twenty distinct RGB values do not guarantee perceptual distinction or color-blind accessibility. Edge crossings remain possible. No physical device, screen reader, new live diagnostic, deployment, Docker, commit or release validation is claimed.

## Density correction and closeout evidence

The pre-density section above is historical, including its 326-test inventory and
nine/25-candidate description. Current source inventory is **Web 328 tests**, plus
the separate application contract script; the closeout inventory run collected
328 and passed the previously unrun asymmetric-glyph-metrics case. It also exposed
one help-text length regression. The neutral callout explanation was moved to the
existing route legend, retaining the original 140-character help cap rather than
weakening its test or adding DOM elements.

The correction measures actual Canvas ink bounds (including asymmetric bearings),
prioritizes aliases and reached destinations, and considers at most **64 fixed
multidirectional candidates per visible node**. Labels protect other labels and
full node/ring bounds. A single bounded nearest-neighbor density pass adapts cores
and halos above 32 nodes, under the unchanged 500-node ceiling; small graphs keep
large luminous circles. Neither pass moves raw topology or runs continuous physics.
Thin gray association callouts are decoration, not observed routes. Actual route
colors, four shared lanes maximum with disclosed omitted memberships, independent
RTT fill (including measured zero versus missing), status/role rings, inferred ASN
context, Unknown folding, Geo, immutable facts, and existing interactions remain
separate contracts.

### Prior same-byte comparison (not final-source acceptance)

These measurements come from `/tmp/route-density-green2-20260907/` and the
independent baseline comparison in `/tmp/route-density-comparison-20260907/`.
They are retained as correction history, not evidence that later bytes passed.

| Captured view | Baseline labels | Regressed labels | Corrected labels | Baseline core overlaps | Corrected core overlaps |
| --- | ---: | ---: | ---: | ---: | ---: |
| 375 / 2D | 11 | 2 | 25 | 379 | 0 |
| 375 / 3D | 11 | 1 | 22 | 358 | 4 |
| 1440 / 2D | 86 | 13 | 119 | 0 | 0 |
| 1440 / 3D | 75 | 14 | 111 | 1 | 0 |

The strict browser gate pins extracted captured report SHA-256 to
`bf730a41e105628abb96e1aef06563e3566f69e202c481dd2adc863be93f95bd`,
requires at least baseline label coverage and no worse core overlaps in all four
views, and rejects ordinary glyph/glyph and glyph/circle overlaps in all eight
producer/captured cases. Small producer cases retain at least 10 mobile / 11 desktop
labels and zero core overlaps. These metrics do not measure every route/callout
crossing or establish universal readability.

The producer fixture uses synthetic observations through the real runner, analysis
and compact encoder. Its degraded outcomes are supported by measured RTT jumps,
not inferred from unknown hops alone. Its exact-byte gate requires six completed
routes across three results and rejects `malformed_details`; do not run update mode
to make a failing freshness check pass.

### Final-source reproduction

Run from `/home/piecer/dev/src/revealnetworktroble-route-visual` after the last
repository edit, preserving separate command exit statuses:

```sh
make test
make build
make vet
make web-test-syntax
git diff --check
env -u UPDATE_REPORT_FIXTURES go test ./backend/api -run '^TestRouteVisualFixtureMatchesExactProducerBytes$' -count=1 -v
PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright node frontend/route-visual.browser.cjs /tmp/route-visual-closeout-20260907 /home/piecer/.hermes/cache/terminal-output/out-1788758982-6755-4d50.log
```

The fresh browser evidence directory contains `results.json` (eight visual cases
and nine exact served-asset hashes), `density.jsonl`, `interaction/results.json`
(four existing interaction cases), `interaction.log`, all producer/captured
375/1440 × 2D/3D full-page and Canvas screenshots, and both legend screenshots.
The browser wrapper owns and closes its ephemeral loopback server. These are
intercepted producer/captured replays, not new live network diagnostics. Final
command results and an external source manifest are recorded with the closeout
handoff, avoiding a self-invalidating documentation edit after verification.

**Remaining visual limitation:** dense mobile identification is not fully solved.
Many central hops remain unlabeled; route and callout crossings can make label
association difficult, especially in 3D, where the prior corrected capture still
had four core overlaps. Filtering, zoom, hover/focus, keyboard navigation and the
inspector remain necessary. Zero ordinary glyph collisions does not imply zero
ring/edge occlusion or user acceptance. Physical-device, screen-reader, perceptual
palette accessibility, deployment and release acceptance remain unverified.

Independent review and user visual acceptance remain pending; do not equate passing automated gates with visual approval.
