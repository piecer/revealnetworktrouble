# Offline Geo map: provenance, boundaries and verification

## Current Stage3 behavior (isolated source candidate)

This is offline geographic context, not a router-location measurement or a deployment claim. Stage1 identity/detail access and Stage2 supplemental metadata remain separate from context labels. Independent acceptance/integration is recorded by the campaign owner, not inferred from this document.

The existing nine production assets are unchanged as a set. Only `geo-map.js` adds production behavior; it imports the existing topology route helpers and embeds data, so no runtime data asset, tiles, CDN, fonts, provider requests, or new dependency is needed. Raw reports, exports, public-IP eligibility, producer and Android contracts are unchanged.

### Pinned geography

Natural Earth v5.1.2 commit `f1890d9f152c896d250a77557a5751a93d494776` supplies:

- Existing LAND: 127 polygons / 5143 vertices, preserved byte-for-byte.
- `ne_110m_admin_0_boundary_lines_land.geojson`: 331 features, **333 separate line parts**, 3108 vertices. Multipart breaks and order are retained, with no simplification, rounding or invented joins.
- `ne_110m_admin_0_countries.geojson`: 177 official `LABEL_X`/`LABEL_Y` points, names and `LABELRANK` values.
- `ne_110m_populated_places.geojson`: 243 official point geometries, names and `POP_MAX` values.

`docs/vendor/Natural-Earth-LICENSE.md` is the verbatim official public-domain notice (including original whitespace). Natural Earth attribution remains on the Canvas. Country/territory names and borders are source cartography, not a claim about network ownership or contested sovereignty.

The development transformer pins all three full input SHA256 values in `scripts/geo_context_data.py`. Supply a directory containing those exact three filenames; it hashes every input before deriving anything. No download is performed and full source GeoJSON is not vendored by default.

```sh
python3 scripts/geo_context_data.py verify /explicit/local/natural-earth-directory
python3 scripts/geo_context_data.py emit /explicit/local/natural-earth-directory
```

`emit` prints compact `BORDERS`, `COUNTRIES`, `CITIES` declarations, never rewrites a product file. `verify` compares every coordinate/name/priority and deterministic serialized byte against the embedded declarations. Country records use `[LABEL_X,LABEL_Y,NAME_KO or NAME_EN,LABELRANK]`; cities use `[point longitude,point latitude,NAME_KO or NAME_EN,POP_MAX]`. Borders flatten only the multipart container. Original order is retained, so equal-population ties use original city indices.

Default offline `npm test` checks independently frozen generated-data SHA256 digests and structural limits. This establishes embedded artifact identity, **not** a fresh upstream download. The explicit-directory verification is a separate full input-to-output gate.

### Labels, exact groups and routes

World zoom considers countries with LABELRANK <=3; relative zoom >=2 considers all countries, and >=3 additionally considers cities in descending population/original-index order. Labels use Korean where supplied, otherwise English, with fixed 12/11 CSS-pixel typography. Full ink rectangles plus halo and four-pixel spacing are culled/collision-rejected deterministically; marker/group rings, attribution and the empty-data message are reserved. At most 64 context labels are painted. Empty data still paints recognizable land, ocean, borders and country context.

Only exact positions group, after longitude wrapping and signed-zero equivalence. Already canonical tiny coordinate differences are preserved; there is no jitter, rounding or proximity clustering. A group badge shows its count. Repeated taps cycle original marker order; detail identifies member/count and explains that shared coordinates do not imply the same device. Per-node fact caching does not cache the currently selected group member. All original identities remain reachable through the existing fixed list (at most 100 buttons), pagination and previous/next controls, including 500 coincident members or no available list slots. Singletons keep their prior selection ring. Unlocated selected nodes remain unlocated.

