# Stage 5 Android parity and lifecycle plan

Base: `a1262aa107b3b55213bb921dc2fe3f586cb884d0`

## Acceptance contract

Stage 5 is one verified commit in its detached worktree. Every behavior change uses RED → GREEN. The final SHA must pass independent backend/security, Android lifecycle/UI, and integration/build reviews with blocker/major 0 before a fast-forward into `main`.

## Runtime defects to close first

1. Add a reproducible Android build and test gate: Gradle Wrapper 8.11.1 with distribution and wrapper-JAR checksums, JDK 17, Android platform 35/build-tools 35.0.0, nonzero JVM tests, lint, debug and release assembly.
2. Extract Android-free immutable request/report/error/state components from `MainActivity` so JVM tests can exercise all trust boundaries.
3. Bound request and response bytes, Content-Length and chunked streams; enforce an absolute call deadline; disconnect exactly once on cancellation.
4. Introduce owner ID plus canonical input signature and explicit idle/loading/ready/error/cancelled state. Reject stale completion/finalization after replacement, input mutation, recreation, cancellation, and final destruction.
5. Parse and validate reports atomically before publication or sharing. Normalize 401/422/429/503 and Retry-After without reflecting arbitrary server or credential text.
6. Separate debug local HTTP from release HTTPS validation. Send Bearer only to HTTPS and never persist it in ordinary preferences, state, report, logs, or shares.
7. Replace raw Binder text sharing with redacted human text by default and bounded FileProvider raw JSON only after explicit user action.
8. Remove portrait lock, restore bounded form/report state, provide contextual labels/live regions/focus, and make 320dp/large-font/landscape layouts usable.

## Capability slices

1. Capability model and request builder for all 13 server kinds. HTTP/HTTPS support editable expected status; traceroute supports 1–10 attempts and requests `topology_mode: compact`.
2. Typed report model preserving healthy/degraded/unreachable and optional additive analysis.
3. Native information hierarchy: report identity/status/coverage → findings → evidence → actions → limitations/provider failures → folded raw results.
4. Bounded textual compact-topology/Geo coverage summary. Interactive Android topology/Geo remains a documented follow-up rather than an untested Stage 5 claim.
5. Retry/cancel UX and saved-state restoration tied to request ownership.

## Test matrix

- Plain JVM: URL/build-type/auth policy, all request kinds and option boundaries, strict report/analysis schema, response byte boundaries, structured errors, Retry-After, owner replacement/cancel/stale callbacks, saved-state codec, deterministic redacted human export.
- Robolectric/resource tests only where Android is required: Activity recreation, actual controls, live regions/focus, 320dp/landscape/font scale, contextual accessibility names, sharing Intent/FileProvider.
- Build gates: wrapper verification, nonzero `testDebugUnitTest`, `lintDebug`, `assembleDebug`, `assembleRelease`, merged cleartext policy inspection.
- Existing repository gates remain mandatory.

## Honest limitations

No emulator, physical device, TalkBack, Switch Access, OEM share sheet, native Windows traceroute, or real mobile network behavior may be reported as verified without actually running it. Device-only acceptance remains open after automated Stage 5 closure.
