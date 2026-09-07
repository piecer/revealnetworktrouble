'use strict';

export const VISUAL_NODES = 500;
export const VISUAL_LINKS = 1000;
export const VISUAL_ROUTES = 1000;

export const VIEWPORT_LIMITS = Object.freeze({
  minWidth: 1,
  maxWidth: 8192,
  minHeight: 1,
  maxHeight: 8192,
  minDPR: 0.5,
  maxDPR: 4,
  defaultWidth: 800,
  defaultHeight: 480,
  defaultDPR: 1
});

export const TRANSFORM_LIMITS = Object.freeze({
  maxPan: 8192,
  minZoom: 0.25,
  maxZoom: 8,
  maxYaw: Math.PI,
  maxPitch: Math.PI / 2 - 0.01
});

export const STATUS_COLORS = Object.freeze({
  healthy: '#22c55e',
  degraded: '#f59e0b',
  failure: '#ef4444',
  unreachable: '#dc2626',
  unknown: '#94a3b8'
});

export const DEFAULT_TRANSFORM = Object.freeze({ panX: 0, panY: 0, zoom: 1, yaw: 0, pitch: 0 });

const MODE_2D = '2d';
const MODE_3D = '3d';
const NODE_RADIUS = 8;
const PADDING = 32;
const CAMERA_DISTANCE = 4;

function snapshotPlainData(value, path = 'input', active = new WeakSet()) {
  if (value === null || typeof value !== 'object') {
    if (typeof value === 'number' && !Number.isFinite(value)) throw new TypeError(`${path} must contain only finite numbers`);
    return value;
  }
  if (active.has(value)) throw new TypeError(`${path} must not contain cycles`);
  const isArray = Array.isArray(value);
  if (Object.getPrototypeOf(value) !== (isArray ? Array.prototype : Object.prototype)) {
    throw new TypeError(`${path} must use a plain prototype`);
  }
  const descriptors = Object.getOwnPropertyDescriptors(value);
  for (const key of Reflect.ownKeys(descriptors)) {
    if (isArray && key === 'length') continue;
    if (!Object.hasOwn(descriptors[key], 'value')) throw new TypeError(`${path} must not contain accessor properties`);
  }
  active.add(value);
  const copy = isArray ? [] : {};
  for (const key of Object.keys(descriptors)) {
    if (isArray && key === 'length') continue;
    if (descriptors[key].enumerable) copy[key] = snapshotPlainData(descriptors[key].value, `${path}.${String(key)}`, active);
  }
  active.delete(value);
  return copy;
}

function requireObject(value, path) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new TypeError(`${path} must be a plain object`);
}

function requireArray(value, path, maximum) {
  if (!Array.isArray(value)) throw new TypeError(`${path} must be an array`);
  if (value.length > maximum) throw new RangeError(`${path} must contain at most ${maximum} entries`);
}

function requireString(value, path, { nonempty = false } = {}) {
  if (typeof value !== 'string' || (nonempty && value.length === 0)) {
    throw new TypeError(`${path} must be ${nonempty ? 'a non-empty ' : ''}string`);
  }
}

