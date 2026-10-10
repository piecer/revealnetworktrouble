# On-demand IP context — backend v1

This producer is separate from diagnostic reports. The Web and Android context
controls/parsers are subsequent stages, not implemented by this backend change.
The controlling reviewed contract is campaign `contract-v1.md`, SHA-256
`0f3e987b665c231a82d2c20fdaac4cfb8f54593fe82c4262012df4829be339b0`.

## Request and authority

`POST /api/v1/ip-context` accepts exactly `{"address":"1.1.1.1"}` (or a canonical
public IPv6 literal). The complete UTF-8 body, including whitespace, is at most
256 bytes. One object, depth at most two, four materialized JSON tokens, no
query string, duplicate decoded keys, null, invalid Unicode, hostname, zone,
IPv4-mapped address, whitespace or noncanonical address spelling. A fixed-byte
lexical pass checks duplicate keys even in invalid shapes before the small
projection; it does not materialize those shapes. Public-address policy is
always the existing `IsPublicDiagnosticIP`, including trusted-local deployment.
The endpoint cannot prove report membership: there is no report store. Clients
must offer only their current report's eligible responsive public observations.

The existing CORS, Bearer authentication, client rate limiting, method handling,
draining and closed error registry remain in force. Existing report/checks/Geo
contracts and fixture bytes are unchanged. No Runner or Geo lookup occurs here.
Grammar/Unicode/duplicate errors use `400/invalid_json`; semantic errors use
`422/invalid_request`; body limit uses `413/request_too_large`. Disabled or absent
service and context saturation reuse `503/server_busy`. Shared decode/write
limits keep their existing rows. Provider failures are component statuses, not
new API errors. Unexpected local service/handler failure uses the existing
`500/internal_error`. Legacy fixed error messages are intentionally unchanged.

A body failure while its request context is live uses `invalid_json`. After
cancellation the application writes nothing and records terminal 499. In Go's
real HTTP/1 server, a socket read deadline itself cancels the request context;
the transport can synthesize empty 200 headers on handler return, with **zero
application response bytes**. This is not an IP-context success payload. The
real-socket regression distinguishes this transport behavior from a live
injected body-read deadline, which gets the registered 400 response.

## Closed response

Every response is schema version 1 and has exactly `schema_version`, `address`,
`source`, `fetched_at`, `expires_at`, `reverse_dns`, `registration`, `routing`.
`source` is `upstream` or `cache`. Every nested object is closed; null is invalid;
empty collections stay `[]`. The canonical Go encoding plus newline is at most
16,384 bytes, validated before any success headers. A dedicated typed responder
capability is the only success publication path.

Integer fields use exact mathematical-integer semantics, not float rounding:
`1.0` and `1e0` are integers; quoted numbers and rounded fractions are not.
Timestamps are Gregorian UTC `YYYY-MM-DDTHH:MM:SS.sssZ`, years 0001–9999. Root
acquisition time is no earlier than component/RPKI completion. Expiry is exactly
300,000 ms after a stable snapshot, or 30,000 ms when any transient/provider error
is present. These are local acquisition/cache times, not DNS TTL or provider
update times. Cache changes only the root source, never timestamps or facts.

### Reverse DNS

Required: `status`, `source=system_resolver`, `fetched_at`, `names`, `omitted`.
Statuses: `ok`, `limited`, `not_found`, `timeout`, `unavailable`,
`invalid_response`. Each name has exactly `name` and `forward_status`.
Names are lowercase ASCII DNS presentation, sorted and unique, no final dot,
1–253 bytes, labels 1–63 bytes using letters/digits/underscore/hyphen, no edge
hyphen. Forward status is `confirmed`, `mismatch`, `not_found`, `timeout`,
`unavailable`, `invalid_response`, or `limited`.

At most 64 returned PTR names are admitted, all validated before retaining the
first eight. `ok` means 1–8 names and omitted=0; `limited` means eight names and
omitted=1–56. Other states require []/0. Forward queries are absolute (`name.`),
sequential, and never dial returned addresses. More than 16 addresses gives
`limited` before any confirmation; malformed addresses invalidate that name.
An empty list or resolver not-found is not_found. Forward failure does not erase
valid reverse names. The system resolver may use hosts/cache; this is not an
assertion of authoritative wire PTR, DNSSEC, device identity, trust or location.

