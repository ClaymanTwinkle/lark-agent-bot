#!/usr/bin/env node

"use strict";

const { execFileSync, execSync } = require("child_process");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const http = require("http");
const net = require("net");
const tls = require("tls");
const zlib = require("zlib");

const PACKAGE = require("./package.json");
const VERSION = `v${PACKAGE.version}`;
const NAME = "lark-agent-bot";
const USER_AGENT = "lark-agent-bot-npm";

const GITHUB_REPO = "ClaymanTwinkle/lark-agent-bot";
// LARK_AGENT_BOT_DOWNLOAD_BASE replaces this prefix, so users who can't
// reach GitHub can point at a mirror.
const RELEASES_BASE = `https://github.com/${GITHUB_REPO}/releases/download`;

// A source that sends nothing for this long is given up on, and so is a
// download (redirects included) that takes longer than the deadline.
// LARK_AGENT_BOT_DOWNLOAD_TIMEOUT and LARK_AGENT_BOT_DOWNLOAD_DEADLINE
// override them, in seconds.
const DEFAULT_IDLE_TIMEOUT_S = 30;
const DEFAULT_DEADLINE_S = 600;
const MAX_REDIRECTS = 5;

// Voice messages and video covers need ffmpeg. When it isn't on PATH, a
// static build from ffmpeg-static goes to ~/.lark-agent-bot/bin, where
// lark-agent-bot also looks. That directory is outside the npm package, so
// upgrades (which replace the package directory) keep it. Set
// LARK_AGENT_BOT_SKIP_FFMPEG=1 to skip.
const FFMPEG_REPO = "eugeneware/ffmpeg-static";
const FFMPEG_RELEASE = "b6.1.1";
// LARK_AGENT_BOT_FFMPEG_DOWNLOAD_BASE replaces this prefix.
const FFMPEG_BASE = `https://github.com/${FFMPEG_REPO}/releases/download`;
// SHA-256 of each ffmpeg-<platform>-<arch>.gz asset in FFMPEG_RELEASE.
const FFMPEG_SHA256 = {
  "darwin-arm64": "8923876afa8db5585022d7860ec7e589af192f441c56793971276d450ed3bbfa",
  "darwin-x64": "929b375c1182d956c51f7ac25e0b2b0411fb01f6f407aa15c9758efeb4242106",
  "linux-arm64": "754a678672298bc68156adff58aa7385a592c2b30b1d0ae8750c45c915c4bac0",
  "linux-x64": "bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa",
  "win32-x64": "8883a3dffbd0a16cf4ef95206ea05283f78908dbfb118f73c83f4951dcc06d77",
};

const PLATFORM_MAP = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

function getPlatformInfo() {
  const platform = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!platform || !arch) {
    throw new Error(
      `Unsupported platform: ${process.platform}/${process.arch}. ` +
        `Supported: linux/darwin/windows x64/arm64`
    );
  }
  const ext = platform === "windows" ? ".zip" : ".tar.gz";
  const filename = `${NAME}-${VERSION}-${platform}-${arch}${ext}`;
  return { platform, arch, ext, filename };
}

// setting reads LARK_AGENT_BOT_<NAME>, or else the npm config key of the
// same name: npm hands `--lark-agent-bot-<name>=...` and .npmrc entries to
// install scripts as npm_config_lark_agent_bot_<name>.
function setting(env, name) {
  const key = `lark_agent_bot_${name.toLowerCase()}`;
  return (
    env[`LARK_AGENT_BOT_${name}`] ||
    env[`npm_config_${key}`] ||
    env[`npm_config_${key.replace(/_/g, "-")}`] ||
    ""
  ).trim();
}

// downloadBases puts the custom base, when there is one, before the default.
function downloadBases(custom, fallback) {
  const base = custom.replace(/\/+$/, "");
  return base && base !== fallback ? [base, fallback] : [fallback];
}

// getDownloadURLs lists the URLs of release asset filename in the order to
// try them.
function getDownloadURLs(filename, env = process.env) {
  return downloadBases(setting(env, "DOWNLOAD_BASE"), RELEASES_BASE).map(
    (base) => `${base}/${VERSION}/${filename}`
  );
}

// getFFmpegURLs lists the URLs of the ffmpeg build for key
// ("<platform>-<arch>") in the order to try them.
function getFFmpegURLs(key, env = process.env) {
  return downloadBases(setting(env, "FFMPEG_DOWNLOAD_BASE"), FFMPEG_BASE).map(
    (base) => `${base}/${FFMPEG_RELEASE}/ffmpeg-${key}.gz`
  );
}