function requireOptionalFinite(value, path, { integer = false, minimum = -Infinity } = {}) {
  if (value === undefined) return;
  if (typeof value !== 'number' || !Number.isFinite(value) || value < minimum || (integer && !Number.isInteger(value))) {
    throw new TypeError(`${path} must be a finite${integer ? ' integer' : ''} number${minimum !== -Infinity ? ` >= ${minimum}` : ''}`);
  }
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function compareRoutes(left, right) {
  return (left.result_index - right.result_index) ||
    (left.attempt - right.attempt) ||
    compareText(left.node_ids.join('\u0000'), right.node_ids.join('\u0000')) ||
    compareText(left.status, right.status);
}

function normalizedStatus(status) {
  return Object.hasOwn(STATUS_COLORS, status) ? status : 'unknown';
}

function nodeLabel(node) {
  const address = typeof node.address === 'string' ? node.address.trim() : '';
  if (address) return address;
  if (node.kind === 'unknown') return 'Unknown hop';
  if (node.kind === 'local') return 'Local';
  return node.id;
}

function validateTopology(input) {
  const value = snapshotPlainData(input, 'topology');
  requireObject(value, 'topology');
  requireArray(value.nodes, 'topology.nodes', VISUAL_NODES);
  requireArray(value.links, 'topology.links', VISUAL_LINKS);
  requireArray(value.routes, 'topology.routes', VISUAL_ROUTES);

  const nodeByID = new Map();
  for (let index = 0; index < value.nodes.length; index++) {
    const item = value.nodes[index];
    requireObject(item, `topology.nodes[${index}]`);
    requireString(item.id, `topology.nodes[${index}].id`, { nonempty: true });
    if (nodeByID.has(item.id)) throw new TypeError(`duplicate node id: ${item.id}`);
    if (item.kind !== undefined) requireString(item.kind, `topology.nodes[${index}].kind`);
    if (item.address !== undefined) requireString(item.address, `topology.nodes[${index}].address`);
    requireString(item.status, `topology.nodes[${index}].status`);
    requireOptionalFinite(item.hop_min, `topology.nodes[${index}].hop_min`, { minimum: 0 });
    requireOptionalFinite(item.hop_max, `topology.nodes[${index}].hop_max`, { minimum: 0 });
    requireOptionalFinite(item.observations, `topology.nodes[${index}].observations`, { integer: true, minimum: 0 });
    requireOptionalFinite(item.latency_ms_avg, `topology.nodes[${index}].latency_ms_avg`, { minimum: 0 });
    nodeByID.set(item.id, item);
  }

  const linkKeys = new Set();
  for (let index = 0; index < value.links.length; index++) {
    const item = value.links[index];
    requireObject(item, `topology.links[${index}]`);
    requireString(item.from, `topology.links[${index}].from`, { nonempty: true });
    requireString(item.to, `topology.links[${index}].to`, { nonempty: true });
    requireString(item.status, `topology.links[${index}].status`);
    requireOptionalFinite(item.observations, `topology.links[${index}].observations`, { integer: true, minimum: 0 });
    if (!nodeByID.has(item.from) || !nodeByID.has(item.to)) {
      throw new TypeError(`link ${item.from}->${item.to} references a missing node`);
    }
    if (item.from === item.to) throw new TypeError(`self link is not allowed: ${item.from}`);
    const key = `${item.from}\u0000${item.to}`;
    if (linkKeys.has(key)) throw new TypeError(`duplicate link: ${item.from}->${item.to}`);
    linkKeys.add(key);
  }

  for (let index = 0; index < value.routes.length; index++) {
    const item = value.routes[index];
    requireObject(item, `topology.routes[${index}]`);
    requireOptionalFinite(item.result_index, `topology.routes[${index}].result_index`, { integer: true, minimum: 0 });
    requireOptionalFinite(item.attempt, `topology.routes[${index}].attempt`, { integer: true, minimum: 0 });
    if (item.result_index === undefined || item.attempt === undefined) {
      throw new TypeError(`topology.routes[${index}] requires finite result_index and attempt values`);
    }
    requireString(item.status, `topology.routes[${index}].status`);
    requireArray(item.node_ids, `topology.routes[${index}].node_ids`, VISUAL_NODES);
    for (let nodeIndex = 0; nodeIndex < item.node_ids.length; nodeIndex++) {
      const id = item.node_ids[nodeIndex];
      requireString(id, `topology.routes[${index}].node_ids[${nodeIndex}]`, { nonempty: true });
      if (!nodeByID.has(id)) throw new TypeError(`route references missing node: ${id}`);
      if (nodeIndex > 0) {
        const from = item.node_ids[nodeIndex - 1];
        if (!linkKeys.has(`${from}\u0000${id}`)) throw new TypeError(`route requires directed link ${from}->${id}`);
      }
    }
  }

  value.nodes.sort((left, right) => compareText(left.id, right.id));
  value.links.sort((left, right) => compareText(left.from, right.from) || compareText(left.to, right.to));
  value.routes.sort(compareRoutes);
  return value;
}

function finiteNumber(value, fallback, path) {
  if (value === undefined) return fallback;
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new TypeError(`${path} must be a finite number`);
  return value;
}

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value));
}

