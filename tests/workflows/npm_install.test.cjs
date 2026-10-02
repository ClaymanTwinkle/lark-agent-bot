const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const https = require('node:https');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const { test } = require('node:test');

// The npm postinstall downloader. Every download here goes to local servers;
// made-up *.test hosts are only reachable through the test proxy.
const installJS = path.join(__dirname, '../../npm/install.js');
const installer = require(installJS);
const { VERSION } = installer;

const GITHUB = 'https://github.com/ClaymanTwinkle/lark-agent-bot/releases/download';
const FILE = `lark-agent-bot-${VERSION}-linux-amd64.tar.gz`;
const FAST = { idle: 2000, deadline: 5000 };
const ARCHIVE = Buffer.from('the real archive');
const TAMPERED = Buffer.from('a tampered archive');

const sha256 = (data) => crypto.createHash('sha256').update(data).digest('hex');
const sumsFor = (data) => `${sha256(data)}  ${FILE}\n`;
const basic = (user, pass) => `Basic ${Buffer.from(`${user}:${pass}`).toString('base64')}`;

// quiet silences the installer's progress lines and returns the console.warn
// mock.
function quiet(t) {
  t.mock.method(console, 'log', () => {});
  return t.mock.method(console, 'warn', () => {});
}

// listen starts server on a free local port and stops it, open connections
// included, when the test ends.
async function listen(t, server) {
  const sockets = new Set();
  server.on('connection', (socket) => {
    sockets.add(socket);
    socket.on('close', () => sockets.delete(socket));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => {
    for (const socket of sockets) socket.destroy();
    server.close(() => resolve());
  }));
  return server.address().port;
}

// serve answers the paths in routes (a body, or a handler) and 404s the rest.
async function serve(t, routes) {
  const seen = [];
  const port = await listen(t, http.createServer((req, res) => {
    seen.push(req.url);
    const route = routes[req.url];
    if (typeof route === 'function') return route(req, res);
    if (route === undefined) {
      res.writeHead(404);
      return res.end('not found');
    }
    res.end(route);
  }));
  return { base: `http://127.0.0.1:${port}`, port, seen };
}

// releaseServer serves FILE and/or checksums.txt the way a release does.
function releaseServer(t, { archive, checksums }) {
  const routes = {};
  if (archive) routes[`/${VERSION}/${FILE}`] = archive;
  if (checksums) routes[`/${VERSION}/checksums.txt`] = checksums;
  return serve(t, routes);
}

const fileURL = (server) => `${server.base}/${VERSION}/${FILE}`;

// proxyServer is an HTTP proxy that reaches the hosts in `hosts` (a
// "host" or "host:port" -> local port map) and logs every request. A port of
// 'capture' accepts the CONNECT, keeps the first bytes sent through the
// tunnel and hangs up. With auth set, other credentials get a 407.
async function proxyServer(t, hosts, { auth } = {}) {
  const log = [];
  const server = http.createServer((req, res) => {
    log.push({ method: req.method, url: req.url, auth: req.headers['proxy-authorization'] });
    if (auth && req.headers['proxy-authorization'] !== auth) {
      res.writeHead(407);
      return res.end();
    }
    const target = new URL(req.url);
    const port = hosts[target.host];
    if (!port) {
      res.writeHead(502);
      return res.end();
    }
    const upstream = http.request({
      host: '127.0.0.1', port, path: target.pathname + target.search, headers: req.headers,
    }, (upstreamRes) => {
      res.writeHead(upstreamRes.statusCode, upstreamRes.headers);
      upstreamRes.pipe(res);
    });
    upstream.on('error', () => res.destroy());
    upstream.end();
  });
  server.on('connect', (req, socket) => {
    const entry = { method: req.method, url: req.url, auth: req.headers['proxy-authorization'] };
    log.push(entry);
    socket.on('error', () => {});
    if (auth && entry.auth !== auth) {
      return socket.end('HTTP/1.1 407 Proxy Authentication Required\r\n\r\n');
    }
    const port = hosts[req.url];
    if (!port) return socket.end('HTTP/1.1 502 Bad Gateway\r\n\r\n');
    if (port === 'capture') {
      socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      socket.once('data', (data) => {
        entry.firstBytes = data;
        socket.destroy();
      });
      return;
    }
    const upstream = net.connect(port, '127.0.0.1', () => {
      socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      upstream.pipe(socket);
      socket.pipe(upstream);
    });
    upstream.on('error', () => socket.destroy());
    socket.on('close', () => upstream.destroy());
  });
  const port = await listen(t, server);
  return { url: `http://127.0.0.1:${port}`, port, log };
}

