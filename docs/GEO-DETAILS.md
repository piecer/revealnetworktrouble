# Geo details v1 — Go, Web and Android contract

This document defines the frozen Stage 2 producer contract and its Web/Android
consumers. Both clients opt in, strictly parse and preserve supplemental details,
and expose them separately from legacy location/ASN facts. Source-backed tests
and independent review are distinct from deployed-service, live-provider,
physical-device and screen-reader acceptance; those are not claimed here.

## Negotiation and compatibility

Only `POST /api/v1/reports?geo_details=1` opts in. The existing request JSON,
`/api/v1/checks`, capabilities, nested `geolocation`/`asn`, error registry and
report semantics are unchanged. No query means no sidecar and byte-identical
legacy serialization of the same report. Existing fixtures are not regenerated.
An old server may ignore the query; a new client must accept its legacy response
without retrying the diagnostic.

The server parses `URL.RawQuery` with error-preserving `url.ParseQuery`. When the
decoded `geo_details` key is present it must occur exactly once and its decoded
value must be `1`. Percent-encoded equivalents are allowed. Empty, repeated,
other values and malformed query encoding (including another malformed parameter)
produce the existing 422 `invalid_request` before body/report admission or checker
execution. Existing authentication/rate-limit/draining precedence is unchanged.

## Exact sidecar shape

The optional top-level `geo_details` value, when present, is a non-null object:

| Required field | Contract |
| --- | --- |
| `schema_version` | Mathematical integer exactly 1 |
| `total` | Mathematical integer 0..6200; distinct eligible raw observed public addresses having a valid supplemental snapshot, before truncation |
| `omitted` | Mathematical integer 0..total |
| `entries` | Non-null array, length 0..500; `total = entries.length + omitted` |

Unknown keys, duplicate decoded keys, missing required fields, nulls, fractional
or out-of-range counts and contradictory arithmetic are invalid. Consumers must
use mathematical integers, not lexical integer-only rules: `1`, `1.0`, `1e0`,
`10e-1` are equivalent, and negative zero represents zero. Numeric strings and
booleans are not numbers. Malformed-present sidecars reject the report atomically;
do not silently fall back to legacy data. Detect duplicate JSON keys before a
parser can erase them, including duplicate root `geo_details` keys.

Each entry is a non-null object with exactly these required fields:

- `address`: canonical IP literal (`netip.Addr.Unmap().String()`), no whitespace,
  scope zone, IPv4-mapped spelling, hostname or noncanonical IPv6 compression/case.
  It must pass the existing `diagnostic.IsPublicDiagnosticIP` policy, including
  `blockedDiagnosticNetworks` in `backend/diagnostic/network_policy.go`.
- `provider`: exactly `ipwho.is`, identifying the adapter, not an accuracy claim.
- `source`: exactly `upstream` or `cache`.

Entries are unique and sorted by ASCII address string (not numerical IP order).
The exact optional **text** keys, in producer encoding order, are:

`city`, `region`, `country`, `country_code`, `continent`, `continent_code`,
`region_code`, `postal`, `timezone`, `isp`, `network_domain`.

Each present text field is a nonempty valid Unicode string of at most **256 UTF-8
bytes**. Their sum per entry is at most **1536 UTF-8 bytes**. Empty/absent provider
strings are omitted, not replaced with placeholders. Whitespace-only strings are
not trimmed or invented. There are no additional country/continent-code, postal,
timezone-name or domain-format constraints in this text contract. Optional text
may all be absent on the wire; this producer creates a supplemental snapshot only
when there is at least one validated nonempty supported text field. Provider
JSON null for an optional string is treated as absent by the typed adapter.

The only other optional keys are `fetched_at` and `expires_at`. They must be both
absent or both present as real Gregorian UTC timestamps in the exact 24-byte
spelling `YYYY-MM-DDTHH:mm:ss.SSSZ`, with expiry not before fetch. No offsets,
missing/extra fractional digits, impossible dates, leap-second spelling, null or
empty strings. Producer timestamp years are 0001..9999. They record local lookup
completion and cache expiry, **not** provider database update time. A cache hit
changes only `source`, retaining the original timestamp pair. Millisecond output
truncates sub-millisecond precision after converting to UTC.

