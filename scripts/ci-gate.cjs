// Decides the ci-gate verdict of Terraform Validate (#943).
//
// Why: ci-gate is the one required check, so it must go red whenever a
// job it stands for did not pass. It used to fail only on `failure` and
// `cancelled`, so a job that was skipped by mistake (a wrong `if:`, or a
// job whose own `needs` failed) passed the gate without running. The
// verdict lives here, not inline in the workflow, so the unit tests in
// ci-gate.test.cjs pin it for every job result.
'use strict';

// The one job that may skip: test-presets skips when discover finds no
// preset with tests, because an empty matrix is an error. Keep this in
// step with test-presets' `if:` in terraform-validate.yml.
const OPTIONAL_WHEN_NO_TESTS = 'test-presets';

// Jobs that are not part of ci-gate's `needs` on purpose: the gate itself,
// and ci-drift, which reports on the gate (it needs ci-gate).
const NOT_GATED = new Set(['ci-gate', 'ci-drift']);

// badJobs returns "job: result" for each needed job that did not pass.
// needs is the `needs` context ({job: {result}}); testable is
// needs.discover.outputs.presets_with_tests (a JSON array as text, or
// empty when discover did not finish).
function badJobs(needs, testable) {
  const noTests = testable === '[]';
  return Object.entries(needs)
    .filter(([job, { result }]) => {
      if (result === 'success') return false;
      if (result === 'skipped' && noTests && job === OPTIONAL_WHEN_NO_TESTS) return false;
      return true;
    })
    .map(([job, { result }]) => `${job}: ${result}`);
}

// ungatedJobs returns each job of the workflow that ci-gate does not
// need. Why: the gate sees only the jobs in its `needs`, so a new job left
// out of that list can fail on every PR while the required check stays
// green. It reads the workflow text by its fixed layout (two-space job
// keys under `jobs:`, a block list under ci-gate's `needs:`) so it needs
// no YAML library.
function ungatedJobs(workflowText) {
  const lines = workflowText.split('\n');
  const jobs = [];
  const gated = new Set();
  let inJobs = false;
  let job = null;
  let inGateNeeds = false;
  for (const line of lines) {
    if (/^\S/.test(line)) {
      inJobs = line.startsWith('jobs:');
      job = null;
      continue;
    }
    if (!inJobs) continue;
    const key = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line);
    if (key) {
      job = key[1];
      jobs.push(job);
      inGateNeeds = false;
      continue;
    }
    if (job !== 'ci-gate') continue;
    if (/^ {4}needs:\s*$/.test(line)) {
      inGateNeeds = true;
      continue;
    }
    if (inGateNeeds) {
      const item = /^ {6}- ([A-Za-z0-9_-]+)\s*$/.exec(line);
      if (item) gated.add(item[1]);
      else if (/^ {4}\S/.test(line)) inGateNeeds = false;
    }
  }
  return jobs.filter((j) => !NOT_GATED.has(j) && !gated.has(j));
}

module.exports = { badJobs, ungatedJobs };

// Run by ci-gate: NEEDS_JSON = toJSON(needs), TESTABLE = discover's output.
if (require.main === module) {
  const bad = badJobs(JSON.parse(process.env.NEEDS_JSON), process.env.TESTABLE ?? '');
  if (bad.length > 0) {
    console.log(bad.join('\n'));
    console.log('::error::A required CI job failed, was cancelled or was skipped.');
    process.exit(1);
  }
  console.log('All required CI jobs passed.');
}
