// 공통 구간 계산: 여러 IP 주소를 입력받아 동일 family(IPv4/IPv6) 내에서
// 모든 주소가 공유하는 가장 긴 공통 접두(longest common prefix, LCP)를
// CIDR·범위로 산출한다. 순수 함수만 제공해 DOM/UI 측과 분리하고 단위 테스트에
// 유리하게 한다. BigInt로 IPv4(32비트)/IPv6(128비트) 비트 공간을 다루며
// min/max XOR으로 O(1)에 공통 접두 길이를 결정한다.

export const COMMON_MAX_ADDRESSES = 64; // DOM/작업량 상한: 초과 입력은 생략 보고
export const COMMON_MAX_INPUT_CHARS = 8192; // 분할 전 textarea 메모리/작업량 상한

const V4_WIDTH = 32;
const V6_WIDTH = 128;

// IPv4 "a.b.c.d" -> { value: BigInt } | null (leading-zero 거부, 0-255 검증)
function ipv4Bits(text) {
  const parts = String(text).split('.');
  if (parts.length !== 4) return null;
  let value = 0n;
  for (const part of parts) {
    if (!/^(0|[1-9]\d{0,2})$/.test(part)) return null;
    const byte = Number(part);
    if (byte > 255) return null;
    value = (value << 8n) | BigInt(byte);
  }
  return { family: 'v4', width: V4_WIDTH, value };
}

// IPv6 주소(선택적 zone id 포함) -> { words: number[8] } | null
function ipv6Words(text) {
  let address = String(text);
  const zoneAt = address.indexOf('%');
  if (zoneAt >= 0) {
    if (!address.slice(zoneAt + 1)) return null; // 기존 canonicalIP와 같이 빈 zone ID 거부
    address = address.slice(0, zoneAt); // 비교에는 인터페이스별 zone ID 제외
  }
  if (!address.includes(':') || (address.match(/::/g)?.length ?? 0) > 1) return null;
  const halves = address.split('::');
  if (halves.length > 2) return null;
  const parseHalf = (half, allowV4Tail) => {
    if (half === '') return [];
    const raw = half.split(':');
    const words = [];
    for (let i = 0; i < raw.length; i++) {
      const part = raw[i];
      if (part.includes('.')) {
        if (!allowV4Tail || i !== raw.length - 1) return null;
        const v4 = ipv4Bits(part);
        if (!v4) return null;
        words.push(Number((v4.value >> 16n) & 0xffffn), Number(v4.value & 0xffffn));
      } else {
        if (!/^[0-9a-fA-F]{1,4}$/.test(part)) return null;
        words.push(Number.parseInt(part, 16));
      }
    }
    return words;
  };
  const left = parseHalf(halves[0], halves.length === 1);
  const right = halves.length === 2 ? parseHalf(halves[1], true) : [];
  if (!left || !right) return null;
  let words;
  if (halves.length === 2) {
    const missing = 8 - left.length - right.length;
    if (missing < 1) return null; // '::'는 최소 한 단어의 0을 의미해야 한다
    words = [...left, ...Array(missing).fill(0), ...right];
  } else {
    if (left.length !== 8) return null;
    words = left;
  }
  if (words.length !== 8) return null;
  // IPv4-mapped (::ffff:a.b.c.d)는 이 앱의 canonicalIP 관례(192.0.2.1로 환원)를
  // 따르며, 실제로도 IPv4 주소이므로 v4 family 비트로 반환해 같은 family로 비교된다.
  if (words.slice(0, 5).every(w => w === 0) && words[5] === 0xffff) {
    return { family: 'v4', width: V4_WIDTH, value: (BigInt(words[6]) << 16n) | BigInt(words[7]) };
  }
  const value = words.reduce((acc, w) => (acc << 16n) | BigInt(w), 0n);
  return { family: 'v6', width: V6_WIDTH, value };
}

// 입력 텍스트를 family별 비트 값으로 파싱. hostname 등 비-IP는 null로 생략.
export function parseAddressBits(rawText) {
  const text = String(rawText ?? '').trim();
  if (!text || text.includes(' ')) return null; // 주소에 공백은 허용하지 않는다
  return ipv4Bits(text) ?? ipv6Words(text);
}