// getTimeouts returns the idle timeout and the deadline of one download, in
// milliseconds.
function getTimeouts(env = process.env) {
  const ms = (name, fallback) => {
    const seconds = Number(setting(env, name));
    return (Number.isFinite(seconds) && seconds > 0 ? seconds : fallback) * 1000;
  };
  return {
    idle: ms("DOWNLOAD_TIMEOUT", DEFAULT_IDLE_TIMEOUT_S),
    deadline: ms("DOWNLOAD_DEADLINE", DEFAULT_DEADLINE_S),
  };
}

function firstSet(env, keys) {
  for (const key of keys) {
    const value = (env[key] || "").trim();
    if (value && value !== "null" && value !== "false") return value;
  }
  return "";
}

// getProxyForURL returns the proxy to fetch url through, or null. Like npm,
// it takes npm's https-proxy / proxy config first, then HTTPS_PROXY and
// HTTP_PROXY, and skips the hosts that NO_PROXY (or npm's noproxy) lists.
function getProxyForURL(url, env = process.env) {
  const keys =
    new URL(url).protocol === "https:"
      ? ["npm_config_https_proxy", "npm_config_proxy", "https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"]
      : ["npm_config_proxy", "http_proxy", "HTTP_PROXY"];
  const proxy = firstSet(env, keys);
  const noProxy = firstSet(env, ["npm_config_noproxy", "no_proxy", "NO_PROXY"]);
  if (!proxy || shouldBypassProxy(url, noProxy)) return null;
  return proxy;
}

// shouldBypassProxy reports whether noProxy, a NO_PROXY list (hosts,
// domains as "example.com", ".example.com" or "*.example.com", each with an
// optional ":port", or "*"), covers url's host.
function shouldBypassProxy(url, noProxy) {
  if (!noProxy) return false;
  const u = new URL(url);
  const host = u.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  const port = u.port || (u.protocol === "https:" ? "443" : "80");
  return noProxy.split(/[\s,]+/).some((raw) => {
    const entry = raw.toLowerCase();
    if (!entry) return false;
    if (entry === "*") return true;
    let entryHost = entry;
    let entryPort = "";
    const m = entry.match(/^\[([^\]]+)\](?::(\d+))?$/) || (!net.isIP(entry) && entry.match(/^([^:]+):(\d+)$/));
    if (m) {
      entryHost = m[1];
      entryPort = m[2] || "";
    }
    if (entryPort && entryPort !== port) return false;
    entryHost = entryHost.replace(/^\*?\./, "");
    return host === entryHost || (!net.isIP(host) && host.endsWith(`.${entryHost}`));
  });
}

