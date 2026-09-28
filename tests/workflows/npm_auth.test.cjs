const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { test } = require('node:test');

const workflow = fs.readFileSync(
  path.join(__dirname, '../../.github/workflows/release.yml'), 'utf8',
);
// Run the actual publish step against a shell stub; never contact npm.
const step = workflow.split('      - name: Publish to npm')[1];
assert.ok(step, 'npm publishing step must exist');
const block = step.match(/        run: \|\r?\n((?:          .*\r?\n|\r?\n)+)/);
assert.ok(block, 'npm publishing step must contain a shell script');
const script = block[1].replace(/^          /gm, '').replace(/\r/g, '');
const bash = process.env.BASH_PATH || (process.platform === 'win32'
  ? path.join(process.env.ProgramFiles, 'Git/bin/bash.exe') : 'bash');

function publish(token, { existing = false, fail = false } = {}) {
  return spawnSync(bash, ['--noprofile', '--norc', '-e', '-c', `
npm() {
  case "$1" in
    version) echo 'VERSION_CALLED' ;;
    view) return ${existing ? 0 : 1} ;;
    publish) echo "PUBLISH_CALLED $*"; return ${fail ? 1 : 0} ;;
    *) echo 'Unexpected npm command' >&2; return 99 ;;
  esac
}
${script}`], {
    encoding: 'utf8',
    env: { ...process.env, NODE_AUTH_TOKEN: token, TAG: 'v0.1.0' },
  });
}

test('missing NPM_TOKEN skips all npm commands', () => {
  const result = publish('');
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /NPM_TOKEN.*skipping npm publish/);
  assert.doesNotMatch(result.stdout, /VERSION_CALLED|PUBLISH_CALLED/);
});

test('setup-node placeholder must not publish when NPM_TOKEN is missing', () => {
  const result = publish('XXXXX-XXXXX-XXXXX-XXXXX');
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /NPM_TOKEN.*skipping npm publish/);
  assert.doesNotMatch(result.stdout, /VERSION_CALLED|PUBLISH_CALLED/);
});

test('configured token publishes with public access and provenance', () => {
  const result = publish('test-token');
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /PUBLISH_CALLED publish --access public --provenance --tag latest/);
});

test('existing npm version is not republished', () => {
  const result = publish('test-token', { existing: true });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.doesNotMatch(result.stdout, /PUBLISH_CALLED/);
});

test('npm publish failure fails the step', () => {
  const result = publish('test-token', { fail: true });
  assert.ifError(result.error);
  assert.equal(result.status, 1, result.stderr);
});
