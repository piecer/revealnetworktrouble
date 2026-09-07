'use strict';
import { asnContextLabel, ASN_CONTEXT_DISCLAIMER } from './topology-presentation.js';

import { planTopologyDOM, commitDOMChunk } from './topology-model.js';
import { createViewTransform, normalizeViewport, projectTopology } from './topology-visualizer.js';

const ROOT_OWNERS = new WeakMap();
const STATUS_OWNERS = new WeakMap();

const CANVAS_BACKGROUND = '#07111f';
const CANVAS_GRID = '#17243a';
const CANVAS_LABEL = '#e5edf8';
const CANVAS_DEFAULT_WIDTH = 800;
const CANVAS_DEFAULT_HEIGHT = 480;
const CANVAS_LABEL_LIMIT = 40;
const CANVAS_LABEL_MAX_WIDTH = 160;
const CANVAS_LABEL_HEIGHT = 14;
const CANVAS_LABEL_INSET = 4;
const CANVAS_LABEL_GAP = 5;

function canvasLabel(value) {
  const text = String(value ?? '').replace(/[\u0000-\u001f\u007f]/g, ' ').trim();
  return text.length <= CANVAS_LABEL_LIMIT ? text : `${text.slice(0, CANVAS_LABEL_LIMIT)}…`;
}

function canvasLabelPlacement(screen, viewport) {
  const insetX = Math.min(CANVAS_LABEL_INSET, viewport.width / 4);
  const maxWidth = Math.min(CANVAS_LABEL_MAX_WIDTH, viewport.width - insetX * 2);
  const minimumX = insetX + maxWidth / 2;
  const maximumX = viewport.width - insetX - maxWidth / 2;
  const x = Math.min(maximumX, Math.max(minimumX, screen.x));
  const insetY = Math.min(CANVAS_LABEL_INSET, viewport.height / 4);
  const below = screen.y + screen.radius + CANVAS_LABEL_GAP;
  if (below + CANVAS_LABEL_HEIGHT <= viewport.height - insetY) {
    return { x, y: Math.max(insetY, below), maxWidth, baseline: 'top' };
  }
  const above = screen.y - screen.radius - CANVAS_LABEL_GAP;
  return {
    x,
    y: Math.min(viewport.height - insetY, Math.max(insetY + CANVAS_LABEL_HEIGHT, above)),
    maxWidth,
    baseline: 'bottom'
  };
}

function requireCanvasContext(context) {
  const methods = ['setTransform', 'clearRect', 'fillRect', 'beginPath', 'moveTo', 'lineTo', 'stroke', 'closePath', 'fill', 'arc', 'fillText'];
  if (!context || methods.some(name => typeof context[name] !== 'function')) throw new TypeError('Canvas 2D context is unavailable');
}

function drawArrowhead(context, from, to, radius) {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const length = Math.hypot(dx, dy);
  if (!Number.isFinite(length) || length < 0.001) return;
  const unitX = dx / length;
  const unitY = dy / length;
  const tipX = to.x - unitX * radius;
  const tipY = to.y - unitY * radius;
  const size = 7;
  const baseX = tipX - unitX * size;
  const baseY = tipY - unitY * size;
  context.beginPath();
  context.moveTo(tipX, tipY);
  context.lineTo(baseX - unitY * size * 0.55, baseY + unitX * size * 0.55);
  context.lineTo(baseX + unitY * size * 0.55, baseY - unitX * size * 0.55);
  context.closePath();
  context.fill();
}

