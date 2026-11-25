// U-2: code-based ko/en display dictionary. The API keeps `code` as the stable
// contract key; only display strings are translated here. English remains the
// canonical fallback (human export and untranslatable codes), and an unknown
// code always falls back to the code itself rather than throwing.
export const LEXICON_LANGS = Object.freeze(['ko', 'en']);

export function resolveLang(search = '') {
  const lang = new URLSearchParams(String(search)).get('lang');
  return LEXICON_LANGS.includes(lang) ? lang : 'ko';
}

const KO = Object.freeze({
  // Semantic keys (contract order preserved; only the visible heading changes).
  cause: '후보 원인',
  supporting_evidence: '근거',
  expectation: '기대 동작',
  evidence_directness: '근거 직접성',
  coverage_limitation: '커버리지 한계',
  next_action: '다음 단계',

  // Section titles.
  'section.verdict': '판정',
  'section.findings': '판정 항목',
  'section.coverage': '커버리지 한계',
  'section.analysis_unavailable': '자동 분석 불가',

  // Status one-liner. The scope qualifier is part of the wording: a healthy
  // status means the configured checks completed, never the whole service.
  'status.healthy': '정상 — 검사 범위 내 설정된 검사 완료 (서비스 전체 판정 아님)',
  'status.degraded': '주의 — 검사 범위 내 확인 필요한 관측 (서비스 전체 판정 아님)',
  'status.unreachable': '주의 — 검사 범위 내 도달 불가 관측 (서비스 전체 판정 아님)',

  // Verdict one-liner.
  'verdict.healthy': '검사 범위 내 주의 필요 판정 없음',
  'verdict.attention': '검사 범위 내 주의 필요',
  'verdict.inconclusive': '검사 범위 내 판정 불가',

  // Per-check status and outcome.
  'result_status.healthy': '이 검사는 검사 범위 내 완료',
  'result_status.degraded': '이 검사에서 확인 필요한 관측',
  'result_status.unreachable': '이 검사로 endpoint 도달성 확인 불가',
  'outcome.healthy': '관측 완료',
  'outcome.degraded': '관측 주의',
  'outcome.unreachable': '도달 불가',

  // Card labels.
  'metric.checks': '검사',
  'metric.passed': '문제 없음',
  'metric.failed': '문제 보고',
  'metric.duration': '소요',
  'metric.observation_point': '관측 위치',
  'metric.coverage': '커버리지',
  'check_prefix': '검사',
  'toggle.show_panel': '접힌 근거·직접성·한계 보기',
  'interpretation_guide': '판단 기준',
  'coverage_detail': '한계 목록',

  // Evidence directness (short label; the canonical caveat stays in the guide).
  direct: '직접 근거',
  corroborated: '교차 검증',
  limited: '한계 근거',

  // U-3 symptom presets: the code is the stable key, only the button label.
  'symptom_presets': '증상 프리셋',
  'symptom.website': '웹사이트',
  'symptom.mail': '메일',
  'symptom.ssh': 'SSH',
  'symptom.slow': '느림',
});

const EN = Object.freeze({
  cause: 'Cause',
  supporting_evidence: 'Supporting evidence',
  expectation: 'Expectation',
  evidence_directness: 'Evidence directness',
  coverage_limitation: 'Coverage limitation',
  next_action: 'Next action',

  'section.verdict': 'Verdict',
  'section.findings': 'Findings',
  'section.coverage': 'Coverage limitation',
  'section.analysis_unavailable': 'Automated analysis unavailable',

  'status.healthy': 'All configured checks completed within their observed scope.',
  'status.degraded': 'Some checks need attention within the observed scope.',
  'status.unreachable': 'Some endpoints could not be reached within the observed scope.',

  'verdict.healthy': 'No finding in the observed scope requires attention.',
  'verdict.attention': 'A finding in the observed scope requires attention.',
  'verdict.inconclusive': 'The observed scope is insufficient for a conclusion.',

  'result_status.healthy': 'No issue reported in this check scope',
  'result_status.degraded': 'Observation requiring attention in this check',
  'result_status.unreachable': 'Endpoint reachability cannot be confirmed by this check',
  'outcome.healthy': 'Observation completed',
  'outcome.degraded': 'Observation requires attention',
  'outcome.unreachable': 'Endpoint reachability not confirmed',

  'metric.checks': 'Checks',
  'metric.passed': 'passed',
  'metric.failed': 'failed',
  'metric.duration': 'duration',
  'metric.observation_point': 'Observation point',
  'metric.coverage': 'Coverage',
  'check_prefix': 'Check',
  'toggle.show_panel': 'Show bounded evidence and action',
  'interpretation_guide': 'Interpretation guide',
  'coverage_detail': 'Coverage details',

  direct: 'Direct evidence: this check observed the signal itself.',
  corroborated: 'Corroborated by independent observation.',
  limited: 'Limited evidence: independent corroboration is required before a conclusion.',

  'symptom_presets': 'Symptom presets',
  'symptom.website': 'Website',
  'symptom.mail': 'Mail',
  'symptom.ssh': 'SSH',
  'symptom.slow': 'Slow',
});

