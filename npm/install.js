#!/usr/bin/env node

"use strict";

const { execFileSync, execSync } = require("child_process");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const http = require("http");
const zlib = require("zlib");

const PACKAGE = require("./package.json");
const VERSION = `v${PACKAGE.version}`;
const NAME = "lark-agent-bot";

const GITHUB_REPO = "ClaymanTwinkle/lark-agent-bot";

// Voice messages and video covers need ffmpeg. When it isn't on PATH, a
// static build from ffmpeg-static goes to ~/.lark-agent-bot/bin, where
// lark-agent-bot also looks. That directory is outside the npm package, so
// upgrades (which replace the package directory) keep it. Set
// LARK_AGENT_BOT_SKIP_FFMPEG=1 to skip.
const FFMPEG_REPO = "eugeneware/ffmpeg-static";
const FFMPEG_RELEASE = "b6.1.1";
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

function getDownloadURLs(filename) {
  return [
    `https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${filename}`,
  ];
}

function fetch(url, redirects = 5) {
  return new Promise((resolve, reject) => {
    if (redirects <= 0) return reject(new Error("Too many redirects"));
    const mod = url.startsWith("https") ? https : http;
    mod
      .get(url, { headers: { "User-Agent": "lark-agent-bot-npm" } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          return resolve(fetch(res.headers.location, redirects - 1));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      })
      .on("error", reject);
  });
}

async function download(urls) {
  for (const url of urls) {
    try {
      console.log(`[lark-agent-bot] Downloading from ${url}`);
      const data = await fetch(url);
      console.log(`[lark-agent-bot] Downloaded ${(data.length / 1024 / 1024).toFixed(1)} MB`);
      return data;
    } catch (err) {
      console.warn(`[lark-agent-bot] Failed: ${err.message}, trying next source...`);
    }
  }
  throw new Error(
    `[lark-agent-bot] Could not download binary from any source.\n` +
      `  Tried: ${urls.join(", ")}\n` +
      `  You can download manually from https://github.com/${GITHUB_REPO}/releases`
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

  const url = `https://github.com/${FFMPEG_REPO}/releases/download/${FFMPEG_RELEASE}/ffmpeg-${key}.gz`;
  const tmpPath = `${ffmpegPath}.download`;
  try {
    console.log(`[lark-agent-bot] ffmpeg not found, downloading from ${url}`);
    const gz = await fetch(url);
    fs.mkdirSync(toolDir, { recursive: true });
    const actual = crypto.createHash("sha256").update(gz).digest("hex");
    if (actual !== sha256) {
      throw new Error(`checksum mismatch: got ${actual}, want ${sha256}`);
    }
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
    console.warn(`[lark-agent-bot] Could not install ffmpeg: ${err.message}`);
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
      fs.unlinkSync(binaryPath);
    } catch {
      console.log(`[lark-agent-bot] Replacing existing binary with ${VERSION}...`);
      fs.unlinkSync(binaryPath);
    }
  }

  const urls = getDownloadURLs(filename);
  const data = await download(urls);

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

module.exports = { installFFmpeg };