function drawTopologyCanvas(context, topology, options = {}) {
  requireCanvasContext(context);
  const viewport = normalizeViewport(options.viewport ?? {});
  const mode = options.mode ?? '2d';
  const transform = createViewTransform(options.transform ?? {});
  const projection = projectTopology(topology, { mode, transform, viewport, ...(options.presentation ? { presentation: options.presentation } : {}) });
  // Session-local presentation offsets never enter the report or identity model.
  for (const node of projection.nodes) {
    const offset = options.nodeOffsets?.get(node.id);
    if (!offset || !Number.isFinite(offset.x) || !Number.isFinite(offset.y)) continue;
    node.screen.x = Math.max(0, Math.min(viewport.width, node.screen.x + offset.x));
    node.screen.y = Math.max(0, Math.min(viewport.height, node.screen.y + offset.y));
  }
  const moved = new Map(projection.nodes.map(node => [node.id, node]));
  for (const link of [...projection.links, ...projection.connectors]) {
    const from = moved.get(link.from).screen, to = moved.get(link.to).screen;
    link.from_screen = { x: from.x, y: from.y };
    link.to_screen = { x: to.x, y: to.y };
  }

  context.setTransform(viewport.dpr, 0, 0, viewport.dpr, 0, 0);
  context.clearRect(0, 0, viewport.width, viewport.height);
  context.fillStyle = CANVAS_BACKGROUND;
  context.fillRect(0, 0, viewport.width, viewport.height);
  context.strokeStyle = CANVAS_GRID;
  context.lineWidth = 1;
  context.beginPath();
  const grid = 40;
  for (let x = grid; x < viewport.width; x += grid) { context.moveTo(x, 0); context.lineTo(x, viewport.height); }
  for (let y = grid; y < viewport.height; y += grid) { context.moveTo(0, y); context.lineTo(viewport.width, y); }
  context.stroke();

  const nodes = new Map(projection.nodes.map(node => [node.id, node]));
  for (const link of [...projection.links, ...projection.connectors]) {
    if (!link.visible) continue;
    const target = nodes.get(link.to);
    context.strokeStyle = link.color;
    context.fillStyle = link.color;
    context.lineWidth = 2;
    context.setLineDash?.(link.kind ? [7, 5] : []);
    context.beginPath();
    context.moveTo(link.from_screen.x, link.from_screen.y);
    const dx = link.to_screen.x - link.from_screen.x;
    const dy = link.to_screen.y - link.from_screen.y;
    const length = Math.max(1, Math.hypot(dx, dy));
    const bend = Math.min(42, length * 0.16);
    const control = { x: (link.from_screen.x + link.to_screen.x) / 2 - dy / length * bend,
      y: (link.from_screen.y + link.to_screen.y) / 2 + dx / length * bend };
    if (link.kind && link.from === link.to) {
      control.x += link.from_screen.x < viewport.width / 2 ? 48 : -48;
      control.y += link.from_screen.y < viewport.height / 2 ? 48 : -48;
    }
    if (typeof context.quadraticCurveTo === 'function') context.quadraticCurveTo(control.x, control.y, link.to_screen.x, link.to_screen.y);
    else context.lineTo(link.to_screen.x, link.to_screen.y);
    context.stroke();
    if (link.directed) drawArrowhead(context, control, link.to_screen, target?.screen.radius ?? 8);
  }
  context.setLineDash?.([]);

  const preservesState = typeof context.save === 'function' && typeof context.restore === 'function';
  if (preservesState) context.save();
  try {
    context.font = '12px system-ui, sans-serif';
    for (const node of projection.nodes) {
      if (!node.visible) continue;
      context.fillStyle = `${node.color}28`;
      context.beginPath();
      context.arc(node.screen.x, node.screen.y, node.screen.radius + 6, 0, Math.PI * 2);
      context.fill();
      context.fillStyle = node.color;
      context.beginPath();
      context.arc(node.screen.x, node.screen.y, node.screen.radius, 0, Math.PI * 2);
      context.fill();
      if (node.asn_context) {
        context.strokeStyle = '#c4b5fd';
        context.lineWidth = 1.5;
        context.setLineDash?.([3, 3]);
        context.beginPath();
        context.arc(node.screen.x, node.screen.y, node.screen.radius + 6, 0, Math.PI * 2);
        context.stroke();
        context.setLineDash?.([]);
      }
    }
    // Fixed candidate count and conservative maxWidth boxes: no font-metric or
    // frame-dependent relaxation, no moved graph nodes, and no synthetic links.
    // Prefer saved aliases, then stable projection order. Crowded labels remain
    // available in hover/focus details and the complete bounded inspector.
    const aliases = new Set(topology.nodes.filter(node => typeof node.display_label === 'string' && node.display_label.trim()).map(node => node.id));
    const labelNodes = projection.nodes.filter(node => node.visible).sort((a, b) => Number(aliases.has(b.id)) - Number(aliases.has(a.id)));
    const occupied = [];
    context.fillStyle = CANVAS_LABEL;
    context.textAlign = 'center';
    for (const node of labelNodes) {
      const label = canvasLabelPlacement(node.screen, viewport);
      const top = label.baseline === 'bottom' ? label.y - CANVAS_LABEL_HEIGHT : label.y;
      for (const shift of [0, -18, 18, -36, 36, -54, 54, -72, 72]) {
        const y = top + shift;
        const box = { left: label.x - label.maxWidth / 2, right: label.x + label.maxWidth / 2,
          top: y, bottom: y + CANVAS_LABEL_HEIGHT };
        if (box.top < CANVAS_LABEL_INSET || box.bottom > viewport.height - CANVAS_LABEL_INSET) continue;
        if (occupied.some(other => box.left < other.right + 2 && box.right + 2 > other.left && box.top < other.bottom + 2 && box.bottom + 2 > other.top)) continue;
        occupied.push(box);
        context.textBaseline = label.baseline;
        context.fillText(canvasLabel(node.label), label.x, label.baseline === 'bottom' ? box.bottom : box.top, label.maxWidth);
        break;
      }
    }
  } finally {
    if (preservesState) context.restore();
  }
  return projection;
}

function elementText(value, fallback = '') {
  if (value === undefined || value === null) return fallback;
  return String(value);
}

function setData(element, name, value) {
  if (value !== undefined && value !== null) element.setAttribute(`data-${name}`, String(value));
}

function hopDetail(value) {
  const minimum = Number(value.hop_min); const maximum = Number(value.hop_max);
  if (!Number.isFinite(minimum) || !Number.isFinite(maximum)) return '';
  return `HOP ${minimum === maximum ? minimum : `${minimum}–${maximum}`}`;
}

