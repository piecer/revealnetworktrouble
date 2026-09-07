# 2D/3D 경로 토폴로지 복원 구현 계획

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Stage 8에서 시각 그래프가 사라진 회귀를 복구하고, 같은 검증된 토폴로지 모델을 2D 또는 3D Canvas 그래프로 선택해 볼 수 있게 한다.

**Architecture:** 현재 strict report/compact parser, bounded topology model, progressive owner-safe coordinator는 유지한다. 토폴로지 view에 Canvas 기반 시각 레이어를 하나 추가하고, 기존 노드/링크/경로 DOM은 화면 읽기·키보드·라벨 편집을 위한 bounded semantic inspector로 유지한다. 2D와 3D는 데이터나 분석 결과를 바꾸지 않는 순수 view projection이며 외부 CDN/런타임 의존성을 추가하지 않는다.

**Tech Stack:** Vanilla ES modules, Canvas 2D API, HTML/CSS, Node 22 test runner, jsdom 30.0.1.

**Status:** Stage 9 implementation is pending final gates/review, not complete. Tasks 1–5 are implemented in the worktree; Task 6 canonical/real gates, independent review, manual browser/screen-reader acceptance, commit, and exact-SHA closure remain pending.

---

## Root cause

- `frontend/styles.css`에는 과거 `.node-marker`, `.node-card`, 연결선 구조를 위한 스타일이 남아 있다.
- 현재 `frontend/topology-renderer.js`의 `materializeTopologyItem`은 node/link/route를 각각 텍스트 한 요소로만 만든다.
- `frontend/topology-model.js`는 그래프의 nodes/links/routes 데이터를 계속 보존하지만 시각 Canvas/SVG item을 topology plan에 만들지 않는다.
- 결과적으로 데이터와 접근 가능한 목록은 남아 있으나 사용자에게 보이던 경로 그래프는 사라졌다.

## Acceptance contract

1. 기본 선택은 `2D 그래프`이며 토폴로지 실행 후 노드와 방향 링크가 Canvas에 보인다.
2. `3D 그래프`를 선택하면 동일한 node/link set을 deterministic perspective projection으로 표시한다.
3. mode 전환은 네트워크 재요청 없이 현재 모델을 다시 그린다.
4. pan/rotation/zoom은 pointer, wheel, 키보드로 동작하며 `Reset view`로 복원된다.
5. reduced-motion에서는 animation을 사용하지 않는다. 이 구현은 기본적으로 정적이며 interaction 때만 redraw한다.
6. Canvas는 장식 전용으로 끝나지 않는다. 동기화된 bounded semantic node inspector와 상태 live region을 유지한다.
7. Canvas 하나와 고정 control 수만 추가하며 document-wide 1,200 element 한도를 지킨다.
8. model limits(nodes 500/links 1,000/routes bounded), stale owner cancellation, hidden-view disposal, fullscreen, target filters, IP label click/edit가 계속 동작한다.
9. Web release archive의 exact production asset allowlist와 hashes가 새 모듈을 포함하도록 동기화된다.
10. Chrome이 없는 환경에서는 실제 브라우저/화면 판독기 검증을 완료했다고 주장하지 않는다.

---

### Task 1: RED — 사라진 그래프와 mode 계약 재현

**Files:**
- Modify: `frontend/topology-renderer.test.js`
- Modify: `frontend/dom.test.js`
- Modify: `frontend/app.test.js`

**Steps:**
1. topology plan이 시각 Canvas item을 포함하지 않는 현재 동작을 실패 테스트로 고정한다.
2. topology 결과가 ready여도 `canvas[data-topology-mode]`가 없는 것을 재현한다.
3. 2D/3D radio 또는 segmented controls, reset control, live mode status가 없음을 RED로 확인한다.
4. mode 변경 시 fetch count가 증가하지 않아야 한다는 테스트를 추가한다.
5. Canvas가 없는 jsdom에서도 draw adapter injection으로 exact projected nodes/links를 검증할 seam을 정의한다.

Run: `npm --prefix frontend test -- --test-name-pattern='2D|3D|topology canvas|view mode'`
Expected: FAIL because the visual graph layer and controls do not exist.

### Task 2: bounded deterministic layout/projection core

**Files:**
- Create: `frontend/topology-visualizer.js`
- Create: `frontend/topology-visualizer.test.js`
- Modify: `frontend/package.json`

