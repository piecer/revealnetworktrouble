'use strict';

import { planTopologyDOM, commitDOMChunk } from './topology-model.js';

const ROOT_OWNERS = new WeakMap();
const STATUS_OWNERS = new WeakMap();

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
  const address = elementText(value.address, elementText(value.id, '알 수 없는 노드'));
  const label = elementText(value.display_label);
  const parts = [label && label !== address ? label : '', address, elementText(value.display_note), elementText(value.status, 'unknown'), hopDetail(value)];
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

function truncationSummary(plan) {
  const parts = ['표시 완료'];
  if (plan.serverTruncation?.truncated) parts.push('서버 제한');
  if (plan.adapterTruncation?.truncated) parts.push('변환 제한');
  if (plan.viewTruncation?.truncated || plan.labelTruncation?.truncated) parts.push('보기 제한');
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

  start({ ownerId, inputSignature, view, model, root, status, workspace, focusTarget, labelRecords } = {}) {
    this.cancel('replaced');
    const generation = ++this.generation;
    if (!root || root.ownerDocument !== this.document || !status || status.ownerDocument !== this.document) {
      throw new TypeError('live root and status elements are required');
    }
    const session = {
      generation, ownerId, inputSignature, view, model, root, status, workspace,
      focusTarget, abortController: new AbortController(), scheduled: null,
      cleanups: [], committed: 0, plan: null, finished: false, token: {}
    };
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

    if (view === 'topology' && session.plan.plannedElements === 0) {
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
      if (session.view === 'geo' && chunk.some(item => item.kind === 'geo-canvas')) {
        this.#scheduleGeoDraw(session, index + 1);
      } else if (index + 1 < session.plan.chunks.length) {
        this.#scheduleChunk(session, index + 1);
      } else {
        this.#finish(session);
      }
    });
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
    this.#installNativeReorder(session);
    if (ROOT_OWNERS.get(session.root) === session.token) session.root.setAttribute('aria-busy', 'false');
    if (STATUS_OWNERS.get(session.status) === session.token) session.status.textContent = truncationSummary(session.plan);
    this.onState({ phase: 'ready', generation: session.generation, ownerId: session.ownerId, inputSignature: session.inputSignature, view: session.view, completed: session.committed, total: session.plan.plannedElements, plan: session.plan });
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

export { TopologyRenderCoordinator };