function nodeDetail(value) {
  if (value.kind === 'unknown-group') return `${value.display_label} · ${hopDetail(value)} · 구간 표시 (IP 아님)`;
  const address = elementText(value.address, elementText(value.id, '알 수 없는 노드'));
  const label = elementText(value.display_label);
  const contextLabel = asnContextLabel(value);
  // Put the disclaimer before long user-authored notes: bounded hover text must
  // never truncate away the distinction between observation and inference.
  const contextParts = contextLabel ? [contextLabel, ASN_CONTEXT_DISCLAIMER,
    ...(value.asn_contexts ?? []).map(c => `경로 ${c.result_index + 1} · 시도 ${c.attempt} · 구간 ${c.start}–${c.end} (출현 ${c.route_occurrence + 1}): AS${c.asn} 사이`),
    value.asn_contexts_omitted ? `추가 경로 문맥 ${value.asn_contexts_omitted}개 생략` : ''] : [];
  const parts = [...contextParts, label && label !== address ? label : '', address, elementText(value.display_note), elementText(value.status, 'unknown'), hopDetail(value)];
  if (Number.isFinite(value.observations)) parts.push(`${value.observations} observations`);
  if (Number.isFinite(value.latency_ms_avg)) parts.push(`${value.latency_ms_avg} ms`);
  if (value.public_ip === true) parts.push('public IP');
  if (value.public_ip === true && value.geolocation) {
    const location = [value.geolocation.city, value.geolocation.region, value.geolocation.country, value.geolocation.country_code]
      .filter(Boolean).filter((part, index, all) => all.indexOf(part) === index).join(', ');
    if (location) parts.push(location);
  }
  if (value.public_ip === true && value.asn) {
    const asn = [Number.isFinite(value.asn.number) ? `AS${value.asn.number}` : '', value.asn.organization].filter(Boolean).join(' ');
    if (asn) parts.push(asn);
  }
  return parts.filter(Boolean).join(' · ');
}

function routeColor(resultIndex) {
  const colors = ['#67d5ff', '#c9ff46', '#ffb84d', '#d59bff', '#ff7185', '#68e0b7', '#f3df63', '#83a7ff'];
  const index = Number.isInteger(Number(resultIndex)) ? Math.abs(Number(resultIndex)) % colors.length : 0;
  return colors[index];
}