// U-3: symptom -> contract check kinds. Codes are stable keys; kinds must come
// from the finding contract's result shapes.
export const SYMPTOM_PRESETS = Object.freeze([
  { code: 'website', kinds: Object.freeze(['https', 'http']) },
  { code: 'mail', kinds: Object.freeze(['smtp', 'imap']) },
  { code: 'ssh', kinds: Object.freeze(['ssh']) },
  { code: 'slow', kinds: Object.freeze(['traceroute', 'http']) },
]);

// Short display labels for finding codes. Keys must cover the finding contract
// (testdata/finding-contract.json) plus the coverage codes; a missing key is
// not an error at runtime — label() falls back to the code itself.
const FINDINGS_KO = Object.freeze({
  checker_capacity_unavailable: '검사기 용량 없음',
  checker_panic: '검사기 비정상 종료',
  dns_resolution_failed: 'DNS 해석 실패',
  endpoint_connect_failed: 'endpoint 접속 실패',
  execution_cancelled: '실행 취소됨',
  execution_timeout: '실행 시간 초과',
  http_slow_server_response: 'HTTP 서버 응답 지연',
  http_unexpected_status: 'HTTP 예상 밖 상태',
  intermittent_connectivity: '간헐적 접속',
  invalid_target: '잘못된 대상',
  local_resolver_divergence: '로컬 해석기 차이',
  service_greeting_unverified: '서비스 greeting 미검증',
  target_policy_blocked: '대상 정책 차단',
  tls_certificate_expired: 'TLS 인증서 만료',
  tls_certificate_expiring: 'TLS 인증서 만료 임박',
  tls_certificate_not_yet_valid: 'TLS 인증서 아직 유효 전',
  tls_downgrade: 'TLS 강등 관측',
  tls_handshake_failed: 'TLS handshake 실패',
  tls_hostname_mismatch: 'TLS hostname 불일치',
  tls_untrusted: 'TLS 신뢰 불가',
  traceroute_execution_failed: 'traceroute 실행 실패',
  traceroute_partial_reachability: 'traceroute 부분 도달',
  traceroute_path_degraded: 'traceroute 경로 품질 저하',
  traceroute_path_unstable: 'traceroute 경로 불안정',
  traceroute_unavailable: 'traceroute 사용 불가',
  traceroute_unreachable: 'traceroute 도달 불가',
  configuration_disabled: '설정에서 꺼짐',
  missing_details: '세부 정보 없음',
  malformed_details: '세부 정보 손상',
  unsupported_details: '지원하지 않는 세부 정보',
});

const FINDINGS_EN = Object.freeze({
  checker_capacity_unavailable: 'checker capacity unavailable',
  checker_panic: 'checker panic',
  dns_resolution_failed: 'DNS resolution failed',
  endpoint_connect_failed: 'endpoint connect failed',
  execution_cancelled: 'execution cancelled',
  execution_timeout: 'execution timeout',
  http_slow_server_response: 'HTTP slow server response',
  http_unexpected_status: 'HTTP unexpected status',
  intermittent_connectivity: 'intermittent connectivity',
  invalid_target: 'invalid target',
  local_resolver_divergence: 'local resolver divergence',
  service_greeting_unverified: 'service greeting unverified',
  target_policy_blocked: 'target policy blocked',
  tls_certificate_expired: 'TLS certificate expired',
  tls_certificate_expiring: 'TLS certificate expiring',
  tls_certificate_not_yet_valid: 'TLS certificate not yet valid',
  tls_downgrade: 'TLS downgrade observed',
  tls_handshake_failed: 'TLS handshake failed',
  tls_hostname_mismatch: 'TLS hostname mismatch',
  tls_untrusted: 'TLS untrusted',
  traceroute_execution_failed: 'traceroute execution failed',
  traceroute_partial_reachability: 'traceroute partial reachability',
  traceroute_path_degraded: 'traceroute path degraded',
  traceroute_path_unstable: 'traceroute path unstable',
  traceroute_unavailable: 'traceroute unavailable',
  traceroute_unreachable: 'traceroute unreachable',
  configuration_disabled: 'configuration disabled',
  missing_details: 'missing details',
  malformed_details: 'malformed details',
  unsupported_details: 'unsupported details',
});

export const LEXICON = Object.freeze({ ko: KO, en: EN });
export const FINDING_LABELS = Object.freeze({ ko: FINDINGS_KO, en: FINDINGS_EN });

export function label(code, lang = 'ko') {
  const table = LEXICON[lang] || EN;
  return table[code] ?? EN[code] ?? code;
}

export function findingLabel(code, lang = 'ko') {
  const table = FINDING_LABELS[lang] || FINDINGS_EN;
  return table[code] ?? FINDINGS_EN[code] ?? code;
}