// parseProxy turns a proxy setting ("http://user:pass@host:port", or just
// "host:port") into what a request to it needs.
function parseProxy(value) {
  const u = new URL(/^[a-z][a-z\d+.-]*:\/\//i.test(value) ? value : `http://${value}`);
  if (u.protocol !== "http:" && u.protocol !== "https:") {
    throw new Error(`proxy ${u.protocol}//${u.host} is not supported, use an http:// or https:// proxy`);
  }
  const headers = {};
  if (u.username || u.password) {
    const auth = `${decodeURIComponent(u.username)}:${decodeURIComponent(u.password)}`;
    headers["Proxy-Authorization"] = `Basic ${Buffer.from(auth).toString("base64")}`;
  }
  return {
    mod: u.protocol === "https:" ? https : http,
    host: u.hostname.replace(/^\[|\]$/g, ""),
    port: Number(u.port) || (u.protocol === "https:" ? 443 : 80),
    headers,
    label: `${u.protocol}//${u.host}`, // without the credentials, for messages
  };
}

// watchIdle calls onIdle once socket has been idle, connecting included, for
// ms, and returns a function that stops watching.
function watchIdle(socket, ms, onIdle) {
  const fire = () => onIdle(new Error(`timed out: no data for ${ms / 1000}s`));
  socket.setTimeout(ms);
  socket.once("timeout", fire);
  return () => {
    socket.setTimeout(0);
    socket.removeListener("timeout", fire);
  };
}

// sendGet sends a GET for target, directly or through the proxy env sets for
// it, and calls onResponse with the response or onError on failure. It
// passes every request, response and socket it opens to track.
function sendGet(target, { env, idle, track, onResponse, onError }) {
  const u = new URL(target);
  if (u.protocol !== "http:" && u.protocol !== "https:") {
    throw new Error(`only http and https URLs can be downloaded, not ${target}`);
  }
  const hostname = u.hostname.replace(/^\[|\]$/g, "");
  const headers = { "User-Agent": USER_AGENT, Host: u.host };
  const send = (mod, options) => {
    const req = track(mod.request(options, onResponse));
    req.on("socket", (socket) =>
      watchIdle(socket, idle, (err) => {
        onError(err);
        req.destroy();
      })
    );
    req.on("error", onError);
    req.end();
  };

  const proxyURL = getProxyForURL(target, env);
  if (!proxyURL) {
    return send(u.protocol === "https:" ? https : http, {
      host: hostname,
      port: Number(u.port) || (u.protocol === "https:" ? 443 : 80),
      path: u.pathname + u.search,
      headers,
      agent: false,
    });
  }

  const proxy = parseProxy(proxyURL);
  const proxyError = (err) => onError(new Error(`proxy ${proxy.label}: ${err.message}`));
  if (u.protocol === "http:") {
    // Plain HTTP goes to the proxy as a request for the absolute URL.
    return send(proxy.mod, {
      host: proxy.host,
      port: proxy.port,
      path: u.href,
      headers: { ...headers, ...proxy.headers },
      agent: false,
    });
  }

  // HTTPS goes through a CONNECT tunnel, with TLS to the target inside it.
  const port = Number(u.port) || 443;
  const authority = `${u.hostname}:${port}`;
  const connectReq = track(
    proxy.mod.request({
      host: proxy.host,
      port: proxy.port,
      method: "CONNECT",
      path: authority,
      headers: { Host: authority, "User-Agent": USER_AGENT, ...proxy.headers },
      agent: false,
    })
  );
  let unwatch = () => {};
  connectReq.on("socket", (socket) => {
    unwatch = watchIdle(socket, idle, (err) => {
      proxyError(err);
      connectReq.destroy();
    });
  });
  connectReq.on("error", proxyError);
  connectReq.on("connect", (res, socket) => {
    // From here on the TLS socket inside the tunnel is watched instead.
    unwatch();
    track(socket);
    socket.on("error", onError);
    if (res.statusCode < 200 || res.statusCode >= 300) {
      socket.destroy();
      return proxyError(new Error(`CONNECT ${authority} answered HTTP ${res.statusCode}`));
    }
    // Without an agent, https.request uses createConnection's socket.
    send(https, {
      host: hostname,
      port,
      path: u.pathname + u.search,
      headers,
      createConnection: () =>
        tls.connect({ socket, host: hostname, servername: net.isIP(hostname) ? undefined : hostname }),
    });
  });
  connectReq.end();
}

// fetchBuffer downloads url into a Buffer, following redirects. Each hop
// goes through the proxy env sets for its host. It fails once the transfer
// has been idle for timeouts.idle ms, or when the whole download, redirects
// included, takes longer than timeouts.deadline ms.
function fetchBuffer(url, { env = process.env, timeouts = getTimeouts(env) } = {}) {
  return new Promise((resolve, reject) => {
    // Everything still open, destroyed when the download fails.
    const open = new Set();
    const track = (stream) => {
      open.add(stream);
      stream.once("close", () => open.delete(stream));
      return stream;
    };
    let settled = false;
    const finish = (err, data) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (!err) return resolve(data);
      for (const stream of open) stream.destroy();
      reject(err);
    };
    const timer = setTimeout(
      () => finish(new Error(`timed out: not finished after ${timeouts.deadline / 1000}s`)),
      timeouts.deadline
    );

    let hops = 0;
    const get = (target, redirectsLeft) => {
      const hop = ++hops;
      // Errors from a hop already redirected away from don't matter.
      const fail = (err) => {
        if (hop === hops) finish(err);
      };
      const onResponse = (res) => {
        track(res);
        res.on("error", fail);
        const { statusCode: status, headers } = res;
        if (status >= 300 && status < 400 && headers.location) {
          res.resume();
          if (redirectsLeft === 0) return fail(new Error("too many redirects"));
          let next;
          try {
            next = new URL(headers.location, target).href;
          } catch {
            return fail(new Error(`bad redirect to ${headers.location}`));
          }
          return get(next, redirectsLeft - 1);
        }
        if (status !== 200) {
          res.resume();
          return fail(new Error(`HTTP ${status} from ${new URL(target).host}`));
        }
        const chunks = [];
        const cut = () => fail(new Error("connection closed before the download finished"));
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => (res.complete ? finish(null, Buffer.concat(chunks)) : cut()));
        res.on("close", cut);
      };
      try {
        sendGet(target, { env, idle: timeouts.idle, track, onResponse, onError: fail });
      } catch (err) {
        fail(err);
      }
    };
    get(url, MAX_REDIRECTS);
  });
}