function materializeTopologyItem(item, detachedDocument) {
  const value = item.value || {};
  switch (item.kind) {
    case 'topology-canvas': {
      const canvas = detachedDocument.createElement('canvas');
      canvas.className = 'topology-canvas';
      canvas.setAttribute('data-mode', '2d');
      canvas.setAttribute('data-dpr', '1');
      canvas.tabIndex = 0;
      canvas.setAttribute('role', 'img');
      canvas.setAttribute('aria-label', '경로 토폴로지 그래프');
      canvas.setAttribute('aria-describedby', 'topology-view-help topology-render-status');
      canvas.width = CANVAS_DEFAULT_WIDTH;
      canvas.height = CANVAS_DEFAULT_HEIGHT;
      return canvas;
    }
    case 'topology-inspector': {
      const details = detachedDocument.createElement('details');
      details.className = 'topology-inspector';
      const summary = detachedDocument.createElement('summary');
      summary.textContent = '노드 · 링크 · 경로 상세 보기';
      details.append(summary);
      return details;
    }
    case 'topology-node': {
      const editable = typeof value.address === 'string' && value.address.length > 0 && value.kind !== 'local' && value.editable !== false;
      const element = detachedDocument.createElement(editable ? 'button' : 'div');
      if (editable) {
        element.type = 'button';
        element.tabIndex = -1;
        setData(element, 'label-address', value.address);
      }
      element.className = 'topology-node';
      setData(element, 'node-id', value.id);
      setData(element, 'status', value.status);
      const detail = nodeDetail(value);
      setData(element, 'detail', detail);
      element.setAttribute('aria-label', detail);
      element.title = detail;
      element.textContent = detail;
      return element;
    }
    case 'topology-connector': {
      const element = detachedDocument.createElement('div');
      element.className = 'topology-connector';
      setData(element, 'from', value.from); setData(element, 'to', value.to);
      setData(element, 'kind', value.kind);
      element.textContent = value.label;
      element.title = value.label;
      element.setAttribute('aria-label', value.label);
      return element;
    }
    case 'topology-link': {
      const element = detachedDocument.createElement('div');
      element.className = 'topology-link';
      setData(element, 'from', value.from);
      setData(element, 'to', value.to);
      setData(element, 'status', value.status);
      const observations = Number.isFinite(value.observations) ? value.observations : 0;
      const detail = `${elementText(value.from)} → ${elementText(value.to)} · ${elementText(value.status, 'unknown')} · ${observations} observations`;
      setData(element, 'detail', detail);
      element.setAttribute('aria-label', detail);
      element.title = detail;
      element.style.setProperty('--observation-width', `${Math.max(1, Math.min(8, Math.ceil(Math.log2(observations + 1))))}px`);
      element.textContent = detail;
      return element;
    }
    case 'topology-route': {
      const element = detachedDocument.createElement('div');
      element.className = 'topology-route';
      setData(element, 'result-index', value.result_index);
      setData(element, 'attempt', value.attempt);
      setData(element, 'status', value.status);
      setData(element, 'reached', Boolean(value.reached));
      setData(element, 'complete', Boolean(value.complete));
      setData(element, 'node-ids', Array.isArray(value.node_ids) ? value.node_ids.join(' ') : '');
      const detail = `${elementText(value.address, `result ${Number(value.result_index ?? 0) + 1}`)} · ${elementText(value.status, 'unknown')} · ${value.reached ? 'reached' : 'unreached'} · ${value.complete ? 'complete' : 'incomplete'}`;
      setData(element, 'detail', detail);
      element.setAttribute('aria-label', detail);
      element.title = detail;
      element.style.setProperty('--route-color', routeColor(value.result_index));
      element.textContent = `경로 ${Number(value.result_index ?? 0) + 1} · 시도 ${elementText(value.attempt, '1')} · ${detail}`;
      return element;
    }
    case 'geo-canvas': {
      const canvas = detachedDocument.createElement('canvas');
      canvas.className = 'topology-geo-canvas';
      canvas.setAttribute('role', 'img');
      canvas.setAttribute('aria-label', '네트워크 경로 지도');
      canvas.width = 960;
      canvas.height = 540;
      return canvas;
    }
    case 'geo-accessible-list': {
      const list = detachedDocument.createElement('ul');
      list.className = 'topology-geo-list';
      list.setAttribute('aria-label', '지도 위치 목록');
      return list;
    }
    case 'geo-accessible-item': {
      const itemElement = detachedDocument.createElement('li');
      setData(itemElement, 'node-id', value.node_id);
      itemElement.textContent = `노드 ${elementText(value.node_id)}: 위도 ${elementText(value.latitude)}, 경도 ${elementText(value.longitude)}`;
      return itemElement;
    }
    case 'label-shell': {
      const table = detachedDocument.createElement('table');
      table.className = 'topology-labels';
      const caption = detachedDocument.createElement('caption');
      caption.textContent = 'IP 라벨';
      const head = detachedDocument.createElement('thead');
      const body = detachedDocument.createElement('tbody');
      table.append(caption, head, body);
      return table;
    }
    case 'label-row': {
      const row = detachedDocument.createElement('tr');
      const address = detachedDocument.createElement('td');
      const label = detachedDocument.createElement('td');
      const note = detachedDocument.createElement('td');
      const remove = detachedDocument.createElement('button');
      address.textContent = elementText(value.node_id);
      label.textContent = elementText(value.label);
      note.textContent = elementText(value.note);
      remove.type = 'button';
      remove.textContent = '삭제';
      setData(remove, 'node-id', value.node_id);
      row.append(address, label, note, remove);
      return row;
    }
    default:
      throw new TypeError(`unsupported topology DOM item: ${item.kind}`);
  }
}

function countElements(document) {
  return document.getElementsByTagName('*').length;
}

function hiddenViewDisposer(workspace) {
  if (typeof workspace === 'function') return workspace;
  for (const name of ['disposeHiddenViews', 'disposeHiddenView', 'disposeHidden']) {
    if (typeof workspace?.[name] === 'function') return workspace[name].bind(workspace);
  }
  return null;
}

function addCleanups(session, result) {
  if (typeof result === 'function') session.cleanups.push(result);
  else if (Array.isArray(result)) for (const cleanup of result) if (typeof cleanup === 'function') session.cleanups.push(cleanup);
  else if (result && typeof result.dispose === 'function') session.cleanups.push(() => result.dispose());
}

function truncationSummary(plan, localLimitation = null) {
  const parts = ['표시 완료'];
  const contexts = plan.presentation?.stats.asn_contexts;
  if (plan.view === 'topology' && contexts?.total) parts.push(`ASN 문맥 ${contexts.displayed}개 표시 · ${contexts.omitted}개 생략 (추정)`);
  if (plan.serverTruncation?.truncated) parts.push('서버 제한');
  if (plan.adapterTruncation?.truncated) parts.push('변환 제한');
  if (plan.viewTruncation?.truncated || plan.labelTruncation?.truncated) parts.push('보기 제한');
  if (localLimitation === 'canvas_context_unavailable') parts.push('그래프를 표시할 수 없습니다. 노드 · 링크 · 경로 상세 보기를 펼쳐 확인하세요.');
  return parts.join(' · ');
}

class TopologyRenderCoordinator {
  constructor({ document, schedule, cancelScheduled, ownsRequest, onState = () => {} }) {
    if (!document || typeof document.createElement !== 'function') throw new TypeError('document is required');
    if (typeof schedule !== 'function' || typeof cancelScheduled !== 'function') throw new TypeError('scheduler functions are required');
    if (typeof ownsRequest !== 'function') throw new TypeError('ownsRequest is required');
    if (typeof onState !== 'function') throw new TypeError('onState must be a function');
    this.document = document;
    this.schedule = schedule;
    this.cancelScheduled = cancelScheduled;
    this.ownsRequest = ownsRequest;
    this.onState = onState;
    this.generation = 0;
    this.current = null;
  }