### Registration

Required: `status`, `source=rdap`, `fetched_at`. Statuses: `ok`, `not_found`,
`timeout`, `unavailable`, `rate_limited`, `invalid_response`. Optional `registry`
is one of arin/apnic/ripe/lacnic/afrinic and may remain on failures. Success
requires canonical `start_address`/`end_address`, same family, containing the
requested address. Optional success-only fields: `handle`, `name`, `type`,
`country`, `organization`, `registered_at`, `updated_at`. Nonempty text is at
most 256 UTF-8 bytes per field and 1,536 combined; country is two uppercase ASCII
letters and is **registration country, not location**. Empty optional provider
text is omitted; malformed recognized data invalidates the bundle.

Registration events select earliest registration/latest last changed and emit
UTC milliseconds. At most 32 events and 32 top-level entities, 16 vCard rows per
entity. Only top-level registrants with explicit vCard kind `org` contribute the
first nonempty fn in source order. No entity recursion, contacts, addresses,
phones, emails, arbitrary links, remarks, or raw provider errors are projected.
Registration is not necessarily the current operator or physical address.

Pinned IANA tables select the longest matching prefix and reviewed HTTPS RIR
base. There is no runtime bootstrap fetch, referral traversal or configurable
user/provider URL. No match yields not_found without registry. HTTP 404 means
not_found, 429 rate_limited, other non-2xx unavailable. Foreign network ranges,
malformed/budget-exceeding 2xx responses are invalid_response.

### Routing and RPKI

Routing requires `status`, `source=ripe_ris`, `fetched_at`, `origins`, `omitted`.
Statuses: `ok`, `limited`, `not_found`, `timeout`, `unavailable`, `rate_limited`,
`invalid_response`. Success requires canonical masked `prefix` containing the
queried address. Admit at most 64 upstream ASNs, fully validate, deduplicate,
sort numerically and retain four. ASN is 1–4294967295; provider decimal strings
are allowed only in the upstream decoder, never the public wire contract.
`ok`: 1–4 origins/omitted0; `limited`: four/omitted1–60; failures: []/0/no prefix.
A valid empty prefix plus empty origins is not_found; missing/contradictory data
or non-ok provider envelope is invalid_response. RIS is external observation,
not the measured packet route; its documented source is an eight-hour data dump,
not instantaneous announcements.

Every origin is exactly `{asn,rpki}`. RPKI requires `status`,
`source=ripe_rpki`, `checked_at`; statuses are `ok`, `timeout`, `unavailable`,
`rate_limited`, `invalid_response`. Only ok has `validity`: `valid`,
`invalid_asn`, `invalid_length`, or `unknown`. Echoed ASN and canonical prefix
must equal the queried pair. Unknown is no applicable ROA, not failed lookup.
RPKI failure retains prefix/origins and other facts. This is origin authorization,
not a security/health verdict. RIPE non-200 responses: 429 rate_limited, all other
statuses (including redirects/404) unavailable. No retry or redirect occurs.

## Resource ownership and composition

- Four admitted context handlers, covering body/lookup/write and panic recovery.
  Report admission remains independent. Decode and successful-response write
  semaphores are shared with reports; tiny rejection writes retain existing
  registry-bound behavior rather than claiming a new all-writes semaphore.
- Two cold IP acquisition flights globally, no queue. Same-IP callers coalesce;
  four bounded waiter identities independently obey cancellation. Flight deadline
  is initiating request observation +6s, never extended by joins. Wire deadline
  is observation +8s (earlier caller deadline wins); body deadline <=1s. No
  post-provider extension of the write deadline.
- Three independent component workers per flight. Reverse and RDAP/BGP steps
  have <=2s; retained RPKI origins run sequentially in one <=2s group budget.
  Maximum cold acquisition: one PTR, eight forward, one RDAP, one BGP, four RPKI:
  15 logical operations, at most six actual HTTP sends. Resolver packet counts
  are implementation-dependent. Upstream bodies <=262,144 bytes, JSON depth16,
  tokens16,384. At most three primitives/flight, six globally.