test('a download base puts the mirror first and GitHub second', () => {
  const github = `${GITHUB}/${VERSION}/${FILE}`;
  assert.deepEqual(installer.getDownloadURLs(FILE, {}), [github]);
  assert.deepEqual(
    installer.getDownloadURLs(FILE, { LARK_AGENT_BOT_DOWNLOAD_BASE: 'https://mirror.example/gh/' }),
    [`https://mirror.example/gh/${VERSION}/${FILE}`, github],
  );
  // npm config: --lark-agent-bot-download-base=... or an .npmrc entry.
  assert.deepEqual(
    installer.getDownloadURLs(FILE, { npm_config_lark_agent_bot_download_base: 'https://npm.example' }),
    [`https://npm.example/${VERSION}/${FILE}`, github],
  );
  assert.deepEqual(
    installer.getDownloadURLs(FILE, {
      LARK_AGENT_BOT_DOWNLOAD_BASE: 'https://env.example',
      npm_config_lark_agent_bot_download_base: 'https://npm.example',
    }),
    [`https://env.example/${VERSION}/${FILE}`, github],
  );
  // GitHub is not tried twice.
  assert.deepEqual(installer.getDownloadURLs(FILE, { LARK_AGENT_BOT_DOWNLOAD_BASE: `${GITHUB}/` }), [github]);

  assert.deepEqual(
    installer.getFFmpegURLs('linux-x64', { LARK_AGENT_BOT_FFMPEG_DOWNLOAD_BASE: 'https://mirror.example/ff' }),
    [
      'https://mirror.example/ff/b6.1.1/ffmpeg-linux-x64.gz',
      'https://github.com/eugeneware/ffmpeg-static/releases/download/b6.1.1/ffmpeg-linux-x64.gz',
    ],
  );
});

test('download timeouts default to 30s idle and 600s in all, set in seconds', () => {
  assert.deepEqual(installer.getTimeouts({}), { idle: 30000, deadline: 600000 });
  assert.deepEqual(
    installer.getTimeouts({
      LARK_AGENT_BOT_DOWNLOAD_TIMEOUT: '5',
      npm_config_lark_agent_bot_download_deadline: '90',
    }),
    { idle: 5000, deadline: 90000 },
  );
  assert.deepEqual(installer.getTimeouts({ LARK_AGENT_BOT_DOWNLOAD_TIMEOUT: 'soon' }), { idle: 30000, deadline: 600000 });
});

test('checksums.txt is read in sha256sum format', () => {
  const a = 'a'.repeat(64);
  const sums = installer.parseChecksums(
    `${a}  ${FILE}\r\n${'B'.repeat(64)} *lark-agent-bot-${VERSION}-windows-amd64.zip\n<html>oops</html>\n`,
  );
  assert.deepEqual([...sums], [
    [FILE, a],
    [`lark-agent-bot-${VERSION}-windows-amd64.zip`, 'b'.repeat(64)],
  ]);
  assert.equal(installer.parseChecksums('<!doctype html><title>404</title>').size, 0);
});

test('a source that fails is skipped for the next one', async (t) => {
  quiet(t);
  const mirror = await releaseServer(t, {});
  const github = await releaseServer(t, { archive: ARCHIVE, checksums: sumsFor(ARCHIVE) });
  const data = await installer.downloadRelease(FILE, [fileURL(mirror), fileURL(github)], { env: {}, timeouts: FAST });
  assert.deepEqual(data, ARCHIVE);
  assert.deepEqual(mirror.seen, [`/${VERSION}/${FILE}`]);
});