Colors use the existing `routeColor`, `edgeRouteMemberships` and four-lane cap, based on original `result_index`, not attempts or filtered positions. Only existing `geo.segments` are drawn; route facts never create new geometry across missing-coordinate gaps. A selected node emphasizes segments in its retained routes; unrelated segments stay visible at 0.35 alpha. Shared edges display at most four original-result colors; complete route/attempt membership remains available in detail. Line and arrow use the same shortest wrapped endpoints, copies and lane offsets. Fact-free legacy renderer mounts retain a neutral fallback rather than inventing route identity.

### Hard work bounds and ownership

Each actual scheduled draw publishes counters on its Canvas, incremented where work occurs:

| Work | Ceiling |
| --- | ---: |
| Land + border vertex projections | 24753 |
| Label candidate projections | 1260 (420 records × 3 copies) |
| Painted geographic labels | 64 |
| Exact groups / projected group copies | 500 / 1500 |
| Segment-lane world copies | 12000 (1000 × 4 × 3) |
| Canvas CSS extent / DPR | 2048 × 1024 / 2 |
| Backing pixels | 8388608 |
| Markers / segments | 500 / 1000 |
| Document elements / insertion chunk / list buttons | 1200 / 100 / 100 |

Resize, pan, zoom and selection coalesce scheduled work. There is no polling/animation loop. Abort, detached roots and stale detail owners cannot paint or mutate replacement controls; original Stage1 real-click visibility and Stage2 source/cache text remain retained gates.

### Verification commands and evidence scope

```sh
make frontend-deps
make test
make build
make vet
make web-test-syntax
python3 scripts/verify_api_archive_test.py
python3 scripts/verify_web_archive_test.py
# Installed external Playwright binding, no new production dependency:
PLAYWRIGHT_PATH=/explicit/installed/playwright-core node frontend/geo-context.browser.cjs /owned/evidence/context
PLAYWRIGHT_PATH=/explicit/installed/playwright-core node frontend/geo-details.browser.cjs /owned/evidence/details
PLAYWRIGHT_PATH=/explicit/installed/playwright-core node frontend/geo-details-producer.browser.cjs /owned/evidence/producer-replay
```

The Stage3 browser gate checks all nine served asset bytes, actual border/country/city raster differences (not call counters alone), rectangle spacing, real mouse/touch group cycling, 500-member list access, four-lane palette/emphasis, both seam directions, empty geography, retained fullscreen/mobile immediate identity visibility, stale callbacks, overflow and external requests. `GEO_CONTEXT_LONG_PATH_MUTANT=1` is a test-only served-source negative control and must fail the no-long-route pixel assertion. Existing Geo/adversarial/graph/route/unknown browser assertions are also retained; the app Geo palette oracle now derives original-result colors and excludes marker pixels.

Campaign receipts under sibling `stage3-evidence` bind command exits/logs to exact source manifests, source-directory verification, screenshots, owned-process cleanup, and seed-relative patch replay. Canonical counts come from those fresh logs, not the historical counts below. Browser inputs are controlled synthetic renderer cases or frozen producer replays, not external GeoIP, deployed-service or physical-device acceptance. Unchanged npm dependencies report two high-severity advisories; no migration is part of this slice.

## Historical pre-Stage1 basemap evidence (not current readiness)

The following records describe an earlier basemap-only slice. Its 328-test inventory, live API observations, temporary paths and open gates are historical, not current Stage3 acceptance. Older inventories 326/320/308/304/298 are likewise historical; browser cases were never added to Node collection counts.

## Geographic source and reproducibility