// width 비트 공간에서 BigInt 값 -> canonical 문자열 (v4는 dotted, v6는 compact)
function formatValue(value, family, width) {
  if (family === 'v4') {
    const bytes = [];
    for (let shift = 24; shift >= 0; shift -= 8) bytes.push(Number((value >> BigInt(shift)) & 0xffn));
    return bytes.join('.');
  }
  const words = [];
  for (let shift = 112; shift >= 0; shift -= 16) words.push(Number((value >> BigInt(shift)) & 0xffffn));
  // 최장 연속 0 단어를 '::'로 압축 (최소 2개일 때만, 없으면 평탄화)
  let bestStart = -1, bestLen = 0;
  for (let i = 0; i < words.length;) {
    if (words[i] !== 0) { i++; continue; }
    let j = i; while (j < words.length && words[j] === 0) j++;
    if (j - i > bestLen && j - i >= 2) [bestStart, bestLen] = [i, j - i];
    i = j;
  }
  const hex = w => w.toString(16);
  if (bestStart < 0) return words.map(hex).join(':');
  const before = words.slice(0, bestStart).map(hex).join(':');
  const after = words.slice(bestStart + bestLen).map(hex).join(':');
  return `${before}::${after}`;
}

// 한 family 그룹의 LCP를 계산. members는 parseAddressBits 결과 배열(>=2개).
function computeGroup(members) {
  let min = members[0].value, max = members[0].value;
  for (const member of members.slice(1)) {
    if (member.value < min) min = member.value;
    if (member.value > max) max = member.value;
  }
  const width = members[0].width;
  let shared; // 공유 비트 수 (0..width)
  if (min === max) {
    shared = width;
  } else {
    const diff = min ^ max;
    let bitLen = 0, cursor = diff;
    while (cursor !== 0n) { cursor >>= 1n; bitLen++; } // 정확한 비트 길이 (BigInt)
    shared = width - bitLen;
  }
  const family = members[0].family;
  const freeBits = BigInt(width - shared);
  const lowMask = freeBits > 0n ? (1n << freeBits) - 1n : 0n;
  const first = min & ~lowMask; // 범위 하한: 하위 자유 비트를 모두 0 (네트워크 주소)
  const last = min | lowMask;   // 범위 상한: 하위 자유 비트를 모두 1
  return {
    family,
    count: members.length,
    sharedBits: shared,
    cidr: `${formatValue(first, family, width)}/${shared}`, // 네트워크 주소로 표기
    rangeFirst: formatValue(first, family, width),
    rangeLast: formatValue(last, family, width),
    firstDiffBit: shared === width ? null : shared + 1, // MSB=1 기준 첫 다름 비트(공유 비트 바로 다음)
    members: members.map(m => ({ canonical: formatValue(m.value, family, width) }))
  };
}

// 입력 주소 목록 -> family별 공통 구간 그룹 + 생략 보고.
export function commonPrefix(rawAddresses = []) {
  const rawList = Array.isArray(rawAddresses) ? rawAddresses : [rawAddresses];
  const truncated = rawList.length > COMMON_MAX_ADDRESSES;
  const working = truncated ? rawList.slice(0, COMMON_MAX_ADDRESSES) : [...rawList];

  const groups = new Map(); // family -> members[]
  const skipped = [];
  for (const raw of working) {
    const text = String(raw ?? '').trim();
    if (!text) continue;
    const bits = parseAddressBits(text);
    if (!bits) { skipped.push({ raw: text, reason: 'invalid' }); continue; }
    if (!groups.has(bits.family)) groups.set(bits.family, []);
    groups.get(bits.family).push(bits);
  }

  const resultGroups = [];
  for (const [family, members] of [...groups.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
    if (members.length < 2) {
      // 같은 family 주소가 하나뿐이면 비교 대상이 없어 공통 구간을 계산하지 않는다.
      resultGroups.push({
        family, count: members.length, sharedBits: null, cidr: null,
        rangeFirst: formatValue(members[0].value, family, members[0].width),
        rangeLast: null, firstDiffBit: null, alone: true,
        members: [{ canonical: formatValue(members[0].value, family, members[0].width) }]
      });
    } else {
      resultGroups.push({ ...computeGroup(members), alone: false });
    }
  }

  return {
    totalInput: rawList.length,
    analyzed: Math.min(rawList.length, COMMON_MAX_ADDRESSES),
    truncated,
    parsed: [...groups.values()].reduce((n, m) => n + m.length, 0),
    skipped,
    groups: resultGroups
  };
}