**Steps:**
1. Test-first로 plain-data input validation과 deterministic layout을 추가한다.
2. 2D layout은 hop depth를 X축, stable branch order를 Y축에 배치한다.
3. 3D layout은 같은 world coordinates에 deterministic depth spread와 perspective projection을 적용한다.
4. disconnected, repeated route, shared nodes, 0/1/max nodes, self-link rejection, unknown node를 테스트한다.
5. viewport 크기와 DPR을 bounded finite 값으로 clamp한다.
6. pan/zoom/rotation state를 finite bounded 값으로 유지한다.

Run: `node --test --test-concurrency=1 frontend/topology-visualizer.test.js`
Expected: PASS.

### Task 3: Canvas renderer와 lifecycle 연결

**Files:**
- Modify: `frontend/topology-model.js`
- Modify: `frontend/topology-renderer.js`
- Modify: `frontend/topology-model.test.js`
- Modify: `frontend/topology-renderer.test.js`

**Steps:**
1. topology plan에 fixed-cost `topology-canvas` item을 추가한다.
2. coordinator가 Canvas와 semantic items를 detached construction 후 atomic commit한다.
3. render session이 current model, mode, transform을 소유하도록 한다.
4. draw는 validated nodes/links/routes만 사용하고 node/link label은 local fixed formatting만 쓴다.
5. cancel/dispose/HMR/navigation 시 pointer, wheel, resize listeners와 scheduled draw를 exact-once 정리한다.
6. stale session이 successor Canvas를 redraw하거나 status를 지우지 못하는 테스트를 추가한다.
7. Canvas/context failure는 semantic inspector를 유지하면서 fixed local limitation을 표시한다.

Run: `npm --prefix frontend run test:renderer`
Expected: PASS.

### Task 4: 2D/3D 선택 UI와 접근성

**Files:**
- Modify: `frontend/index.html`
- Modify: `frontend/styles.css`
- Modify: `frontend/app.js`
- Modify: `frontend/dom.test.js`

**Steps:**
1. 토폴로지 결과 header에 native fieldset/radio 기반 `2D 그래프`/`3D 그래프` 선택을 추가한다.
2. `Reset view` 버튼과 concise interaction help를 추가한다.
3. 기본값은 2D이며 선택은 현재 탭의 UI state에만 보관한다.
4. mode change는 current model을 다시 projection/draw하고 fetch를 호출하지 않는다.
5. Canvas pointer drag/wheel과 keyboard arrows/+/-/Home을 연결한다.
6. node inspector click/keyboard가 기존 label editor를 계속 채우도록 한다.
7. 320/375/400px에서 controls wrap, Canvas min-height, horizontal overflow 없음, fullscreen sizing을 source/runtime test한다.
8. high contrast, focus-visible, reduced-motion, Canvas fallback status를 검증한다.

Run: `npm --prefix frontend run test:dom`
Expected: PASS.

### Task 5: release asset closure 및 문서

**Files:**
- Modify: `frontend/Dockerfile`
- Modify: `frontend/docker.test.js`
- Modify: `scripts/verify_web_archive.py` or its expected asset list if required
- Modify: `scripts/verify-release.sh` if its source projection has an explicit allowlist
- Modify: `README.md`
- Modify: `SPEC.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/TESTING.md`

**Steps:**
1. `topology-visualizer.js`를 exact Web production asset set에 추가한다.
2. served asset manifest와 archive verifier가 새 파일을 요구하고 unexpected files는 계속 거부하게 한다.
3. 2D/3D는 같은 producer facts의 view projection이며 진단 결과를 변경하지 않는다고 문서화한다.
4. 실제 브라우저/화면 판독기 검증 gap은 명시적으로 유지한다.

### Task 6: canonical verification and review

**Steps:**
1. `npm --prefix frontend test`
2. `npm --prefix frontend run test:syntax`
3. `git diff --check`
4. `make test`
5. `make build`
6. `make ci`
7. real Web archive/release verification from an immutable candidate.
8. independent Web/UI and release review; blocker/major 0/0만 commit 허용.
9. one Stage 9 commit, exact-SHA verification, then main fast-forward without push.

## Risks and trade-offs

- 진짜 WebGL 3D 엔진을 추가하면 번들/공급망/메모리 비용이 커진다. 이번 단계는 Canvas 2D API 위에서 deterministic perspective projection을 사용해 3D depth/rotation을 제공한다.
- Canvas만 사용하면 접근성이 낮아지므로 기존 bounded semantic node/link/route DOM을 유지한다.
- 500 nodes/1,000 links를 매 frame 계속 animation하지 않는다. interaction event와 resize에서만 redraw하여 CPU 사용을 제한한다.
- 브라우저가 Canvas 2D context를 제공하지 않아도 raw report와 semantic inspector는 유지되어야 한다.