test('an archive that does not match checksums.txt is never returned', async (t) => {
  quiet(t);
  const opts = { env: {}, timeouts: FAST };
  const mirror = await releaseServer(t, { archive: TAMPERED, checksums: sumsFor(ARCHIVE) });
  const github = await releaseServer(t, { archive: ARCHIVE, checksums: sumsFor(ARCHIVE) });
  // The mirror's copy is turned down and GitHub's is used.
  assert.deepEqual(await installer.downloadRelease(FILE, [fileURL(mirror), fileURL(github)], opts), ARCHIVE);
  // With no good copy anywhere, the download fails.
  await assert.rejects(installer.downloadRelease(FILE, [fileURL(mirror)], opts), /checksum mismatch/);
});

test("a mirror without checksums.txt is checked against GitHub's", async (t) => {
  quiet(t);
  const opts = { env: {}, timeouts: FAST };
  const github = await releaseServer(t, { checksums: sumsFor(ARCHIVE) });
  const good = await releaseServer(t, { archive: ARCHIVE });
  const bad = await releaseServer(t, { archive: TAMPERED });
  assert.deepEqual(await installer.downloadRelease(FILE, [fileURL(good), fileURL(github)], opts), ARCHIVE);
  await assert.rejects(installer.downloadRelease(FILE, [fileURL(bad), fileURL(github)], opts), /checksum mismatch/);
});

test('without any checksums.txt the archive is installed with a warning', async (t) => {
  const warn = quiet(t);
  const mirror = await releaseServer(t, { archive: ARCHIVE });
  const github = await releaseServer(t, {});
  const data = await installer.downloadRelease(FILE, [fileURL(mirror), fileURL(github)], { env: {}, timeouts: FAST });
  assert.deepEqual(data, ARCHIVE);
  const warnings = warn.mock.calls.map((call) => call.arguments.join(' '));
  assert.ok(warnings.some((w) => /no checksums\.txt lists .* unverified/.test(w)), warnings.join('\n'));
});

test('a server or proxy that never answers times out', async (t) => {
  const port = await listen(t, net.createServer(() => {}));
  const timeouts = { idle: 200, deadline: 10000 };
  const started = Date.now();
  await assert.rejects(
    installer.fetchBuffer(`http://127.0.0.1:${port}/file`, { env: {}, timeouts }),
    /timed out: no data for 0\.2s/,
  );
  await assert.rejects(
    installer.fetchBuffer('https://release.test/file', { env: { HTTPS_PROXY: `127.0.0.1:${port}` }, timeouts }),
    /proxy http:\/\/127\.0\.0\.1:\d+: timed out: no data for 0\.2s/,
  );
  assert.ok(Date.now() - started < 5000, `took ${Date.now() - started}ms`);
});

test('a download that only trickles in stops at the deadline', async (t) => {
  const server = await serve(t, {
    '/file': (req, res) => {
      res.writeHead(200, { 'Content-Length': 100000 });
      const tick = setInterval(() => res.write('x'), 20);
      res.on('close', () => clearInterval(tick));
    },
  });
  await assert.rejects(
    installer.fetchBuffer(`${server.base}/file`, { env: {}, timeouts: { idle: 1000, deadline: 300 } }),
    /timed out: not finished after 0\.3s/,
  );
});

