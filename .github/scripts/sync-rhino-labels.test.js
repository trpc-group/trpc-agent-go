'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');

const syncRhinoLabels = require('./sync-rhino-labels');
const {
  extractIssueNumbers,
  MAX_ISSUE_REFERENCES,
} = syncRhinoLabels;

function createCore() {
  return {
    infoMessages: [],
    warningMessages: [],
    info(message) {
      this.infoMessages.push(message);
    },
    warning(message) {
      this.warningMessages.push(message);
    },
  };
}

function createContext(body, labels = []) {
  return {
    repo: { owner: 'trpc-group', repo: 'trpc-agent-go' },
    payload: {
      pull_request: {
        number: 42,
        body,
        labels,
      },
    },
  };
}

test('extractIssueNumbers finds supported same-repository references', () => {
  const body = [
    'Fixes #2001',
    'Issue: #2002 and related to (#2003).',
    'https://github.com/trpc-group/trpc-agent-go/issues/2004',
    'HTTPS://GITHUB.COM/TRPC-GROUP/TRPC-AGENT-GO/ISSUES/2005',
    'Resolves trpc-group/trpc-agent-go#2006',
    'Duplicate #2001',
  ].join('\n');

  assert.deepEqual(
    extractIssueNumbers(body, 'trpc-group', 'trpc-agent-go'),
    [2001, 2002, 2003, 2004, 2005, 2006],
  );
});

test('extractIssueNumbers ignores cross-repository and pull request URLs', () => {
  const body = [
    'Fixes another-owner/another-repo#2001',
    'See https://github.com/trpc-group/trpc-agent-go/pull/2002',
    'C#2003 is not an issue reference.',
    '##2004 is a heading, not an issue reference.',
  ].join('\n');

  assert.deepEqual(extractIssueNumbers(body, 'trpc-group', 'trpc-agent-go'), []);
});

test('extractIssueNumbers ignores references in comments and code', () => {
  const body = [
    '<!-- Fixes #2001 -->',
    '`Issue #2002`',
    '```text\nRelated to #2003\n```',
    '~~~markdown\nFixes #2004\n~~~',
    'Fixes #2005',
  ].join('\n');

  assert.deepEqual(
    extractIssueNumbers(body, 'trpc-group', 'trpc-agent-go'),
    [2005],
  );
});

test('syncRhinoLabels exits cleanly when the body has no issue references', async () => {
  const core = createCore();

  await syncRhinoLabels({
    github: {},
    context: createContext('No related issue.'),
    core,
  });

  assert.match(core.infoMessages.at(-1), /No same-repository issue references/);
});

test('syncRhinoLabels adds only missing managed labels from referenced issues', async () => {
  const addedLabels = [];
  const github = {
    rest: {
      issues: {
        async get({ issue_number: issueNumber }) {
          if (issueNumber === 2001) {
            return {
              data: {
                labels: [
                  { name: '腾讯犀牛鸟开源专属' },
                  { name: '犀牛鸟-低难度' },
                  { name: 'type/feature' },
                ],
              },
            };
          }
          return {
            data: {
              labels: ['腾讯犀牛鸟开源专属', '犀牛鸟-中高难度'],
            },
          };
        },
        async addLabels({ labels }) {
          addedLabels.push(...labels);
        },
      },
    },
  };
  const core = createCore();
  const context = createContext('Fixes #2001\nRelated to #2003', [
    { name: '腾讯犀牛鸟开源专属' },
  ]);

  await syncRhinoLabels({ github, context, core });

  assert.deepEqual(addedLabels, ['犀牛鸟-低难度', '犀牛鸟-中高难度']);
  assert.match(core.infoMessages.at(-1), /Added labels/);
});

test('syncRhinoLabels skips missing issues, pull requests, and unrelated labels', async () => {
  let addLabelsCalled = false;
  const github = {
    rest: {
      issues: {
        async get({ issue_number: issueNumber }) {
          if (issueNumber === 404) {
            const error = new Error('not found');
            error.status = 404;
            throw error;
          }
          if (issueNumber === 100) {
            return { data: { pull_request: {}, labels: ['犀牛鸟-低难度'] } };
          }
          return { data: { labels: [{ name: 'type/feature' }] } };
        },
        async addLabels() {
          addLabelsCalled = true;
        },
      },
    },
  };
  const core = createCore();

  await syncRhinoLabels({
    github,
    context: createContext('Issue #404, #100, and #200'),
    core,
  });

  assert.equal(addLabelsCalled, false);
  assert.equal(core.warningMessages.length, 1);
  assert.match(core.infoMessages.at(-1), /No Rhino Bird labels/);
});

test('syncRhinoLabels limits the number of issue lookups', async () => {
  let lookupCount = 0;
  const references = Array.from(
    { length: MAX_ISSUE_REFERENCES + 2 },
    (_, index) => `#${index + 1}`,
  ).join(' ');
  const github = {
    rest: {
      issues: {
        async get() {
          lookupCount += 1;
          return { data: { labels: [] } };
        },
        async addLabels() {
          throw new Error('unexpected label write');
        },
      },
    },
  };
  const core = createCore();

  await syncRhinoLabels({
    github,
    context: createContext(references),
    core,
  });

  assert.equal(lookupCount, MAX_ISSUE_REFERENCES);
  assert.equal(core.warningMessages.length, 1);
});

test('syncRhinoLabels fails without a pull request payload', async () => {
  await assert.rejects(
    syncRhinoLabels({
      github: {},
      context: { payload: {}, repo: {} },
      core: createCore(),
    }),
    /pull_request payload is required/,
  );
});

test('syncRhinoLabels propagates unexpected API failures', async () => {
  const github = {
    rest: {
      issues: {
        async get() {
          const error = new Error('rate limited');
          error.status = 429;
          throw error;
        },
      },
    },
  };

  await assert.rejects(
    syncRhinoLabels({
      github,
      context: createContext('Fixes #2001'),
      core: createCore(),
    }),
    /rate limited/,
  );
});