function sha256Hex(data) {
  return crypto.createHash("sha256").update(data).digest("hex");
}

// parseChecksums reads sha256sum output ("<hex>  <filename>" lines, as in a
// release's checksums.txt) into a map from filename to lowercase hex.
function parseChecksums(text) {
  const sums = new Map();
  for (const line of text.split(/\r?\n/)) {
    const m = line.match(/^([0-9a-f]{64})\s+\*?(\S.*?)\s*$/i);
    if (m) sums.set(m[2], m[1].toLowerCase());
  }
  return sums;
}

function proxyNote(url, env) {
  try {
    const proxy = getProxyForURL(url, env);
    return proxy ? ` via proxy ${parseProxy(proxy).label}` : "";
  } catch {
    return ""; // fetchBuffer reports the bad setting
  }
}

// downloadFirst downloads from each of urls in turn and returns the first
// download that check accepts; check throws to reject one. The error lists
// why each URL failed.
async function downloadFirst(urls, check, opts = {}) {
  const failures = [];
  for (const url of urls) {
    console.log(`[lark-agent-bot] Downloading ${url}${proxyNote(url, opts.env || process.env)}`);
    try {
      const data = await fetchBuffer(url, opts);
      console.log(`[lark-agent-bot] Downloaded ${(data.length / 1024 / 1024).toFixed(1)} MB`);
      await check(data, url);
      return data;
    } catch (err) {
      console.warn(`[lark-agent-bot] Failed: ${err.message}`);
      failures.push(`  ${url}: ${err.message}`);
    }
  }
  throw new Error(failures.join("\n"));
}

// downloadRelease downloads release asset filename from the first of urls
// that works and checks its SHA-256 against checksums.txt: the one next to
// it, or when that one is missing or doesn't list filename, the ones next
// to the other URLs (a mirror may not copy checksums.txt). A download that
// doesn't match is never returned. When no checksums.txt can be had, the
// download is returned unverified, with a warning.
async function downloadRelease(filename, urls, opts = {}) {
  const lists = new Map(); // checksums.txt URL -> Promise of its parsed list, or null
  const checksums = (url) => {
    if (!lists.has(url)) {
      lists.set(
        url,
        fetchBuffer(url, opts).then(
          (data) => parseChecksums(data.toString("utf8")),
          (err) => {
            console.warn(`[lark-agent-bot] Could not get ${url}: ${err.message}`);
            return null;
          }
        )
      );
    }
    return lists.get(url);
  };
  const nextTo = (url) => `${url.slice(0, url.lastIndexOf("/") + 1)}checksums.txt`;

  return downloadFirst(
    urls,
    async (data, url) => {
      for (const sumsURL of [url, ...urls.filter((u) => u !== url)].map(nextTo)) {
        const want = ((await checksums(sumsURL)) || new Map()).get(filename);
        if (!want) continue;
        const got = sha256Hex(data);
        if (got !== want) {
          throw new Error(`checksum mismatch: the download's SHA-256 is ${got}, but ${sumsURL} lists ${want}`);
        }
        console.log(`[lark-agent-bot] SHA-256 matches ${sumsURL}`);
        return;
      }
      console.warn(`[lark-agent-bot] Warning: no checksums.txt lists ${filename}, installing it unverified.`);
    },
    opts
  );
}

function extractTarGz(buffer, destDir, binaryName) {
  const tmpFile = path.join(destDir, "_tmp.tar.gz");
  fs.writeFileSync(tmpFile, buffer);
  try {
    execSync(`tar xzf "${tmpFile}" -C "${destDir}"`, { stdio: "pipe" });
  } finally {
    fs.unlinkSync(tmpFile);
  }
  const extracted = fs.readdirSync(destDir).find((f) => f.startsWith(NAME) && !f.endsWith(".tar.gz"));
  if (extracted && extracted !== binaryName) {
    fs.renameSync(path.join(destDir, extracted), path.join(destDir, binaryName));
  }
}

