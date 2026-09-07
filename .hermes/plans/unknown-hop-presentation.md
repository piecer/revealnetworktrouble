# Unknown-hop presentation restoration

Base: 4fb1b2df0cb9d0575a383114d7f3f4e6ee0203a3, detached isolated worktree. Main compose.yaml is caller-owned and untouched. No stage/commit, Docker, or deletion.

## Contract
- Keep raw/normalized observed nodes, links and routes unchanged by visibility. Select targets first.
- Derive separate bounded presentation nodes and connectors from route occurrences; consecutive unknown runs fold; hiding uses dashed `무응답 N홉 생략` connectors, not observed adjacency.
- Preserve responsive tails, rich hover/editor and individual drag in 2D/3D. Unknown groups have no editable IP.
- Count projected elements under 500 nodes / 1000 connections / document 1200. Conservative raw-prefix planning may still impose resource truncation before presentation.

## Work and gates
1. Inspect historic folding and current closed model/planner/Canvas paths.
2. Executable RED/GREEN regression for folding, then hiding; adversarial route identity, immutability and budgets.
3. Integrate separate presentation layer into planner, Canvas and inspector without changing producer schemas.
4. Real Playwright on private static port, producer fixture and captured-report replay if available; both modes, rich interactions.
5. Update current contract docs/counts; full make test/build/vet/web-test-syntax and diff check after final edits.

Status: implementation ready for final local gates; independent review pending (not Stage 9/release acceptance).

## Recovery and regression evidence
- Repaired accidental read-file line-number prefixes only on changed Go/documentation lines; preserved historical rich-topology inventory 291.
- Current Node inventory observed at 298 passing tests, plus the separate app contract script. Final canonical gates must be rerun after this document edit.
- Previously RED self-return Canvas regression is GREEN. Added budget assertions exposed omitted connectors on routes still marked complete; repaired to retain only a contiguous presentation prefix and clear completeness after truncation.
- Each projected route transition requires its exact retained occurrence/position connector. An observed pair from another route or an earlier repeated bypass cannot substitute for an omitted span (RED then GREEN).
- Focused unknown-presentation/renderer run: 30 passing tests including 2D/3D folded hover, dashed connectors, attached-edge drag and non-editable groups.

## Final gate procedure and limits
- Run `make test`, `make build`, `make vet`, `make web-test-syntax`, both offline Python archive-validator suites and `git diff --check` on these final bytes.
- Replay unknown-presentation, rich-topology and graph-first browser scripts using external Playwright Chromium, private port 18786, producer fixtures and captured REPORT log; retain fresh JSON/screenshots outside the repository.
- Visibility itself never truncates a responsive tail. Resource admission remains conservative: observed-prefix budgeting precedes presentation, and shared-unknown occurrence expansion can require additional presentation truncation. No claim of all routes fitting arbitrary budgets or optimal presentation packing.
- Self-return bypass is a visible non-observed curved mark, not a new observed self-edge. Bypass explanation is in the persistent legend/semantic inspector, not Canvas edge-hover hit-testing.
- Physical Chrome/Firefox/Safari, real screen-reader, Android, Docker/release and independent review remain outside these local gates. Build artifacts and private server are retained under the no-deletion instruction. No stage/commit or main-worktree edits.