test('the proxy comes from npm config first, then the environment', () => {
  const all = {
    npm_config_https_proxy: 'http://a:1',
    npm_config_proxy: 'http://b:2',
    HTTPS_PROXY: 'http://c:3',
    HTTP_PROXY: 'http://d:4',
  };
  const pick = installer.getProxyForURL;
  assert.equal(pick('https://github.com/x', all), 'http://a:1');
  assert.equal(pick('https://github.com/x', { ...all, npm_config_https_proxy: '' }), 'http://b:2');
  assert.equal(pick('https://github.com/x', { HTTPS_PROXY: 'http://c:3', HTTP_PROXY: 'http://d:4' }), 'http://c:3');
  assert.equal(pick('https://github.com/x', { https_proxy: 'http://c:3' }), 'http://c:3');
  // As in npm, HTTP_PROXY covers https too.
  assert.equal(pick('https://github.com/x', { HTTP_PROXY: 'http://d:4' }), 'http://d:4');
  // An https proxy setting is not used for http.
  assert.equal(pick('http://mirror.example/x', all), 'http://b:2');
  assert.equal(pick('http://mirror.example/x', { HTTPS_PROXY: 'http://c:3' }), null);
  assert.equal(pick('https://github.com/x', {}), null);
  assert.equal(pick('https://github.com/x', { HTTPS_PROXY: 'http://c:3', NO_PROXY: 'github.com' }), null);
  assert.equal(pick('https://github.com/x', { HTTPS_PROXY: 'http://c:3', no_proxy: 'github.com' }), null);
  assert.equal(pick('https://github.com/x', { HTTPS_PROXY: 'http://c:3', npm_config_noproxy: 'a.example,github.com' }), null);
});

test('NO_PROXY entries match hosts, domains, ports and IPs', () => {
  const bypass = installer.shouldBypassProxy;
  const objects = 'https://objects.githubusercontent.com/x';
  assert.ok(bypass(objects, 'githubusercontent.com'));
  assert.ok(bypass(objects, '.githubusercontent.com'));
  assert.ok(bypass(objects, '*.githubusercontent.com'));
  assert.ok(bypass('https://GitHub.com/x', 'localhost, github.com'));
  assert.ok(bypass('https://github.com/x', '*'));
  assert.ok(bypass('https://github.com/x', 'github.com:443'));
  assert.ok(!bypass('https://github.com/x', 'github.com:8443'));
  assert.ok(bypass('http://127.0.0.1:8080/x', '127.0.0.1'));
  assert.ok(bypass('http://[::1]:8080/x', '::1'));
  assert.ok(bypass('http://[::1]:8080/x', '[::1]:8080'));
  assert.ok(!bypass('https://notgithub.com/x', 'github.com'));
  assert.ok(!bypass('http://10.0.0.1/x', '0.0.1'));
  assert.ok(!bypass('https://github.com/x', ''));
});

test('plain http downloads and their redirects go through the proxy', async (t) => {
  const origin = await serve(t, {
    '/dl/file': (req, res) => {
      res.writeHead(302, { Location: 'http://objects.test/blob?sig=1' });
      res.end();
    },
    '/blob?sig=1': 'payload',
  });
  const auth = basic('user', 'p@ss');
  const proxy = await proxyServer(t, { 'release.test': origin.port, 'objects.test': origin.port }, { auth });
  const env = { http_proxy: `http://user:p%40ss@127.0.0.1:${proxy.port}` };
  const data = await installer.fetchBuffer('http://release.test/dl/file', { env, timeouts: FAST });
  assert.equal(data.toString(), 'payload');
  assert.deepEqual(proxy.log, [
    { method: 'GET', url: 'http://release.test/dl/file', auth },
    { method: 'GET', url: 'http://objects.test/blob?sig=1', auth },
  ]);
});

test('https downloads tunnel through CONNECT with the proxy credentials and SNI', async (t) => {
  const auth = basic('user', 'p@ss');
  const proxy = await proxyServer(t, { 'release.test:443': 'capture' }, { auth });
  const url = 'https://release.test/dl/file';
  const viaProxy = (userinfo) => ({ env: { HTTPS_PROXY: `http://${userinfo}@127.0.0.1:${proxy.port}` }, timeouts: FAST });

  await assert.rejects(installer.fetchBuffer(url, viaProxy('user:p%40ss')));
  const [connect] = proxy.log;
  assert.equal(connect.method, 'CONNECT');
  assert.equal(connect.url, 'release.test:443');
  assert.equal(connect.auth, auth);
  assert.equal(connect.firstBytes[0], 0x16, 'TLS starts inside the tunnel');
  assert.ok(connect.firstBytes.includes('release.test'), 'with the target host as SNI');

  await assert.rejects(
    installer.fetchBuffer(url, viaProxy('user:wrong')),
    /proxy http:\/\/127\.0\.0\.1:\d+: CONNECT release\.test:443 answered HTTP 407/,
  );
});

