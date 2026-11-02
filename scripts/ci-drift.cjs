// Opens, refreshes or closes the `ci-drift` issue after a scheduled
// Terraform Validate run on main (#882).
//
// Why: Terraform Validate ran only on pushes and PRs. When main broke from
// outside the repo (an upstream module or provider release resolving
// differently, as in #881), no check said so until the next PR ran CI,
// while customer deploy archives were broken too. A daily run on main with
// a tracking issue limits that to one day and makes it visible without
// anyone watching cron failures.
//
// Called from actions/github-script with `result` = needs.ci-gate.result.
'use strict';

const LABEL = 'ci-drift';
const TITLE = 'Terraform Validate is failing on main (scheduled run)';

module.exports = async function run({ github, context, core, result }) {
  // Only success and failure/cancelled are signals. A skipped gate checked
  // nothing, so it must not close a real failure.
  const red = result === 'failure' || result === 'cancelled';
  if (!red && result !== 'success') {
    core.info(`ci-gate result is "${result}"; leaving the ${LABEL} issue as it is.`);
    return;
  }

  const { owner, repo } = context.repo;
  const runUrl = `${context.serverUrl}/${owner}/${repo}/actions/runs/${context.runId}`;
  const { data: open } = await github.rest.issues.listForRepo({
    owner, repo, labels: LABEL, state: 'open', per_page: 10,
  });
  const issue = open[0];

  if (!red) {
    if (!issue) return;
    await github.rest.issues.createComment({
      owner, repo, issue_number: issue.number,
      body: `The scheduled run on main is green again: ${runUrl} (commit ${context.sha}). Closing.`,
    });
    await github.rest.issues.update({
      owner, repo, issue_number: issue.number, state: 'closed', state_reason: 'completed',
    });
    core.info(`Closed #${issue.number}.`);
    return;
  }

  const body = [
    `The scheduled Terraform Validate run on main failed (ci-gate: ${result}).`,
    '',
    `- Run: ${runUrl}`,
    `- Commit: ${context.sha}`,
    '',
    'No commit may be needed to cause this: a floating upstream module or',
    'provider range can resolve to a new release (see #881). If a preset',
    'fails `terraform init`, customer deploy archives with that preset fail too.',
    '',
    'This issue closes itself when a scheduled run is green again.',
  ].join('\n');

  if (issue) {
    await github.rest.issues.createComment({
      owner, repo, issue_number: issue.number,
      body: `Still failing (ci-gate: ${result}): ${runUrl} (commit ${context.sha}).`,
    });
    core.warning(`Refreshed #${issue.number}.`);
    return;
  }

  // Create the label first, so the issue search above finds the issue on
  // the next run. A 404 means the label does not exist yet.
  try {
    await github.rest.issues.getLabel({ owner, repo, name: LABEL });
  } catch (e) {
    if (e.status !== 404) throw e;
    await github.rest.issues.createLabel({
      owner, repo, name: LABEL, color: 'b60205',
      description: 'Scheduled CI on main is red (opened and closed by CI)',
    });
  }
  const { data: created } = await github.rest.issues.create({
    owner, repo, title: TITLE, body, labels: [LABEL],
  });
  core.warning(`Opened #${created.number}: ${created.html_url}`);
};