No additional keys are allowed. In particular this sidecar contains no
coordinates, ASN, organization, radius, accuracy, Anycast, VPN, proxy or security
claims. `isp` is independent of legacy ASN organization. `network_domain` maps
only `connection.domain`; `timezone` maps only `timezone.id`. Other text keys map
to the provider's same-name root fields. Legacy ASN `org`/`isp` fallback remains
unchanged. These fields match the current adapter's public-provider protocol;
loopback tests do not prove live provider availability or geographical accuracy.

## Selection, provenance and resource bounds

`IPMetadata.GeoDetails` and `TopologyNode.GeoDetails` are hidden (`json:"-"`)
value snapshots: no mutable pointers, slices or maps are shared with the cache.
The existing legacy Geo/ASN decoder, `validIPMetadata`, coverage, success/failure
accounting and error-cache policy remain independent. Malformed supplemental
fields cannot invalidate otherwise-valid legacy Geo/ASN. Useful text-only data
can accompany the original legacy malformed error; it is propagated to raw hops
but still counted as a legacy failure and is not cached as success. Transport,
invalid JSON, duplicate-key, false/missing success and empty-success failures do
not receive provenance. The same upstream response is reused; no extra request
is made per hop or supplemental field.

`BuildGeoDetails` visits typed raw results/attempts/hops once, in their stored
order, before compact pruning. Eligibility requires `traceAttemptEligible`,
positive hop, responsive `healthy`/`degraded` node and a public literal address.
Failed/status-inconsistent attempts, local/unknown/private nodes and synthetic
`failure` destinations are excluded. This does not change the broader legacy
lookup predicate. The first valid **whole** snapshot for each canonical address
wins; no merging or later overwrite. The sorted selection may contain addresses
not retained by compact topology. Consumers may attach details only to matching
existing public nodes and must not synthesize nodes or positions, or describe the
sidecar as certifying independently aggregated legacy coordinates/ASN.

The standalone sidecar's **canonical Go encoding** is at most **131072 bytes**,
inclusive, excluding any newline. Count object punctuation, escaped string bytes
and the complete required envelope. Mirror `encoding/json` string escaping
(including quotes, backslashes, controls, `<>&`, U+2028/U+2029), UTF-8 BMP/non-BMP
encoding, omission and the declared field order. Mathematical counts use the
producer's canonical integer spelling for this budget. Raw equivalent numeric,
Unicode-escape and whitespace spellings need not have the same raw length; the
unchanged whole-wire limit still applies independently.

The server first fits the **unchanged legacy report**, retaining the exact typed
compact rollback winner. It then binary-searches complete sidecar prefixes (at
most 500 entries) against the standalone cap and every closed serializer limit.
Whole responses include the final newline: compact is **strictly below 1 MiB**;
full is **at most 8 MiB**. No extra legacy Geo/ASN, route, analysis or result facts
are removed to make room. If zero entries fit, a non-null empty `entries: []`
envelope reports the original total/omissions; if the envelope itself cannot fit,
the optional sidecar is omitted. Counts do not change compact truncation reasons.
Impossible injected raw sets over 6200 distinct valid addresses omit the entire
extension rather than fabricate a bounded total. No arbitrary raw JSON, custom
marshaler or raw HTTP writer capability is introduced.

## Producer witnesses and shared differential corpus

These files are generated by `backend/api/geo_details_fixture_test.go`:

- `testdata/geo-details-rich-full-report.json`
- `testdata/geo-details-rich-compact-report.json`
- `testdata/geo-details-empty-full-report.json`
- `testdata/geo-details-empty-compact-report.json`
- `testdata/geo-details-truncated-compact-report.json`
- `testdata/geo-details-corpus.json`

The five report witnesses execute loopback HTTP provider → strict decoder →
lookup/cache → actual traceroute parsing/enrichment → supervised Runner/analysis
→ production serializer. Trace command text and provider responses are test
inputs; only result duration/derived wall-clock age are normalized. The rich pair
includes cached observed (0,0), ISP different from organization, escaped Korean
and non-BMP text, upstream IPv6, text without coordinates and original legacy
failure, malformed extras with preserved legacy facts, HTTP failure with preserved
routes, and an excluded failed attempt. Empty witnesses retain legacy enrichment
of a synthetic destination without treating it as eligible evidence. Truncation
uses 501 distinct real cached lookups and retains 500 entries with one omission.