function findOpenssl() {
  const candidates = ['openssl'];
  if (process.platform === 'win32') {
    const git = path.join(process.env.ProgramFiles || 'C:\\Program Files', 'Git');
    candidates.push(path.join(git, 'usr/bin/openssl.exe'), path.join(git, 'mingw64/bin/openssl.exe'));
  }
  return candidates.find((bin) => spawnSync(bin, ['version']).status === 0);
}

// envWithout returns process.env without proxy and installer settings.
function envWithout() {
  return Object.fromEntries(Object.entries(process.env).filter(
    ([key]) => !/proxy|^lark_agent_bot_|^npm_config_lark_agent_bot_/i.test(key),
  ));
}

function run(cmd, args, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { env });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (data) => { stdout += data; });
    child.stderr.on('data', (data) => { stderr += data; });
    child.on('error', reject);
    child.on('close', (code) => resolve({ code, stdout, stderr }));
  });
}

const openssl = findOpenssl();

test('https downloads and their redirects work through a CONNECT proxy',
  { skip: openssl ? false : 'needs openssl to make a certificate' }, async (t) => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'npm-install-test-'));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    const keyPath = path.join(dir, 'key.pem');
    const certPath = path.join(dir, 'cert.pem');
    const made = spawnSync(openssl, [
      'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
      '-keyout', keyPath, '-out', certPath, '-subj', '/CN=release.test',
      '-addext', 'subjectAltName=DNS:release.test,DNS:objects.test',
    ], { encoding: 'utf8' });
    assert.equal(made.status, 0, made.stderr);

    const originPort = await listen(t, https.createServer({
      key: fs.readFileSync(keyPath), cert: fs.readFileSync(certPath),
    }, (req, res) => {
      if (req.headers.host === 'release.test' && req.url === '/dl/file') {
        res.writeHead(302, { Location: 'https://objects.test/blob' });
        return res.end();
      }
      if (req.headers.host === 'objects.test' && req.url === '/blob') return res.end('payload');
      res.writeHead(404);
      res.end();
    }));
    const proxy = await proxyServer(t, { 'release.test:443': originPort, 'objects.test:443': originPort });

    // NODE_EXTRA_CA_CERTS is only read at startup, so a child process that
    // trusts the test certificate does the download, with the proxy from its
    // environment.
    const script = `require(${JSON.stringify(installJS)}).fetchBuffer(process.argv[1]).then(
      (data) => process.stdout.write(data),
      (err) => { console.error(err.message); process.exitCode = 1; })`;
    const result = await run(process.execPath, ['-e', script, 'https://release.test/dl/file'], {
      ...envWithout(),
      NODE_EXTRA_CA_CERTS: certPath,
      HTTPS_PROXY: proxy.url,
      LARK_AGENT_BOT_DOWNLOAD_TIMEOUT: '5',
      LARK_AGENT_BOT_DOWNLOAD_DEADLINE: '20',
    });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(result.stdout, 'payload');
    assert.deepEqual(proxy.log.map((entry) => `${entry.method} ${entry.url}`), [
      'CONNECT release.test:443',
      'CONNECT objects.test:443',
    ]);
  });

test('NO_PROXY hosts are fetched without the proxy', async (t) => {
  const origin = await serve(t, { '/file': 'direct' });
  const proxy = await proxyServer(t, {});
  const env = { HTTP_PROXY: proxy.url };
  await assert.rejects(installer.fetchBuffer(`${origin.base}/file`, { env, timeouts: FAST }), /HTTP 502/);
  assert.equal(proxy.log.length, 1);

  const data = await installer.fetchBuffer(`${origin.base}/file`, {
    env: { ...env, NO_PROXY: 'localhost,127.0.0.1' }, timeouts: FAST,
  });
  assert.equal(data.toString(), 'direct');
  assert.equal(proxy.log.length, 1);
});
