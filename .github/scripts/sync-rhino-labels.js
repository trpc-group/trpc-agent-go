'use strict';

const MAX_ISSUE_REFERENCES = 20;
const MANAGED_LABELS = Object.freeze([
  '腾讯犀牛鸟开源专属',
  '犀牛鸟-低难度',
  '犀牛鸟-中高难度',
]);

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function stripIgnoredMarkdown(value) {
  return value
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/```[\s\S]*?```/g, '')
    .replace(/~~~[\s\S]*?~~~/g, '')
    .replace(/`[^`\r\n]*`/g, '');
}

function extractIssueNumbers(body, owner, repo) {
  const text = stripIgnoredMarkdown(body || '');
  const matches = [];
  const issueURLPattern = new RegExp(
    `https?://github\\.com/${escapeRegExp(owner)}/${escapeRegExp(repo)}/issues/([1-9]\\d*)\\b`,
    'gi',
  );
  const qualifiedReferencePattern = new RegExp(
    `\\b${escapeRegExp(owner)}/${escapeRegExp(repo)}#([1-9]\\d*)\\b`,
    'gi',
  );
  const localReferencePattern = /(^|[^\w./#-])#([1-9]\d*)\b/gm;

  for (const match of text.matchAll(issueURLPattern)) {
    matches.push({ index: match.index, issueNumber: Number(match[1]) });
  }
  for (const match of text.matchAll(qualifiedReferencePattern)) {
    matches.push({ index: match.index, issueNumber: Number(match[1]) });
  }
  for (const match of text.matchAll(localReferencePattern)) {
    matches.push({ index: match.index, issueNumber: Number(match[2]) });
  }

  matches.sort((left, right) => left.index - right.index);
  return [...new Set(matches.map((match) => match.issueNumber))];
}

function labelName(label) {
  return typeof label === 'string' ? label : label.name;
}

async function syncRhinoLabels({ github, context, core }) {
  const pullRequest = context.payload.pull_request;
  if (!pullRequest) {
    throw new Error('pull_request payload is required');
  }

  const { owner, repo } = context.repo;
  const referencedIssues = extractIssueNumbers(pullRequest.body, owner, repo);
  if (referencedIssues.length === 0) {
    core.info('No same-repository issue references found in the pull request body.');
    return;
  }

  if (referencedIssues.length > MAX_ISSUE_REFERENCES) {
    core.warning(
      `Found ${referencedIssues.length} issue references; only the first ${MAX_ISSUE_REFERENCES} will be checked.`,
    );
  }

  const managedLabels = new Set(MANAGED_LABELS);
  const desiredLabels = new Set();
  for (const issueNumber of referencedIssues.slice(0, MAX_ISSUE_REFERENCES)) {
    let response;
    try {
      response = await github.rest.issues.get({
        owner,
        repo,
        issue_number: issueNumber,
      });
    } catch (error) {
      if (error.status === 404) {
        core.warning(`Referenced issue #${issueNumber} was not found; skipping it.`);
        continue;
      }
      throw error;
    }

    if (response.data.pull_request) {
      core.info(`#${issueNumber} is a pull request, not an issue; skipping it.`);
      continue;
    }

    for (const label of response.data.labels || []) {
      const name = labelName(label);
      if (managedLabels.has(name)) {
        desiredLabels.add(name);
      }
    }
  }

  const currentLabels = new Set((pullRequest.labels || []).map(labelName));
  const missingLabels = [...desiredLabels].filter((label) => !currentLabels.has(label));
  if (missingLabels.length === 0) {
    core.info('No Rhino Bird labels need to be added.');
    return;
  }

  await github.rest.issues.addLabels({
    owner,
    repo,
    issue_number: pullRequest.number,
    labels: missingLabels,
  });
  core.info(`Added labels to pull request #${pullRequest.number}: ${missingLabels.join(', ')}`);
}

module.exports = syncRhinoLabels;
module.exports.extractIssueNumbers = extractIssueNumbers;
module.exports.MANAGED_LABELS = MANAGED_LABELS;
module.exports.MAX_ISSUE_REFERENCES = MAX_ISSUE_REFERENCES;