export function normalizeViewport(input = {}) {
  const value = snapshotPlainData(input, 'viewport');
  requireObject(value, 'viewport');
  const width = Math.round(clamp(finiteNumber(value.width, VIEWPORT_LIMITS.defaultWidth, 'viewport.width'), VIEWPORT_LIMITS.minWidth, VIEWPORT_LIMITS.maxWidth));
  const height = Math.round(clamp(finiteNumber(value.height, VIEWPORT_LIMITS.defaultHeight, 'viewport.height'), VIEWPORT_LIMITS.minHeight, VIEWPORT_LIMITS.maxHeight));
  const dpr = clamp(finiteNumber(value.dpr, VIEWPORT_LIMITS.defaultDPR, 'viewport.dpr'), VIEWPORT_LIMITS.minDPR, VIEWPORT_LIMITS.maxDPR);
  return { width, height, dpr, pixelWidth: Math.round(width * dpr), pixelHeight: Math.round(height * dpr) };
}

export function createViewTransform(input = {}) {
  const value = snapshotPlainData(input, 'transform');
  requireObject(value, 'transform');
  return {
    panX: clamp(finiteNumber(value.panX, DEFAULT_TRANSFORM.panX, 'transform.panX'), -TRANSFORM_LIMITS.maxPan, TRANSFORM_LIMITS.maxPan),
    panY: clamp(finiteNumber(value.panY, DEFAULT_TRANSFORM.panY, 'transform.panY'), -TRANSFORM_LIMITS.maxPan, TRANSFORM_LIMITS.maxPan),
    zoom: clamp(finiteNumber(value.zoom, DEFAULT_TRANSFORM.zoom, 'transform.zoom'), TRANSFORM_LIMITS.minZoom, TRANSFORM_LIMITS.maxZoom),
    yaw: clamp(finiteNumber(value.yaw, DEFAULT_TRANSFORM.yaw, 'transform.yaw'), -TRANSFORM_LIMITS.maxYaw, TRANSFORM_LIMITS.maxYaw),
    pitch: clamp(finiteNumber(value.pitch, DEFAULT_TRANSFORM.pitch, 'transform.pitch'), -TRANSFORM_LIMITS.maxPitch, TRANSFORM_LIMITS.maxPitch)
  };
}

export function updateViewTransform(input, delta = {}) {
  const current = createViewTransform(input);
  const change = snapshotPlainData(delta, 'transform delta');
  requireObject(change, 'transform delta');
  return createViewTransform({
    panX: current.panX + finiteNumber(change.panX, 0, 'transform delta.panX'),
    panY: current.panY + finiteNumber(change.panY, 0, 'transform delta.panY'),
    zoom: current.zoom + finiteNumber(change.zoom, 0, 'transform delta.zoom'),
    yaw: current.yaw + finiteNumber(change.yaw, 0, 'transform delta.yaw'),
    pitch: current.pitch + finiteNumber(change.pitch, 0, 'transform delta.pitch')
  });
}

export function resetViewTransform() {
  return { ...DEFAULT_TRANSFORM };
}

function routeMemberships(value) {
  const memberships = new Map(value.nodes.map(item => [item.id, []]));
  const centeredRouteLane = new Map();
  const center = (value.routes.length - 1) / 2;
  value.routes.forEach((item, index) => {
    const key = `${item.result_index}:${item.attempt}:${index}`;
    centeredRouteLane.set(key, index - center);
    for (const id of new Set(item.node_ids)) memberships.get(id).push({ key, lane: index - center });
  });
  return { memberships, centeredRouteLane };
}

