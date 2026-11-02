// Unit tests for scripts/ci-drift.cjs (#882). Run with `node --test scripts/`.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const run = require('./ci-drift.cjs');

// fakeGithub records every REST call and answers the issue search with
// `open` (the open drift issues).
function fakeGithub(open) {
  const calls = [];
  const rec = (name, ret) => async (args) => { calls.push({ name, args }); return { data: ret } };
  return {
    calls,
    rest: {
      issues: {
        listForRepo: rec('listForRepo', open),
        getLabel: rec('getLabel', {}),
        createLabel: rec('createLabel', {}),
        create: rec('create', { number: 42, html_url: 'https://example/42' }),
        createComment: rec('createComment', {}),
        update: rec('update', {}),
      },
    },
  };
}

const context = {
  repo: { owner: 'o', repo: 'r' },
  serverUrl: 'https://github.com',
  runId: 7,
  sha: 'abc123',
};
const core = { info() {}, warning() {} };
const names = (gh) => gh.calls.map((c) => c.name);

test('red run with no open issue opens one', async () => {
  const gh = fakeGithub([]);
  await run({ github: gh, context, core, result: 'failure' });
  assert.ok(names(gh).includes('create'));
  const created = gh.calls.find((c) => c.name === 'create').args;
  assert.deepEqual(created.labels, ['ci-drift']);
  assert.match(created.body, /actions\/runs\/7/);
});

test('red run with an open issue comments on it, no second issue', async () => {
  const gh = fakeGithub([{ number: 9 }]);
  await run({ github: gh, context, core, result: 'failure' });
  assert.ok(!names(gh).includes('create'));
  const c = gh.calls.find((x) => x.name === 'createComment').args;
  assert.equal(c.issue_number, 9);
});

test('cancelled gate changes nothing', async () => {
  // A cancelled run (a newer run, or a person, stopped it) says nothing
  // about main: it must neither open nor close an issue.
  const gh = fakeGithub([{ number: 9 }]);
  await run({ github: gh, context, core, result: 'cancelled' });
  assert.deepEqual(names(gh), []);
});

test('pull requests with the label are not drift issues', async () => {
  // listForRepo returns pull requests too; a labelled PR must not be
  // commented on or closed as if it were the drift issue.
  const pr = { number: 5, pull_request: { url: 'x' } };
  let gh = fakeGithub([pr]);
  await run({ github: gh, context, core, result: 'success' });
  assert.deepEqual(names(gh), ['listForRepo']);
  gh = fakeGithub([pr]);
  await run({ github: gh, context, core, result: 'failure' });
  assert.ok(names(gh).includes('create'));
  assert.ok(!gh.calls.some((c) => c.name === 'createComment' && c.args.issue_number === 5));
});

test('green run closes every open drift issue', async () => {
  const gh = fakeGithub([{ number: 3 }, { number: 9 }]);
  await run({ github: gh, context, core, result: 'success' });
  const closed = gh.calls.filter((c) => c.name === 'update').map((c) => c.args.issue_number);
  assert.deepEqual(closed.sort(), [3, 9]);
});

test('red run comments on the oldest open drift issue only', async () => {
  const gh = fakeGithub([{ number: 3 }, { number: 9 }]);
  await run({ github: gh, context, core, result: 'failure' });
  const listed = gh.calls.find((c) => c.name === 'listForRepo').args;
  assert.equal(listed.sort, 'created');
  assert.equal(listed.direction, 'asc');
  const comments = gh.calls.filter((c) => c.name === 'createComment').map((c) => c.args.issue_number);
  assert.deepEqual(comments, [3]);
  assert.ok(!names(gh).includes('create'));
});

test('green run closes the open issue', async () => {
  const gh = fakeGithub([{ number: 9 }]);
  await run({ github: gh, context, core, result: 'success' });
  const u = gh.calls.find((x) => x.name === 'update').args;
  assert.equal(u.issue_number, 9);
  assert.equal(u.state, 'closed');
  assert.ok(names(gh).includes('createComment'));
});

test('green run with no open issue does nothing', async () => {
  const gh = fakeGithub([]);
  await run({ github: gh, context, core, result: 'success' });
  assert.deepEqual(names(gh), ['listForRepo']);
});

test('skipped gate never closes the issue', async () => {
  // A skipped gate checked nothing; reading it as green would close a
  // real failure.
  const gh = fakeGithub([{ number: 9 }]);
  await run({ github: gh, context, core, result: 'skipped' });
  assert.deepEqual(names(gh), []);
});