- The feature owns a one-shot, verified HTTP/1.1 adapter, separate from report
  transports. It reuses the public-address predicate and bounded candidate
  deadline rule, **not** NetworkPolicy's detached32 worker implementation.
  All DNS answers are validated before any dial; vetted IP dialing preserves
  Host/SNI and TLS verification. No environment proxy, alternate protocol hook,
  redirect, transparent retry, connection reuse or idle pool exists. Resolving,
  dialing, TLS, sending, body reads and cancellation-close watcher are inside the
  generation's transitive lifetime. No next candidate starts while a predecessor
  callback remains alive. Late connections/bodies are closed without publication.
- Context-ignoring callbacks stay charged until actual exit, even after a timeout
  snapshot is delivered. Last waiter cancellation permanently abandons a
  generation; publication checks live waiter contexts, not just retained counts.
  Late finally/result cannot clear or overwrite another generation. A timeout
  snapshot is immutable and safely cacheable only with a live publication owner.
- At most 256 deterministic LRU snapshots; lazy expiry, no periodic worker or
  per-entry timer. Every returned value owns its arrays. Stable states are reverse
  ok/limited/not_found, forward confirmed/mismatch/not_found/limited,
  registration ok/not_found, routing ok/limited/not_found and every RPKI ok.
- cmd startup creates the service in both deployment modes. BeginDrain closes
  admission/cancels generations before HTTP shutdown. Close joins actual work
  under the same application shutdown deadline as HTTP/checker drain and reports
  incomplete if a callback still exists. Constructor does no bootstrap networking.

Dependency injection in `IPContextOptions` is for trusted composition/tests, not
request configuration. Production defaults use the system resolver/dialer and
system trust roots; fixture dial mapping/private CA is never a deployment bypass.
The feature adds no persistent history, report field, raw/human export content,
Geo mutation, endpoint probing, paid provider, scan or automatic client lookup.
Telemetry contains only closed route/count/outcome fields, never queried IP,
provider text, request headers, credentials or raw provider bodies.

## Pinned data and regeneration

`backend/diagnostic/ip_context_data/provenance.json` records official source URLs,
publication dates, retrieval date, original fetched-byte hashes and separate
hashes of the shipped JSON-reserialized artifacts. Do not confuse these hashes.
IPv4 publication: 2019-06-07T19:00:02Z; IPv6: 2024-11-01T22:00:01Z; retrieved
2026-10-10. The provenance and tables are embedded. Updating requires reviewed
HTTPS bases, prefix changes, new provenance and passing bootstrap/adapter tests;
there is no automatic refresh.

`testdata/ip-context-corpus.json` is generated by actual service execution and the
production serializer/validator, plus explicitly labeled deterministic mutations.
Each case supplies a captured `requested_address`, `accept`, exact `wire_base64`
bytes, and (for accepted cases) the producer-normalized `projection`. Decode
base64 to bytes first; do not normalize numbers/Unicode/duplicates before the
consumer's strict parser. Compare acceptance and normalized values. Projection
is a producer-derived oracle, not a claim of independently implemented parsing.
Web direct-object prototype/getter/hole guards remain the later Web owner's gate.

Explicit regeneration (never done silently by normal tests):

```sh
CHECKNETWORK_UPDATE_IP_CONTEXT_FIXTURES=1 go test ./backend/diagnostic -run '^TestIPContextProducerCorpus$' -count=1
```

The permanent Go gate generates twice in memory, compares exact corpus bytes to
the retained file, checks unique IDs, and exercises every accepted/rejected case.
Original report/Geo/error fixtures are not regenerated. Required producer gates:
`make test`, `make test-race`, `make build`, `make vet`, `make web-test-syntax`,
focused race repetitions and controlled actual API → verified TLS provider tests.
No public-provider availability, deployed port3000, Web/Android integration,
physical-device or screen-reader acceptance is implied by these backend gates.