function extractZip(buffer, destDir, binaryName) {
  const tmpFile = path.join(destDir, "_tmp.zip");
  fs.writeFileSync(tmpFile, buffer);
  try {
    try {
      execSync(`unzip -o "${tmpFile}" -d "${destDir}"`, { stdio: "pipe" });
    } catch {
      execSync(`powershell -Command "Expand-Archive -Force '${tmpFile}' '${destDir}'"`, {
        stdio: "pipe",
      });
    }
  } finally {
    try { fs.unlinkSync(tmpFile); } catch {}
  }
  const extracted = fs.readdirSync(destDir).find((f) => f.startsWith(NAME) && f.endsWith(".exe"));
  if (extracted && extracted !== binaryName) {
    fs.renameSync(path.join(destDir, extracted), path.join(destDir, binaryName));
  }
}

// parseVersion splits "1.2.3-beta.1" into { nums: [1,2,3], preTag: "beta", preNum: 1 }
function parseVersion(v) {
  v = v.replace(/^v/, "").trim();
  const [base, ...rest] = v.split("-");
  const nums = base.split(".").map(Number);
  const pre = rest.join("-");
  const m = pre.match(/^([a-zA-Z]+)\.?(\d+)?$/);
  return { nums, preTag: m ? m[1] : pre, preNum: m && m[2] ? parseInt(m[2], 10) : 0, hasPre: pre !== "" };
}

// isNewerOrEqual returns true if installed >= expected
function isNewerOrEqual(installed, expected) {
  const a = parseVersion(installed);
  const b = parseVersion(expected);
  const len = Math.max(a.nums.length, b.nums.length);
  for (let i = 0; i < len; i++) {
    const av = a.nums[i] || 0;
    const bv = b.nums[i] || 0;
    if (av > bv) return true;
    if (av < bv) return false;
  }
  if (!a.hasPre && b.hasPre) return true;
  if (a.hasPre && !b.hasPre) return false;
  if (!a.hasPre && !b.hasPre) return true;
  // Both pre-release: compare tag then number (rc > beta, beta.10 > beta.9)
  if (a.preTag !== b.preTag) return a.preTag > b.preTag;
  return a.preNum >= b.preNum;
}

function hasFFmpegOnPath() {
  try {
    execFileSync("ffmpeg", ["-version"], { stdio: "ignore", timeout: 10000 });
    return true;
  } catch {
    return false;
  }
}

function ffmpegToolDir() {
  return path.join(os.homedir(), ".lark-agent-bot", "bin");
}

function ffmpegHint(toolDir) {
  return (
    "[lark-agent-bot] Voice messages and video covers need ffmpeg. Install it yourself:\n" +
    "  Windows: winget install Gyan.FFmpeg\n" +
    "  macOS:   brew install ffmpeg\n" +
    "  Linux:   sudo apt install ffmpeg (or your distro's package manager)\n" +
    `  or put an ffmpeg executable in ${toolDir}`
  );
}

// installFFmpeg never fails the install: without ffmpeg lark-agent-bot still
// works, it only loses voice conversion and video covers.
async function installFFmpeg(toolDir) {
  if (process.env.LARK_AGENT_BOT_SKIP_FFMPEG) {
    console.log("[lark-agent-bot] LARK_AGENT_BOT_SKIP_FFMPEG is set, skipping ffmpeg.");
    return;
  }
  const ffmpegPath = path.join(toolDir, process.platform === "win32" ? "ffmpeg.exe" : "ffmpeg");
  if (fs.existsSync(ffmpegPath)) {
    console.log(`[lark-agent-bot] ffmpeg already at ${ffmpegPath}, skipping.`);
    return;
  }
  if (hasFFmpegOnPath()) {
    console.log("[lark-agent-bot] ffmpeg found on PATH, skipping.");
    return;
  }

  const key = `${process.platform}-${process.arch}`;
  const sha256 = FFMPEG_SHA256[key];
  if (!sha256) {
    console.warn(`[lark-agent-bot] No prebuilt ffmpeg for ${key}.`);
    console.warn(ffmpegHint(toolDir));
    return;
  }

  const tmpPath = `${ffmpegPath}.download`;
  try {
    console.log("[lark-agent-bot] ffmpeg not found, downloading a static build.");
    const gz = await downloadFirst(getFFmpegURLs(key), (data) => {
      const actual = sha256Hex(data);
      if (actual !== sha256) {
        throw new Error(`checksum mismatch: got ${actual}, want ${sha256}`);
      }
    });
    fs.mkdirSync(toolDir, { recursive: true });
    fs.writeFileSync(tmpPath, zlib.gunzipSync(gz), { mode: 0o755 });
    fs.renameSync(tmpPath, ffmpegPath);
    if (process.platform === "darwin") {
      try {
        execSync(`xattr -d com.apple.quarantine "${ffmpegPath}"`, { stdio: "pipe" });
      } catch {
        // xattr fails if the attribute doesn't exist, which is fine
      }
    }
    console.log(`[lark-agent-bot] Installed ffmpeg to ${ffmpegPath}`);
  } catch (err) {
    try { fs.unlinkSync(tmpPath); } catch {}
    console.warn(`[lark-agent-bot] Could not install ffmpeg:\n${err.message}`);
    console.warn(ffmpegHint(toolDir));
  }
}