`frontend/geo-map.js` vendors Natural Earth **v5.1.2**, `ne_110m_land.geojson` at commit `f1890d9f152c896d250a77557a5751a93d494776`. `git ls-remote https://github.com/nvkelso/natural-earth-vector.git refs/tags/v5.1.2` returned that commit. The upstream [terms of use](https://www.naturalearthdata.com/about/terms-of-use/) explicitly place all versions of raster/vector data in the public domain, permit modification and redistribution, and require no permission or credit. The map nevertheless displays Natural Earth attribution. This is an equirectangular, low-resolution land overview, not a street map, country-label layer or precise physical traceroute.

Reproduce the geometry verification from the repository root (the network download is a development verification step, never a browser runtime dependency):

```sh
curl -fsSL https://raw.githubusercontent.com/nvkelso/natural-earth-vector/f1890d9f152c896d250a77557a5751a93d494776/geojson/ne_110m_land.geojson -o /tmp/geo-final-ne.geojson
python3 - <<'PY'
import hashlib, json, re
from pathlib import Path
source = Path('/tmp/geo-final-ne.geojson').read_bytes()
assert hashlib.sha256(source).hexdigest() == '9e0729ee253ca7d7a5c4ae9395fb1902264c5377c52e224d13dd85010e2835d9'
features = json.loads(source)['features']
expected = []
for feature in features:
    geometry = feature['geometry']
    assert geometry['type'] in ('Polygon', 'MultiPolygon')
    expected.extend([geometry['coordinates']] if geometry['type'] == 'Polygon' else geometry['coordinates'])
module = Path('frontend/geo-map.js').read_text()
land = json.loads(re.search(r'export const LAND = (.*);', module).group(1))
assert land == expected
assert len(land) == 127
assert sum(len(ring) for polygon in land for ring in polygon) == 5143
print('Pinned source and every coordinate verified: 127 polygons / 5143 vertices')
PY
```

The transformation removes feature properties and takes Polygon coordinates (or flattens MultiPolygon coordinates) in original order. No coordinate is rounded, simplified or invented. Structural equality against the newly downloaded, hash-pinned source was verified. JSON whitespace/number spelling is not a geographic transformation.

## API → normalizer → plan → pixels

`createApp` parses the API response through `parseResponse` / `normalizeReport` in `state.js`, then calls `topologyModelFromReport`. The coordinator plans the active Geo view with `planTopologyDOM`, and `app.js:drawGeo` mounts `mountGeoMap` with that plan. `topology-model.js:hasGeo` retains the existing `public_ip === true` plus finite-coordinate gate. Segments require both endpoint locations and an observed selected adjacency; unknown hops are never fabricated as positions or direct observed links. All raw analysis/report facts remain separate from presentation.

The browser gate independently applies the exact served normalizer/model/planner to the actual response, asserts raw JSON is unchanged, and compares planned marker/segment counts with the mounted Canvas. Only aggregate counts are emitted; reports, request URLs, addresses and coordinates are not dumped. Screenshots are map-only and stored in a private output directory, not repository artifacts.

Observed during this slice (desktop 1440 / mobile 375):

| Source | Raw / normalized results | Compact / model nodes | Planned & mounted markers | Planned & mounted segments | DOM |
| --- | --- | --- | --- | --- | --- |
| Actual API8090 | 1 / 1 | 17 / 17 | 6 / 6 | 3 desktop, 2 mobile | 281 |
| Saved report replay | 1 / 1 | 17 / 17 | 7 / 7 | 4 / 4 | 282 |
| Permanent no-Geo producer fixture | 1 / 1 | 4 / 4 | 0 / 0 | 0 / 0 | 275 |

Live requests are separate observations and counts can vary. Actual API land pixels were 166625 / 41269; saved replay 166619 / 41272. The no-Geo fixture paints real land/ocean, has zero marker/route-color pixels, and explicitly explains that no public-IP coordinates were identified. All three modes had no horizontal overflow, no page errors and no third-party browser requests.

The private source origin is not on API8090's live CORS allowlist (preflight returned 204 without allow-origin), so direct browser access initially timed out. `GEO_LIVE=1` now forwards the **unchanged browser POST** through Playwright `route.fetch` to the actual API and fulfills the unchanged response. This is genuine fresh API data, **not** a captured replay and **not** proof of deployment CORS compatibility. No live3000 files/configuration or Docker resources are changed.

## Permanent adversarial coverage

- Unit RED reproduced an observer acquired on pre-aborted mount and a null-camera exception when panning after Canvas failure. Minimal guards make both GREEN.
- Detached/stale frames cannot paint; abort cancels pending frames and detaches controls; repeated disposal is safe.
- Oversized viewport/DPR and 501-marker/1001-segment input are capped to 4096×2048 backing pixels, 500 markers and 1000 segments. Land is exactly 5143 vertices × at most three world copies; no continuous draw loop.
- Real Chromium tests shortest seam rendering at both widths in both directions using explicitly synthetic renderer-only ±175° coordinates. They require cyan pixels inside the short route bounds, **zero** far cyan pixels, and asymmetric lime triangle pixels pointing in the traversal direction. A test-only interception replacing wrapped displacement with direct longitude failed with **560 far cyan pixels**, proving the pixel gate catches a world-spanning regression. No production file was mutated.
- Browser wheel plus 50 resize events coalesce into one frame; abort leaves zero pending/stale draws. App browser gates exercise zoom/fit, keyboard pan/Home, real pointer drag, and three view replacement cycles with stable DOM counts and no hidden Geo Canvas.
- Existing rich-topology unknown-fold browser fixture still passes show/hide × 2D/3D × desktop/mobile (eight scenarios). No public-IP gate, raw topology, normalization or unknown-fold semantics changed.

## Exact commands and evidence locations

Run in `/home/piecer/dev/src/revealnetworktroble-geo-basemap-0511b93`. Earlier servers were discoverable on 18763/18786 but ownership could not be proven and 18786 returned 404 for `geo-map.js`; neither was modified. This slice launched its own source-only server:

```sh
python3 -m http.server 18897 --bind 127.0.0.1 --directory /home/piecer/dev/src/revealnetworktroble-geo-basemap-0511b93/frontend
```

Health/source readiness was verified by fetching `geo-map.js` and comparing SHA256. Both browser entry points verify exact served source bytes (the app gate checks all nine production assets).

```sh
export PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright
GEO_LIVE=1 node frontend/geo-map.browser.cjs http://127.0.0.1:18897 /tmp/geo-final-live
GEO_REPORT=/tmp/geo-live-report.json node frontend/geo-map.browser.cjs http://127.0.0.1:18897 /tmp/geo-final-replay
GEO_EMPTY=1 node frontend/geo-map.browser.cjs http://127.0.0.1:18897 /tmp/geo-final-empty
node frontend/geo-map-adversarial.browser.cjs http://127.0.0.1:18897 /tmp/geo-final-seam
node frontend/unknown-presentation.browser.cjs http://127.0.0.1:18897 /tmp/geo-final-unknown
make test
make build
make vet
make web-test-syntax
python3 scripts/verify_api_archive_test.py
python3 scripts/verify_web_archive_test.py
git diff --check
```

Each browser directory contains its aggregate `results.json`; Geo directories include map-only PNGs. `/tmp/geo-final-red.log` records the two unit failures, `/tmp/geo-final-seam-mutant.log` the intentional pixel mutation rejection. Final canonical command logs use `/tmp/geo-final-{test,build,vet,syntax,api-archive,web-archive}.log`. Evidence paths are local, not portable release artifacts. Preserve existing temporary data under the non-deletion instruction.

Production closure is nine assets in Docker COPY/hash/timestamp, offline validator exact selected set, release source/served-byte checks, borrowed-base read-only mounts and corresponding contracts. Offline API/Web validators use synthetic archives (28 and 25 cases), not a claim that an uncommitted Docker image was built or deployed. No stage, commit, push, main-worktree edit, Docker lifecycle or deletion is authorized. `make build` output is retained. Independent review, exact-SHA release/buildx closure, Android CI, other physical browsers and screen-reader acceptance remain separate outstanding gates.
