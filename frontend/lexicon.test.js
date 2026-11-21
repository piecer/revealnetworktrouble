import { readFileSync } from 'node:fs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { LEXICON, FINDING_LABELS, SYMPTOM_PRESETS, resolveLang, label, findingLabel } from './lexicon.js';

const contract = JSON.parse(readFileSync(new URL('../testdata/finding-contract.json', import.meta.url), 'utf8'));
const codes = contract.findings.map(finding => finding.code);
const coverageCodes = ['missing_details', 'malformed_details', 'unsupported_details'];

test('lexicon covers every finding-contract code in both languages', () => {
  assert.equal(codes.length, 23);
  for (const code of codes) {
    for (const lang of ['ko', 'en']) {
      const entry = FINDING_LABELS[lang][code];
      assert.ok(typeof entry === 'string' && entry.length > 0, `${lang}:${code}`);
    }
  }
  for (const code of coverageCodes) {
    for (const lang of ['ko', 'en']) {
      assert.ok(FINDING_LABELS[lang][code], `${lang}:${code}`);
    }
  }
});

test('semantic keys have labels in both languages', () => {
  for (const key of ['cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action']) {
    for (const lang of ['ko', 'en']) {
      assert.ok(LEXICON[lang][key], `${lang}:${key}`);
    }
  }
});

test('the Korean one-liner keeps the scope qualifier', () => {
  for (const status of ['healthy', 'degraded', 'unreachable']) {
    const line = LEXICON.ko[`status.${status}`];
    assert.match(line, /검사 범위 내/, status);
  }
  // A healthy status must not read as "the whole service is healthy".
  assert.match(LEXICON.ko['status.healthy'], /서비스 전체 판정 아님/);
  assert.match(LEXICON.ko['verdict.healthy'], /검사 범위 내/);
});

test('resolveLang defaults to ko and accepts only known languages', () => {
  assert.equal(resolveLang(''), 'ko');
  assert.equal(resolveLang('lang=en'), 'en');
  assert.equal(resolveLang('lang=fr'), 'ko');
  assert.equal(resolveLang('?lang=en&x=1'), 'en');
});

test('symptom presets keep codes as keys and select only contract check kinds', () => {
  assert.deepEqual(SYMPTOM_PRESETS.map(preset => preset.code), ['website', 'mail', 'ssh', 'slow']);
  const kinds = new Set(contract.result_shapes.flatMap(shape => shape.kinds));
  for (const preset of SYMPTOM_PRESETS) {
    assert.ok(preset.kinds.length >= 1, `preset ${preset.code} selects at least one check`);
    assert.ok(preset.kinds.every(kind => kinds.has(kind)), `preset ${preset.code} uses contract kinds only`);
    assert.notEqual(label(`symptom.${preset.code}`, 'ko'), preset.code);
    assert.notEqual(label(`symptom.${preset.code}`, 'en'), preset.code);
  }
});

test('unknown codes fall back to the code itself instead of throwing', () => {
  assert.equal(label('not_a_code', 'ko'), 'not_a_code');
  assert.equal(label('not_a_code', 'en'), 'not_a_code');
  assert.equal(findingLabel('not_a_code', 'ko'), 'not_a_code');
  assert.equal(findingLabel('dns_resolution_failed', 'ko'), 'DNS 해석 실패');
  assert.equal(findingLabel('dns_resolution_failed', 'en'), 'DNS resolution failed');
});
