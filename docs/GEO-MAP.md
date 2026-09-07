# Offline Geo map: provenance, boundaries and verification

This uncommitted Geo slice is ready for independent source review after its fresh gates; it is not a release or deployment claim. Historical Stage 8/9 counts and release acceptance remain separate. Current Node runner inventory is **320 tests**, including ten Geo unit cases and the ASN-context slice. Geo inventories 308/304 and pre-Geo 298 remain historical. Browser scenarios are separate, not added to the Node count.

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