function inferDepths(value) {
  const depths = new Map();
  for (const item of value.nodes) {
    if (Number.isFinite(item.hop_min)) depths.set(item.id, item.hop_min);
  }
  for (const item of value.routes) {
    item.node_ids.forEach((id, index) => {
      const previous = depths.get(id);
      if (previous === undefined || index < previous) depths.set(id, index);
    });
  }

  const incoming = new Map(value.nodes.map(item => [item.id, 0]));
  const outgoing = new Map(value.nodes.map(item => [item.id, []]));
  for (const item of value.links) {
    incoming.set(item.to, incoming.get(item.to) + 1);
    outgoing.get(item.from).push(item.to);
  }
  for (const list of outgoing.values()) list.sort(compareText);
  const queue = value.nodes.filter(item => incoming.get(item.id) === 0).map(item => item.id).sort(compareText);
  for (const id of queue) if (!depths.has(id)) depths.set(id, 0);
  for (let cursor = 0; cursor < queue.length; cursor++) {
    const from = queue[cursor];
    for (const to of outgoing.get(from)) {
      if (!depths.has(to)) depths.set(to, depths.get(from) + 1);
      incoming.set(to, incoming.get(to) - 1);
      if (incoming.get(to) === 0) queue.push(to);
    }
  }
  for (const item of value.nodes) if (!depths.has(item.id)) depths.set(item.id, 0);
  return depths;
}

function average(items, select) {
  if (items.length === 0) return 0;
  return items.reduce((sum, item) => sum + select(item), 0) / items.length;
}

function layoutValidated(value) {
  const depths = inferDepths(value);
  const { memberships } = routeMemberships(value);
  const incoming = new Map(value.nodes.map(item => [item.id, []]));
  for (const item of value.links) incoming.get(item.to).push(item.from);
  for (const list of incoming.values()) list.sort(compareText);

  const groups = new Map();
  for (const item of value.nodes) {
    const depth = depths.get(item.id);
    if (!groups.has(depth)) groups.set(depth, []);
    groups.get(depth).push(item);
  }

  const laneByID = new Map();
  for (const depth of [...groups.keys()].sort((left, right) => left - right)) {
    const items = groups.get(depth);
    items.sort((left, right) => {
      const leftMemberships = memberships.get(left.id);
      const rightMemberships = memberships.get(right.id);
      const leftRouteLane = leftMemberships.length ? average(leftMemberships, item => item.lane) : Number.POSITIVE_INFINITY;
      const rightRouteLane = rightMemberships.length ? average(rightMemberships, item => item.lane) : Number.POSITIVE_INFINITY;
      const leftParentLane = average(incoming.get(left.id).filter(id => laneByID.has(id)), id => laneByID.get(id));
      const rightParentLane = average(incoming.get(right.id).filter(id => laneByID.has(id)), id => laneByID.get(id));
      return (leftRouteLane - rightRouteLane) || (leftParentLane - rightParentLane) || compareText(left.id, right.id);
    });
    const center = (items.length - 1) / 2;
    items.forEach((item, index) => laneByID.set(item.id, index - center));
  }

  const nodes = value.nodes.map(item => {
    const member = memberships.get(item.id);
    const routeDepth = member.length ? average(member, entry => entry.lane) : laneByID.get(item.id) * 0.75;
    return {
      id: item.id,
      kind: item.kind ?? 'unknown',
      status: normalizedStatus(item.status),
      label: nodeLabel(item),
      color: STATUS_COLORS[normalizedStatus(item.status)],
      route_keys: member.map(entry => entry.key),
      world: { x: depths.get(item.id), y: laneByID.get(item.id), z: routeDepth }
    };
  });
  const byID = new Map(nodes.map(item => [item.id, item]));
  const links = value.links.map(item => {
    const status = normalizedStatus(item.status);
    return {
      from: item.from,
      to: item.to,
      directed: true,
      status,
      color: STATUS_COLORS[status],
      label: `${byID.get(item.from).label} → ${byID.get(item.to).label}`
    };
  });
  return { nodes, links };
}

function extent(items, axis) {
  if (items.length === 0) return { minimum: 0, maximum: 0, span: 0, center: 0 };
  let minimum = Infinity;
  let maximum = -Infinity;
  for (const item of items) {
    minimum = Math.min(minimum, item.world[axis]);
    maximum = Math.max(maximum, item.world[axis]);
  }
  return { minimum, maximum, span: maximum - minimum, center: (minimum + maximum) / 2 };
}

