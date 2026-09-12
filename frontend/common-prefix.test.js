import assert from 'node:assert/strict';
import { commonPrefix, COMMON_MAX_ADDRESSES } from './common-prefix.js';

// helper: family별 첫 그룹을 꺼낸다.
const g = r => r.groups[0];

// IPv4 /24: 마지막 octet만 0~255로 달라지면 상위 24비트 공유
{
  const r = commonPrefix(['192.168.1.10', '192.168.1.55', '192.168.1.200']);
  assert.equal(r.groups.length, 1);
  assert.equal(g(r).family, 'v4');
  assert.equal(g(r).count, 3);
  assert.equal(g(r).sharedBits, 24);
  assert.equal(g(r).cidr, '192.168.1.0/24');
  assert.equal(g(r).rangeFirst, '192.168.1.0');
  assert.equal(g(r).rangeLast, '192.168.1.255');
  assert.equal(g(r).firstDiffBit, 25); // 공유 24비트 + 1
}

// IPv4 /30: .8~.11이 정확히 한 /30 블록을 채운다
{
  const r = commonPrefix(['10.0.0.8', '10.0.0.9', '10.0.0.10', '10.0.0.11']);
  assert.equal(g(r).sharedBits, 30);
  assert.equal(g(r).cidr, '10.0.0.8/30');
  assert.equal(g(r).rangeFirst, '10.0.0.8');
  assert.equal(g(r).rangeLast, '10.0.0.11');
}

// IPv4: 첫 octet MSB가 다르면 공유 비트 0 (전체 IPv4 공간)
{
  const r = commonPrefix(['192.0.2.1', '10.0.0.1']); // 192=1100..., 10=0000...
  assert.equal(g(r).sharedBits, 0);
  assert.equal(g(r).cidr, '0.0.0.0/0');
  assert.equal(g(r).rangeFirst, '0.0.0.0');
  assert.equal(g(r).rangeLast, '255.255.255.255');
  assert.equal(g(r).firstDiffBit, 1);
}

// IPv4: 동일한 주소 두 개 -> /32 (전체 32비트 공유)
{
  const r = commonPrefix(['172.16.5.9', '172.16.5.9']);
  assert.equal(g(r).sharedBits, 32);
  assert.equal(g(r).cidr, '172.16.5.9/32');
  assert.equal(g(r).firstDiffBit, null);
}

// IPv4 /22: 두 번째 octet이 0과 3(0000 vs 0011)로 갈라져 상위 22비트 공유
{
  const r = commonPrefix(['192.168.0.5', '192.168.3.7']);
  assert.equal(g(r).sharedBits, 22);
  assert.equal(g(r).cidr, '192.168.0.0/22');
  assert.equal(g(r).rangeFirst, '192.168.0.0');
  assert.equal(g(r).rangeLast, '192.168.3.255');
}

// IPv4: 마지막 octet 상위 비트가 다르면 /26 (10=00001010 vs 55=00110111)
{
  const r = commonPrefix(['192.168.1.10', '192.168.1.55']);
  assert.equal(g(r).sharedBits, 26);
  assert.equal(g(r).cidr, '192.168.1.0/26');
  assert.equal(g(r).rangeLast, '192.168.1.63');
}

// IPv6: 같은 /48 안에서 주소 -> 최소 48비트 공유
{
  const r = commonPrefix(['2001:db8:aaaa::1', '2001:db8:aaaa::9f']);
  assert.equal(g(r).family, 'v6');
  assert.ok(g(r).sharedBits >= 48);
}

// IPv6 /121: 마지막 byte가 0x05 vs 0x77로 갈라져 상위 121비트 공유
{
  const r = commonPrefix(['2001:db8::5', '2001:db8::77']);
  assert.equal(g(r).sharedBits, 121);
  assert.equal(g(r).cidr, '2001:db8::/121');
  assert.equal(g(r).rangeFirst, '2001:db8::');
  assert.equal(g(r).rangeLast, '2001:db8::7f');
}