  start({ ownerId, inputSignature, view, model, root, status, workspace, focusTarget, labelRecords, mode = '2d', transform = {}, viewport } = {}) {
    this.cancel('replaced');
    const generation = ++this.generation;
    if (!root || root.ownerDocument !== this.document || !status || status.ownerDocument !== this.document) {
      throw new TypeError('live root and status elements are required');
    }
    const session = {
      generation, ownerId, inputSignature, view, model, root, status, workspace,
      mode, transform: null, viewport: viewport === undefined ? null : normalizeViewport(viewport), localLimitation: null,
      focusTarget, abortController: new AbortController(), scheduled: null,
      cleanups: [], committed: 0, plan: null, finished: false, token: {}
    };
    session.nodeOffsets = new Map();
    this.current = session;
    ROOT_OWNERS.set(root, session.token);
    STATUS_OWNERS.set(status, session.token);
    root.replaceChildren();
    root.removeAttribute('aria-live');
    root.setAttribute('aria-busy', 'true');
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');

    try {
      hiddenViewDisposer(workspace)?.();
      if (mode !== '2d' && mode !== '3d') throw new TypeError('mode must be 2d or 3d');
      session.transform = createViewTransform(transform);
      const existingDOMElements = countElements(this.document);
      session.plan = planTopologyDOM(model, { view, existingDOMElements, maxDOMPerChunk: 100, labelRecords });
    } catch (error) {
      this.#fail(session, error);
      return generation;
    }

    if (!this.#active(session)) return generation;
    if (!session.plan.renderable) {
      root.replaceChildren();
      root.setAttribute('aria-busy', 'false');
      status.textContent = '표시할 수 없음: 문서 요소 한도';
      session.finished = true;
      this.onState({ phase: 'ready', generation, ownerId, inputSignature, view, renderable: false });
      return generation;
    }

    const topologySemanticTotal = session.plan.semanticCounts.topology.nodes +
      session.plan.semanticCounts.topology.links + session.plan.semanticCounts.topology.routes;
    if (view === 'topology' && topologySemanticTotal === 0) {
      const empty = this.document.createElement('p');
      empty.className = 'topology-empty-state';
      empty.textContent = '선택된 TRACE 경로 0개 · 표시할 토폴로지가 없습니다.';
      root.replaceChildren(empty);
      root.setAttribute('aria-busy', 'false');
      status.textContent = '선택된 TRACE 경로 0개';
      session.finished = true;
      this.onState({ phase: 'ready', generation, ownerId, inputSignature, view, completed: 0, total: 0, plan: session.plan, empty: true });
      return generation;
    }

    const total = session.plan.plannedElements;
    status.textContent = `표시 0/${total}`;
    this.onState({ phase: 'rendering', generation, ownerId, inputSignature, view, completed: 0, total });
    this.#installKeyboard(session);
    if (session.plan.chunks.length === 0) this.#finish(session);
    else this.#scheduleChunk(session, 0);
    return generation;
  }

  cancel(reason = 'user') {
    const session = this.current;
    if (!session) return false;
    const stillOwnsSurface = ROOT_OWNERS.get(session.root) === session.token && STATUS_OWNERS.get(session.status) === session.token;
    const mayAnnounce = stillOwnsSurface && !session.abortController.signal.aborted &&
      this.ownsRequest(session.ownerId, session.inputSignature, session.generation, session.abortController.signal) === true;
    this.current = null;
    session.abortController.abort(reason);
    if (session.scheduled !== null && session.scheduled !== undefined) {
      try { this.cancelScheduled(session.scheduled); } catch { /* cleanup must continue */ }
    }
    this.#runCleanups(session);
    if (ROOT_OWNERS.get(session.root) === session.token) {
      session.root.replaceChildren();
      session.root.removeAttribute('aria-busy');
      ROOT_OWNERS.delete(session.root);
    }
    if (STATUS_OWNERS.get(session.status) === session.token) {
      session.status.textContent = '';
      STATUS_OWNERS.delete(session.status);
    }
    if (mayAnnounce && (reason === 'user' || reason === 'replaced') && typeof session.focusTarget === 'function') session.focusTarget(reason);
    if (mayAnnounce) this.onState({ phase: 'cancelled', generation: session.generation, ownerId: session.ownerId, inputSignature: session.inputSignature, view: session.view, reason });
    return true;
  }

  dispose() {
    this.cancel('dispose');
  }

  updateTopologyView({ ownerId, inputSignature, generation, mode, transform, viewport } = {}) {
    const session = this.current;
    if (!session || session.view !== 'topology' || session.ownerId !== ownerId ||
        session.inputSignature !== inputSignature || session.generation !== generation || !this.#active(session)) return false;
    const nextMode = mode === undefined ? session.mode : mode;
    if (nextMode !== '2d' && nextMode !== '3d') throw new TypeError('mode must be 2d or 3d');
    const nextTransform = transform === undefined ? session.transform : createViewTransform(transform);
    const nextViewport = viewport === undefined ? session.viewport : normalizeViewport(viewport);
    if (!session.root.querySelector('canvas.topology-canvas')) return false;
    session.mode = nextMode;
    session.transform = nextTransform;
    session.viewport = nextViewport;
    this.#drawTopology(session);
    if (!this.#active(session)) return false;
    if (session.finished && STATUS_OWNERS.get(session.status) === session.token) {
      session.status.textContent = truncationSummary(session.plan, session.localLimitation);
    }
    return true;
  }

  #active(session) {
    return this.current === session && this.generation === session.generation &&
      !session.abortController.signal.aborted && ROOT_OWNERS.get(session.root) === session.token &&
      this.ownsRequest(session.ownerId, session.inputSignature, session.generation, session.abortController.signal) === true;
  }

  #scheduleSafely(session, callback) {
    try { session.scheduled = this.schedule(callback); }
    catch (error) { this.#fail(session, error); }
  }

  #scheduleChunk(session, index) {
    if (!this.#active(session)) return;
    this.#scheduleSafely(session, () => {
      session.scheduled = null;
      if (!this.#active(session)) return;
      const chunk = session.plan.chunks[index];
      try {
        if (!this.#active(session)) return;
        const inserted = commitDOMChunk(this.document, session.root, chunk, materializeTopologyItem);
        if (!this.#active(session)) return;
        this.#organizeCommittedDOM(session);
        session.committed += inserted;
        session.status.textContent = `표시 ${session.committed}/${session.plan.plannedElements}`;
      } catch (error) {
        this.#fail(session, error);
        return;
      }
      if (!this.#active(session)) return;
      if (session.view === 'topology' && chunk.some(item => item.kind === 'topology-canvas')) {
        this.#scheduleTopologyDraw(session, index + 1);
      } else if (session.view === 'geo' && chunk.some(item => item.kind === 'geo-canvas')) {
        this.#scheduleGeoDraw(session, index + 1);
      } else if (index + 1 < session.plan.chunks.length) {
        this.#scheduleChunk(session, index + 1);
      } else {
        this.#finish(session);
      }
    });
  }

  #scheduleTopologyDraw(session, nextChunk) {
    if (!this.#active(session)) return;
    this.#scheduleSafely(session, () => {
      session.scheduled = null;
      if (!this.#active(session)) return;
      this.#drawTopology(session);
      if (!this.#active(session)) return;
      if (nextChunk < session.plan.chunks.length) this.#scheduleChunk(session, nextChunk);
      else this.#finish(session);
    });
  }

  #drawTopology(session) {
    if (!this.#active(session)) return false;
    const canvas = session.root.querySelector('canvas.topology-canvas');
    if (!canvas) return false;
    try {
      const view = canvas.ownerDocument.defaultView;
      if (!view || typeof view.CanvasRenderingContext2D !== 'function') throw new TypeError('Canvas 2D context is unavailable');
      const context = canvas.getContext('2d');
      if (!context) throw new TypeError('Canvas 2D context is unavailable');
      const dpr = Number(canvas.dataset.dpr);
      const viewport = session.viewport ?? normalizeViewport({ width: canvas.width / dpr, height: canvas.height / dpr, dpr });
      if (canvas.width !== viewport.pixelWidth) canvas.width = viewport.pixelWidth;
      if (canvas.height !== viewport.pixelHeight) canvas.height = viewport.pixelHeight;
      session.projection = drawTopologyCanvas(context, session.plan.topology, {
        mode: session.mode,
        transform: session.transform,
        viewport, nodeOffsets: session.nodeOffsets, presentation: session.plan.presentation
      });
      if (!this.#active(session)) return false;
      canvas.dataset.mode = session.mode;
      canvas.dataset.dpr = String(viewport.dpr);
      canvas.setAttribute('aria-label', `${session.mode === '3d' ? '3D' : '2D'} 경로 토폴로지 그래프`);
      canvas.dataset.drawState = 'rendered';
      session.localLimitation = null;
      return true;
    } catch {
      if (!this.#active(session)) return false;
      canvas.dataset.drawState = 'unavailable';
      session.localLimitation = 'canvas_context_unavailable';
      return false;
    }
  }

  #scheduleGeoDraw(session, nextChunk) {
    if (!this.#active(session)) return;
    this.#scheduleSafely(session, () => {
      session.scheduled = null;
      if (!this.#active(session)) return;
      try {
        const canvas = session.root.querySelector('canvas.topology-geo-canvas');
        const draw = typeof session.workspace?.drawGeo === 'function' ? session.workspace.drawGeo.bind(session.workspace) : null;
        if (draw) addCleanups(session, draw({ canvas, geo: session.plan.geo, signal: session.abortController.signal }));
        else if (canvas) {
          setData(canvas, 'markers', session.plan.geo.markers.length);
          setData(canvas, 'segments', session.plan.geo.segments.length);
          setData(canvas, 'arrows', session.plan.geo.arrows.length);
        }
      } catch (error) {
        this.#fail(session, error);
        return;
      }
      if (!this.#active(session)) return;
      if (nextChunk < session.plan.chunks.length) this.#scheduleChunk(session, nextChunk);
      else this.#finish(session);
    });
  }

  #organizeCommittedDOM(session) {
    if (session.view === 'geo') {
      const list = session.root.querySelector('ul.topology-geo-list');
      if (list) for (const item of [...session.root.children]) if (item.tagName === 'LI') list.append(item);
    } else if (session.view === 'labels') {
      const body = session.root.querySelector('table.topology-labels tbody');
      if (body) for (const item of [...session.root.children]) if (item.tagName === 'TR') body.append(item);
    } else {
      const inspector = session.root.querySelector('details.topology-inspector');
      if (inspector) for (const item of [...session.root.children]) {
        if (item.matches('.topology-node, .topology-link, .topology-connector, .topology-route')) inspector.append(item);
      }
      const buttons = [...session.root.querySelectorAll('button.topology-node')];
      if (buttons.length && !buttons.some(button => button.tabIndex === 0)) buttons[0].tabIndex = 0;
    }
  }

  #installKeyboard(session) {
    if (session.view !== 'topology') return;
    const listener = event => {
      if (!this.#active(session) || !['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown'].includes(event.key)) return;
      const buttons = [...session.root.querySelectorAll('button.topology-node')];
      const currentIndex = buttons.indexOf(event.target);
      if (currentIndex < 0 || buttons.length < 2) return;
      const direction = event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1;
      if (event.altKey) {
        if (!session.finished) return;
        const adjacent = buttons[currentIndex + direction];
        if (!adjacent) return;
        if (direction < 0) adjacent.before(event.target); else adjacent.after(event.target);
        event.target.focus();
        event.preventDefault();
        return;
      }
      const next = buttons[(currentIndex + direction + buttons.length) % buttons.length];
      for (const button of buttons) button.tabIndex = button === next ? 0 : -1;
      next.focus();
      event.preventDefault();
    };
    session.root.addEventListener('keydown', listener);
    session.cleanups.push(() => session.root.removeEventListener('keydown', listener));
  }

  #finish(session) {
    if (!this.#active(session)) return;
    session.finished = true;
    this.#installGraphInteraction(session);
    this.#installNativeReorder(session);
    if (ROOT_OWNERS.get(session.root) === session.token) session.root.setAttribute('aria-busy', 'false');
    if (STATUS_OWNERS.get(session.status) === session.token) session.status.textContent = truncationSummary(session.plan, session.localLimitation);
    this.onState({ phase: 'ready', generation: session.generation, ownerId: session.ownerId, inputSignature: session.inputSignature, view: session.view, completed: session.committed, total: session.plan.plannedElements, plan: session.plan, localLimitation: session.localLimitation });
  }

  #installGraphInteraction(session) {
    if (session.view !== 'topology' || !session.projection) return;
    const canvas = session.root.querySelector('canvas.topology-canvas');
    const tooltip = session.workspace?.tooltip;
    const facts = new Map(session.plan.presentation.nodes.map(node => [node.id, node]));
    let drag = null;
    let selected = session.projection.nodes[0]?.id;
    let suppressClick = false;
    const point = event => {
      const rect = canvas.getBoundingClientRect(), viewport = session.projection.viewport;
      return { x: (event.clientX - rect.left) * viewport.width / (rect.width || viewport.width),
        y: (event.clientY - rect.top) * viewport.height / (rect.height || viewport.height) };
    };
    const hit = p => {
      let nearest, distance = Infinity;
      // Closest center wins; equal-distance overlaps use frontmost draw order.
      for (const node of session.projection.nodes) {
        const candidate = Math.hypot(node.screen.x - p.x, node.screen.y - p.y);
        if (node.visible && candidate <= node.screen.radius + 8 && candidate <= distance) {
          nearest = node; distance = candidate;
        }
      }
      return nearest;
    };
    const hide = () => { if (tooltip) { tooltip.hidePopover?.(); tooltip.hidden = true; } };
    const show = node => {
      if (!tooltip) return;
      if (!node) { hide(); return; }
      selected = node.id;
      tooltip.textContent = nodeDetail(facts.get(node.id)).slice(0, 2048);
      tooltip.hidden = false;
      tooltip.showPopover?.();
      const rect = canvas.getBoundingClientRect(), view = canvas.ownerDocument.defaultView;
      const viewport = session.projection.viewport;
      const x = rect.left + node.screen.x * (rect.width || viewport.width) / viewport.width;
      const y = rect.top + node.screen.y * (rect.height || viewport.height) / viewport.height;
      tooltip.style.left = `${Math.max(8, Math.min(view.innerWidth - Math.min(320, view.innerWidth - 16) - 8, x - 150))}px`;
      tooltip.style.top = `${Math.max(8, Math.min(view.innerHeight - 168, y - 170))}px`;
      canvas.setAttribute('aria-describedby', `${tooltip.id} topology-view-help topology-render-status`);
    };
    const on = (type, listener) => {
      const guarded = event => { if (this.#active(session)) listener(event); };
      canvas.addEventListener(type, guarded, true);
      session.cleanups.push(() => canvas.removeEventListener(type, guarded, true));
    };
    const edit = node => {
      if (!node) return;
      const button = [...session.root.querySelectorAll('button.topology-node')].find(item => item.dataset.nodeId === node.id);
      button?.click(); // One canonical storage/editor path for graph and inspector.
    };
    on('focus', () => show(session.projection.nodes.find(node => node.id === selected)));
    on('blur', hide);
    on('keydown', event => {
      if (event.key === 'Escape') { hide(); event.preventDefault(); return; }
      if (event.key === 'Enter') { edit(session.projection.nodes.find(node => node.id === selected)); event.preventDefault(); return; }
      if (!['[', ']'].includes(event.key)) return;
      const nodes = session.projection.nodes;
      const index = nodes.findIndex(node => node.id === selected);
      show(nodes[(index + (event.key === ']' ? 1 : -1) + nodes.length) % nodes.length]);
      event.preventDefault(); event.stopPropagation();
    });
    on('click', event => { if (suppressClick) { suppressClick = false; return; } edit(hit(point(event))); });
    on('dblclick', event => edit(hit(point(event))));
    on('pointerdown', event => {
      if (event.button !== 0) return;
      const p = point(event), node = hit(p);
      if (!node) return; // Empty background retains pan/rotate.
      suppressClick = false;
      drag = { id: node.id, pointerId: event.pointerId, point: p };
      try { canvas.setPointerCapture?.(event.pointerId); } catch { /* optional capture */ }
      show(node);
      event.preventDefault(); event.stopPropagation();
    });
    on('pointermove', event => {
      const p = point(event);
      if (drag && event.pointerId === drag.pointerId) {
        const offset = session.nodeOffsets.get(drag.id) || { x: 0, y: 0 };
        const x = offset.x + p.x - drag.point.x, y = offset.y + p.y - drag.point.y;
        if (Number.isFinite(x) && Number.isFinite(y)) {
          if (Math.hypot(p.x - drag.point.x, p.y - drag.point.y) > 2) suppressClick = true;
          session.nodeOffsets.set(drag.id, { x: Math.max(-8192, Math.min(8192, x)), y: Math.max(-8192, Math.min(8192, y)) });
          drag.point = p;
          this.#drawTopology(session);
        }
        event.preventDefault(); event.stopPropagation();
      } else show(hit(p));
    });
    for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) on(type, event => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      drag = null;
      try { canvas.releasePointerCapture?.(event.pointerId); } catch { /* optional capture */ }
      event.stopPropagation();
    });
    on('pointerleave', () => { if (!drag) hide(); });
    session.cleanups.push(() => {
      if (drag) { try { canvas.releasePointerCapture?.(drag.pointerId); } catch { /* detached canvas */ } }
      drag = null;
      // An old coordinator must not hide a successor's tooltip.
      if (ROOT_OWNERS.get(session.root) === session.token) hide();
    });
  }

  #installNativeReorder(session) {
    if (session.view !== 'topology') return;
    let dragged = null;
    for (const node of session.root.querySelectorAll('button.topology-node')) node.draggable = true;
    const dragstart = event => {
      const node = event.target.closest('button.topology-node[draggable="true"]');
      if (!this.#active(session) || !session.finished || !node) return;
      dragged = node;
      event.dataTransfer?.setData('text/plain', node.dataset.nodeId || '');
      if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
    };
    const dragover = event => {
      if (dragged && event.target.closest('button.topology-node')) event.preventDefault();
    };
    const drop = event => {
      const target = event.target.closest('button.topology-node');
      if (!this.#active(session) || !session.finished || !dragged || !target || dragged === target) return;
      event.preventDefault();
      target.before(dragged);
      dragged.focus();
      dragged = null;
    };
    const dragend = () => { dragged = null; };
    session.root.addEventListener('dragstart', dragstart);
    session.root.addEventListener('dragover', dragover);
    session.root.addEventListener('drop', drop);
    session.root.addEventListener('dragend', dragend);
    session.cleanups.push(() => {
      session.root.removeEventListener('dragstart', dragstart);
      session.root.removeEventListener('dragover', dragover);
      session.root.removeEventListener('drop', drop);
      session.root.removeEventListener('dragend', dragend);
    });
  }

  #fail(session, error) {
    if (!this.#active(session)) return;
    session.abortController.abort('error');
    if (session.scheduled !== null && session.scheduled !== undefined) {
      try { this.cancelScheduled(session.scheduled); } catch { /* cleanup must continue */ }
    }
    this.#runCleanups(session);
    if (ROOT_OWNERS.get(session.root) === session.token) {
      session.root.replaceChildren();
      session.root.setAttribute('aria-busy', 'false');
    }
    if (STATUS_OWNERS.get(session.status) === session.token) session.status.textContent = '표시 오류';
    session.finished = true;
    this.onState({ phase: 'error', generation: session.generation, ownerId: session.ownerId, inputSignature: session.inputSignature, view: session.view, error });
  }

  #runCleanups(session) {
    const cleanups = session.cleanups.splice(0).reverse();
    for (const cleanup of cleanups) {
      try { cleanup(); } catch { /* cleanup is best effort */ }
    }
  }
}

export { drawTopologyCanvas, TopologyRenderCoordinator };
