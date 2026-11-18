// Unit tests for scripts/ci-gate.cjs (#943). Run with `node --test scripts/`.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { badJobs, ungatedJobs } = require('./ci-gate.cjs');

const ok = { result: 'success' };
const skipped = { result: 'skipped' };

test('all jobs passed: gate passes', () => {
  assert.deepEqual(badJobs({ discover: ok, lint: ok, 'test-presets': ok }, '["aws/x"]'), []);
});

test('failed or cancelled job fails the gate', () => {
  assert.deepEqual(
    badJobs({ discover: ok, lint: { result: 'failure' }, 'go-test': { result: 'cancelled' } }, '[]'),
    ['lint: failure', 'go-test: cancelled'],
  );
});

test('test-presets may skip when no preset has tests', () => {
  assert.deepEqual(badJobs({ discover: ok, 'test-presets': skipped }, '[]'), []);
});

test('test-presets may not skip when presets have tests', () => {
  assert.deepEqual(badJobs({ discover: ok, 'test-presets': skipped }, '["aws/x"]'), ['test-presets: skipped']);
});

test('any other skipped job fails the gate (#943)', () => {
  assert.deepEqual(badJobs({ lint: skipped, 'test-presets': skipped }, '[]'), ['lint: skipped']);
});

test('discover failed: its output is empty, so the test-presets skip counts too', () => {
  assert.deepEqual(
    badJobs({ discover: { result: 'failure' }, 'test-presets': skipped }, ''),
    ['discover: failure', 'test-presets: skipped'],
  );
});

test('ungatedJobs finds a job missing from ci-gate needs', () => {
  const wf = [
    'on: push',
    'jobs:',
    '  a:',
    '    runs-on: x',
    '  b:',
    '    needs: a',
    '  ci-gate:',
    '    needs:',
    '      - a',
    '    if: always()',
    '  ci-drift:',
    '    needs: ci-gate',
  ].join('\n');
  assert.deepEqual(ungatedJobs(wf), ['b']);
});

// The real workflow: every job but ci-gate and ci-drift must be gated.
test('terraform-validate.yml: ci-gate needs every job', () => {
  const wf = fs.readFileSync(path.join(__dirname, '..', '.github', 'workflows', 'terraform-validate.yml'), 'utf8');
  assert.deepEqual(ungatedJobs(wf), []);
  // The parse must see the real layout: drop one need and it is reported.
  assert.ok(wf.includes('      - lint\n'));
  assert.deepEqual(ungatedJobs(wf.replace('      - lint\n', '')), ['lint']);
});