function fitScale(viewport, xExtent, yExtent) {
  const availableWidth = Math.max(1, viewport.width - PADDING * 2);
  const availableHeight = Math.max(1, viewport.height - PADDING * 2);
  const xScale = xExtent.span > 0 ? availableWidth / xExtent.span : Infinity;
  const yScale = yExtent.span > 0 ? availableHeight / yExtent.span : Infinity;
  const candidate = Math.min(xScale, yScale);
  return Number.isFinite(candidate) ? candidate : Math.min(availableWidth, availableHeight) / 2;
}

function project2D(item, extents, viewport, transform, scale) {
  const rawX = viewport.width / 2 + (item.world.x - extents.x.center) * scale * transform.zoom + transform.panX;
  const rawY = viewport.height / 2 + (item.world.y - extents.y.center) * scale * transform.zoom + transform.panY;
  return {
    x: clamp(rawX, 0, viewport.width),
    y: clamp(rawY, 0, viewport.height),
    radius: clamp(NODE_RADIUS * (item.perspective ?? 1) * Math.sqrt(transform.zoom), 3, 18),
    visible: rawX >= 0 && rawX <= viewport.width && rawY >= 0 && rawY <= viewport.height,
    depth: item.depth ?? 0
  };
}

function project3D(item, extents, transform) {
  const baseScale = Math.max(extents.x.span, extents.y.span, extents.z.span, 1);
  const x = (item.world.x - extents.x.center) / baseScale * 2;
  const y = (item.world.y - extents.y.center) / baseScale * 2;
  const z = (item.world.z - extents.z.center) / baseScale * 2;
  const cosYaw = Math.cos(transform.yaw);
  const sinYaw = Math.sin(transform.yaw);
  const yawX = x * cosYaw - z * sinYaw;
  const yawZ = x * sinYaw + z * cosYaw;
  const cosPitch = Math.cos(transform.pitch);
  const sinPitch = Math.sin(transform.pitch);
  const pitchY = y * cosPitch - yawZ * sinPitch;
  const depth = y * sinPitch + yawZ * cosPitch;
  const perspective = CAMERA_DISTANCE / Math.max(0.5, CAMERA_DISTANCE + depth);
  return {
    world: { x: yawX * perspective, y: pitchY * perspective },
    perspective,
    depth
  };
}

export function projectTopology(input, inputOptions = {}) {
  const options = snapshotPlainData(inputOptions, 'options');
  requireObject(options, 'options');
  const mode = options.mode ?? MODE_2D;
  if (mode !== MODE_2D && mode !== MODE_3D) throw new TypeError('mode must be 2d or 3d');
  const viewport = normalizeViewport(options.viewport ?? {});
  const transform = createViewTransform(options.transform ?? {});
  const layout = layoutValidated(validateTopology(input));
  const extents = {
    x: extent(layout.nodes, 'x'),
    y: extent(layout.nodes, 'y'),
    z: extent(layout.nodes, 'z')
  };
  // Fit the actual camera-plane bounds, not an assumed world cube. Zoom is
  // relative to this fit; singleton bounds remain centered with normal radius.
  const cameraNodes = layout.nodes.map(item => mode === MODE_2D ? item : project3D(item, extents, transform));
  const cameraExtents = { x: extent(cameraNodes, 'x'), y: extent(cameraNodes, 'y') };
  const scale = fitScale(viewport, cameraExtents.x, cameraExtents.y);
  const projectedNodes = layout.nodes.map((item, index) => {
    const point = project2D(cameraNodes[index], cameraExtents, viewport, transform, scale);
    return {
      ...item,
      screen: { x: point.x, y: point.y, radius: point.radius },
      depth: point.depth,
      visible: point.visible
    };
  });
  if (mode === MODE_3D) projectedNodes.sort((left, right) => (right.depth - left.depth) || compareText(left.id, right.id));
  const nodeByID = new Map(projectedNodes.map(item => [item.id, item]));
  const projectedLinks = layout.links.map(item => {
    const from = nodeByID.get(item.from);
    const to = nodeByID.get(item.to);
    return {
      ...item,
      from_screen: { x: from.screen.x, y: from.screen.y },
      to_screen: { x: to.screen.x, y: to.screen.y },
      visible: from.visible || to.visible
    };
  });
  return {
    mode,
    viewport,
    transform,
    nodes: projectedNodes,
    links: projectedLinks
  };
}