// IPv6: 완전히 같은 주소(확장 vs compact) -> 128비트 공유
{
  const r = commonPrefix(['fe80::1', 'FE80:0000:0000:0000:0000:0000:0000:0001']);
  assert.equal(g(r).sharedBits, 128);
  assert.equal(g(r).cidr, 'fe80::1/128');
  assert.equal(g(r).firstDiffBit, null);
}

// IPv6: 첫 word가 다르면 공유 비트가 작다 (2001 vs 203c -> /10)
{
  const r = commonPrefix(['2001:db8::1', '203c::1']);
  assert.equal(g(r).sharedBits, 10);
  assert.equal(g(r).cidr, '2000::/10');
}

// family 혼합: v4 하나 + v6 둘 -> v4는 alone(비교 불가), v6는 구간 계산
{
  const r = commonPrefix(['192.168.1.10', '2001:db8::5', '2001:db8::77']);
  assert.equal(r.groups.length, 2);
  const v4 = r.groups.find(x => x.family === 'v4');
  const v6 = r.groups.find(x => x.family === 'v6');
  assert.ok(v4.alone);
  assert.equal(v4.cidr, null);
  assert.equal(v6.count, 2);
  assert.equal(v6.sharedBits, 121);
}

// 같은 family가 두 개 이상이어야 구간이 성립한다 (둘 다 v4 -> 계산)
{
  const r = commonPrefix(['8.8.8.8', '1.1.1.1']);
  assert.equal(r.groups.length, 1);
  assert.ok(!r.groups[0].alone);
}

// 무효 주소는 skipped로 보고되고 분석에서 제외된다
{
  const r = commonPrefix(['192.168.1.10', 'not-an-ip', '999.999.999.999']);
  assert.equal(r.parsed, 1);
  assert.ok(r.skipped.length >= 1);
  const v4 = r.groups.find(x => x.family === 'v4');
  assert.ok(v4.alone); // 유효 주소가 하나만 남으므로 단독
}

// 한도(64개) 초과 입력은 truncated로 보고하고 한도까지만 분석한다
{
  const many = Array.from({ length: COMMON_MAX_ADDRESSES + 5 }, (_, i) => `10.0.${i >> 8}.${i & 255}`);
  const r = commonPrefix(many);
  assert.equal(r.totalInput, COMMON_MAX_ADDRESSES + 5);
  assert.equal(r.analyzed, COMMON_MAX_ADDRESSES);
  assert.equal(r.parsed, COMMON_MAX_ADDRESSES);
  assert.equal(r.truncated, true);
}

// empty / blank 입력은 분석 대상이 없다
{
  const r = commonPrefix(['', '   ', '\n']);
  assert.equal(r.parsed, 0);
  assert.equal(r.groups.length, 0);
}

// canonicalization: 확장된 IPv6는 압축되고 ::ffff-mapped는 v4로 환원
{
  const expanded = commonPrefix(['2001:0DB8:0000:0000:0000:0000:0000:0005', '2001:db8::7']);
  assert.equal(g(expanded).family, 'v6');
  assert.ok(g(expanded).sharedBits >= 48);

  const mapped = commonPrefix(['::ffff:192.168.1.5', '::ffff:192.168.1.9']);
  assert.equal(mapped.groups.length, 1);
  assert.equal(g(mapped).family, 'v4'); // canonicalIP 관례에 따라 v4로 환원
  assert.equal(g(mapped).cidr, '192.168.1.0/28');
  assert.deepEqual(g(mapped).members.map(member => member.canonical), ['192.168.1.5', '192.168.1.9']);
}

// IPv6 zone ID는 비교 identity에서 제외하지만 빈 zone ID는 유효하지 않다.
{
  const zoned = commonPrefix(['fe80::1%eth0', 'fe80::2%wlan0']);
  assert.equal(g(zoned).family, 'v6');
  assert.equal(g(zoned).cidr, 'fe80::/126');
  const emptyZone = commonPrefix(['fe80::1%', 'fe80::2']);
  assert.equal(emptyZone.skipped.length, 1);
  assert.equal(emptyZone.groups[0].alone, true);
}

console.log('common-prefix unit tests passed');
