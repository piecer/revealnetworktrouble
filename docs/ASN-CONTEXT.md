# Private-span ASN context (implementation design)

## UX decision

Keep every private hop independently draggable/editable. Use a dashed per-node
context halo and a short contextual suffix in the existing collision-bounded
Canvas label, rather than an enclosing band: merged/revisited nodes can belong to
several routes, so a band would visually assert a single ownership group. The
semantic inspector and existing hover/keyboard-focus tooltip expose route/run
occurrences and an explicit inference disclaimer. Multiple contexts say
`경로별 ASN 문맥`; never assign `asn`, `public_ip`, or Geo metadata to private hops.
No additional requests, DOM items, modules, or production assets are required.

## Evidence contract

Infer only from a complete healthy/degraded route occurrence, bounded on both
sides by literal public-IP observations with equal valid numeric ASN (not an
organization name). Require each observed directed adjacency, and retain all
those adjacencies after presentation budgeting. Only RFC1918 IPv4 (including
mapped IPv4 after canonicalization) and IPv6 ULA qualify. Unknown, failed,
loopback, link-local, reserved, and missing evidence break the run. Incomplete
routes are conservatively excluded even if an interior run seems observable.
Classification follows `IsPublicDiagnosticIP` / Go `net.IP.IsPrivate` semantics.
Derived annotations belong only to the presentation; raw and normalized facts,
API schemas, Geo markers, aliases, and unknown folding/hiding stay unchanged.

## Bounds and verification

At most four context memberships per node and 500 across the presentation;
omitted memberships are counted and disclosed. Each context stores only numeric
route occurrence/result/attempt/run positions and ASN. No path copies or inferred
organization/geolocation. Canvas uses its existing label count/placement budget;
halos cost zero DOM. Inspector reuses one element per node and a fixed legend
reuses existing help text. Counts use total = displayed + omitted.

## Source acceptance and reproduction

Current Node runner inventory is **328 tests** (pre-density route-visual 326 and ASN-context 320 are historical), plus the separate application
contract script (Geo 308/304 and pre-Geo 298 are historical). At ASN-context acceptance, the focused
presentation/renderer suite contained 42 tests (historical). The Go-owned classification fixture
contains 3,119 witnesses: every canonical blocked prefix's first/last address,
adjacent outside boundaries, and each host bit and its inverse across the entire
prefix interior, plus public/mapped controls. These are not exhaustive enumeration
of every IPv6 address. Both endpoint-public and run-private classification are
compared against actual Go `IsPublicDiagnosticIP` and `net.IP.IsPrivate` output.

`testdata/compact-asn-context-report.json` is a **synthetic observed-route** fixture
produced by the real Go runner, analysis and compact serializer. Freshness checks
compare exact bytes. The Web regression consumes those bytes through strict
`normalizeReport` → model → presentation → both projections; forged wire context
fields reject. No raw/normalized facts change and Geo remains empty.

Context counts cover eligible memberships on complete retained presentation
routes, not a claim about omitted/unobserved network paths. Separate node/link/
route budgets retain their existing omission reporting. Context limits cover
499/500/501/600 total memberships and four memberships per node, including nodes
whose contexts are all omitted. The inspector reports those omitted memberships;
shared conflicting contexts never advertise one universal ASN. Small Canvas labels
use a concise `AS15169 문맥·추정` suffix within 28 characters; full address/alias,
route occurrence and non-ownership disclaimer remain in hover/focus/inspector.

Reproduction from this isolated worktree:

```sh
go test ./backend/api -run TestCommittedCompactCanonicalFixturesMatchGoProducerBytes -count=1
go test ./backend/diagnostic -run TestASNClassificationFixtureFreshness -count=1 -v
node --test frontend/unknown-presentation.test.js frontend/topology-renderer.test.js
node /tmp/canvasprobe/asn-context-acceptance.cjs
make test
make build
make vet
make web-test-syntax
git diff --check
```

The preserved local Playwright verifier uses
`/tmp/canvasprobe/node_modules/playwright` and owns an ephemeral 127.0.0.1 source
server; it never contacts localhost:3000. It compares all nine served production
assets byte-for-byte, replays the exact producer fixture, and records hashes in
`/tmp/canvasprobe/asn-context-final/results.json`. Four Chromium cases cover
375/1440 × 2D/3D: two dashed lavender halos and inferred labels, hover and keyboard
focus disclaimer, native inspector, private-node dragging with attached incoming/
outgoing edges, unchanged other node, alias editing and reload persistence, zero
additional interaction requests, zero page errors/overflow, and 284 document
elements (ceiling 1,200). Eight hover/inspector PNGs are retained beside the JSON.
The source server closes after each run; no Docker lifecycle is involved.

This evidence proves synthetic UI inference behavior, **not live ASN evidence or
private-IP ownership**. Canvas labels remain collision-bounded and may be omitted
in crowded graphs; complete bounded semantic details remain accessible. Physical
devices, other browsers, real screen readers, independent review and deployment/
release acceptance remain separate gates. Nothing is staged or committed.