The corpus shares reusable standalone sidecar bases and one real rich full-report
prefix/suffix to avoid large duplicated fixtures. Its boundary bases are
producer-encoded controlled DTO inputs (not separate live-provider observations).
Consumers reconstruct **exact raw JSON bytes** as follows:

1. Read `bases[case.base]` as a string.
2. Apply each `case.edits` entry in order: replace the **first exact occurrence**
   of `old` with `new`; fail the corpus harness if it is missing.
3. Concatenate `report_prefix + sidecar + report_suffix`.
4. If `duplicate_root` is true, use
   `report_prefix + sidecar + ',"geo_details":' + sidecar + report_suffix` instead.
5. Pass these bytes through the real consumer's raw JSON entry point and compare
   acceptance to `case.accept`. Never parse/re-encode the intermediate sidecar.

The corpus includes exact/+1 256, 1536, 500 and 128 KiB boundaries, canonical/public
addresses, sorted/deduplicated identity, required/optional type and presence,
unknown/duplicate/version/count failures, paired timestamps and mathematically
equivalent number/Unicode escape spellings **at the byte cap**. Also exercise
absence of `geo_details` using unchanged existing legacy fixtures and all five
complete extended report witnesses. Preserve legacy raw JSON/export behavior.

Canonical tests compare committed bytes read-only. Explicit local regeneration,
which never touches legacy fixtures:

```sh
UPDATE_GEO_DETAILS_FIXTURES=1 go test ./backend/api -run '^TestGeoDetailsCommittedFixturesMatchProducer$' -count=1
```

A nonempty `CI` environment forbids generation. Freshness mismatch fails; a
one-byte mutation is rejected. Run the test twice without the update variable to
verify deterministic generation and unchanged committed bytes.

## Implementation hooks and gates

- `frontend/state.js` validates raw duplicate keys, exact numeric values and the
  closed sidecar. `frontend/topology-model.js` also validates the original own
  descriptor and sidecar before snapshotting direct object inputs; hidden fields,
  array holes or hooks cannot silently become a valid legacy fallback.
- Web `geo-map.js` presents selected-node supplemental text and provenance apart
  from legacy coordinates/ASN, including selected unlocated identities. Existing
  bounded selection, full-identity access and privacy/export behavior are retained.
- Android `GeoDetailsParser`/`GeoDetailsNumbers` validate immutable records;
  `ReportTransport` requests the sidecar in one POST; `GeoDetailsView` exposes
  bounded one-address-at-a-time details. Raw sharing retains the received JSON.
- Both consumers use the same 165 exact raw corpus cases and five producer
  witnesses below their ordinary report boundary. Their canonical tests include
  old-server no-retry, precision, malformed-present and immutability regressions.

- `backend/diagnostic/geo_details.go`: `GeoDetailSnapshot`, independent
  `decodeGeoDetails`, `GeoDetailsSidecar`/`GeoDetailsEntry`, limits,
  `GeoDetailsTimestampLayout`, `BuildGeoDetails`.
- `backend/diagnostic/geoip.go`: `decodeGeoIPResponse` gates JSON syntax;
  `decodeLegacyGeoIPPayload` preserves legacy rules; `completeFlight` stamps only
  validated snapshots; `Lookup` retains cache times and selects source.
- `backend/diagnostic/traceroute.go`: `enrichTopologiesWithCoverage` transports
  hidden snapshots even alongside legacy errors without reclassifying coverage.
- `backend/diagnostic/model.go`: optional `Report.GeoDetails`.
- `backend/api/server.go`: raw-query validation and
  `marshalCompactResponseRetainingSnapshot`, preserving the typed winner.
- `backend/api/geo_details.go`: `marshalReportResponse`, `fitGeoDetails`.
- `backend/api/report_payload.go`: static DTO allowlist and
  `marshalGeoDetailsSidecar` through the same closed encoder/validator.

Producer regressions include structural hidden-field mutation, default fixture
identity, raw/compact scope, empty-envelope fallback, exact whole-wire limits,
string/container/node budgets, maximum rollback cache reuse, complete-prefix
linear oracle comparison and logarithmic marshal/cumulative-byte bounds. Required
producer gates are `make test`, `make build`, `make vet`, `make web-test-syntax`,
uncached focused/race tests and exact fixture freshness. Android gates, consumer
integration/review, source-backed extended API→browser flow, external provider and
deployed/physical acceptance are not established by this producer slice.
