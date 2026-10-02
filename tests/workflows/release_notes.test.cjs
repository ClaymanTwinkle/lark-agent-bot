const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { test } = require('node:test');

const workflow = fs.readFileSync(
  path.join(__dirname, '../../.github/workflows/release.yml'), 'utf8',
);
// Run the actual release-notes step in a throwaway repository.
const step = workflow.split('      - name: Write release notes')[1];
assert.ok(step, 'release notes step must exist');
const block = step.match(/        run: \|\r?\n((?:          .*\r?\n|\r?\n)+)/);
assert.ok(block, 'release notes step must contain a shell script');
const script = block[1].replace(/^          /gm, '').replace(/\r/g, '');
const bash = process.env.BASH_PATH || (process.platform === 'win32'
  ? path.join(process.env.ProgramFiles, 'Git/bin/bash.exe') : 'bash');

function git(dir, ...args) {
  const result = spawnSync('git', args, { cwd: dir, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout.trim();
}

// history: [commit subject, tag to put on it or ''].
function repoWith(history) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'release-notes-'));
  git(dir, 'init', '-q');
  git(dir, 'config', 'user.email', 'test@example.com');
  git(dir, 'config', 'user.name', 'test');
  for (const [subject, tag] of history) {
    git(dir, 'commit', '-q', '--allow-empty', '-m', subject);
    if (tag) git(dir, 'tag', '-a', tag, '-m', tag);
  }
  return dir;
}

function notes(dir, tag) {
  const result = spawnSync(bash, ['--noprofile', '--norc', '-eo', 'pipefail', '-c', script], {
    cwd: dir,
    encoding: 'utf8',
    env: { ...process.env, TAG: tag, GITHUB_REPOSITORY: 'owner/repo' },
  });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  const out = fs.readFileSync(path.join(dir, 'release-notes.md'), 'utf8');
  fs.rmSync(dir, { recursive: true, force: true });
  return out;
}

test('notes list the commits since the previous tag, grouped by type', () => {
  const dir = repoWith([
    ['feat: old feature', 'v0.1.0'],
    ['feat: add a thing', ''],
    ['fix(feishu): stop a crash', ''],
    ['docs: explain the thing', 'v0.2.0'],
  ]);
  const out = notes(dir, 'v0.2.0');
  assert.match(out, /## Features\n\n- add a thing \([0-9a-f]+\)\n/);
  assert.match(out, /## Fixes\n\n- stop a crash \([0-9a-f]+\)\n/);
  assert.match(out, /## Other changes\n\n- explain the thing \([0-9a-f]+\)\n/);
  assert.doesNotMatch(out, /old feature/);
  assert.match(out, /compare\/v0\.1\.0\.\.\.v0\.2\.0/);
});

test('a release is compared with the previous release, not a beta', () => {
  const dir = repoWith([
    ['feat: first', 'v0.1.0'],
    ['fix: in the beta', 'v0.2.0-beta.1'],
    ['fix: after the beta', 'v0.2.0'],
  ]);
  const out = notes(dir, 'v0.2.0');
  assert.match(out, /in the beta/);
  assert.match(out, /after the beta/);
  assert.match(out, /compare\/v0\.1\.0\.\.\.v0\.2\.0/);
  assert.doesNotMatch(out, /## Features/);
});

test('a beta is compared with the tag right before it', () => {
  const dir = repoWith([
    ['feat: first', 'v0.1.0'],
    ['fix: in beta 1', 'v0.2.0-beta.1'],
    ['fix: in beta 2', 'v0.2.0-beta.2'],
  ]);
  const out = notes(dir, 'v0.2.0-beta.2');
  assert.match(out, /in beta 2/);
  assert.doesNotMatch(out, /in beta 1/);
});

test('the first tag lists the whole history without a compare link', () => {
  const dir = repoWith([
    ['Initial commit', ''],
    ['feat: first', 'v0.1.0'],
  ]);
  const out = notes(dir, 'v0.1.0');
  assert.match(out, /- first/);
  assert.match(out, /- Initial commit/);
  assert.doesNotMatch(out, /Full Changelog/);
});