async function main() {
  const binDir = path.join(__dirname, "bin");
  await installBinary(binDir);
  await installFFmpeg(ffmpegToolDir());
}

async function installBinary(binDir) {
  const { platform, arch, ext, filename } = getPlatformInfo();
  console.log(`[lark-agent-bot] Platform: ${platform}/${arch}`);

  fs.mkdirSync(binDir, { recursive: true });

  const binaryName = platform === "windows" ? `${NAME}.exe` : NAME;
  const binaryPath = path.join(binDir, binaryName);

  // The old binary is removed only once the new one has downloaded, so a
  // failed download leaves it in place.
  let replace = false;
  if (fs.existsSync(binaryPath)) {
    try {
      const out = execSync(`"${binaryPath}" --version`, { encoding: "utf8", timeout: 5000 });
      const expectedVer = VERSION.slice(1); // remove leading "v"
      if (out.includes(expectedVer)) {
        console.log(`[lark-agent-bot] Binary ${VERSION} already installed, skipping.`);
        return;
      }
      // Don't downgrade: if existing binary is newer, keep it
      const match = out.match(/(\d+\.\d+\.\d+[^\s]*)/);
      if (match && isNewerOrEqual(match[1], expectedVer)) {
        console.log(`[lark-agent-bot] Binary ${match[1]} is newer than ${VERSION}, skipping.`);
        return;
      }
      console.log(`[lark-agent-bot] Existing binary is outdated, upgrading to ${VERSION}...`);
    } catch {
      console.log(`[lark-agent-bot] Replacing existing binary with ${VERSION}...`);
    }
    replace = true;
  }

  const urls = getDownloadURLs(filename);
  let data;
  try {
    data = await downloadRelease(filename, urls);
  } catch (err) {
    throw new Error(
      `[lark-agent-bot] Could not download ${filename}:\n${err.message}\n` +
        `[lark-agent-bot] Download it yourself from\n  ${urls[urls.length - 1]}\n` +
        `  and extract ${binaryName} from it into ${binDir}\n` +
        "  Behind a proxy? Set HTTPS_PROXY. GitHub slow or blocked? Point\n" +
        "  LARK_AGENT_BOT_DOWNLOAD_BASE at a mirror, or raise LARK_AGENT_BOT_DOWNLOAD_TIMEOUT /\n" +
        "  LARK_AGENT_BOT_DOWNLOAD_DEADLINE (seconds)."
    );
  }

  if (replace) {
    fs.unlinkSync(binaryPath);
  }
  if (ext === ".tar.gz") {
    extractTarGz(data, binDir, binaryName);
  } else {
    extractZip(data, binDir, binaryName);
  }

  if (platform !== "windows") {
    fs.chmodSync(binaryPath, 0o755);
  }

  if (platform === "darwin") {
    try {
      execSync(`xattr -d com.apple.quarantine "${binaryPath}"`, { stdio: "pipe" });
      console.log(`[lark-agent-bot] Removed macOS quarantine attribute`);
    } catch {
      // xattr fails if the attribute doesn't exist, which is fine
    }
  }

  console.log(`[lark-agent-bot] Installed to ${binaryPath}`);
}

if (require.main === module) {
  main().catch((err) => {
    console.error(err.message);
    console.error(
      "[lark-agent-bot] Installation failed. You can install manually:\n" +
        `  https://github.com/${GITHUB_REPO}/releases/tag/${VERSION}`
    );
    process.exit(1);
  });
}

// For tests.
module.exports = {
  VERSION,
  downloadRelease,
  fetchBuffer,
  getDownloadURLs,
  getFFmpegURLs,
  getProxyForURL,
  getTimeouts,
  installFFmpeg,
  parseChecksums,
  shouldBypassProxy,
};
