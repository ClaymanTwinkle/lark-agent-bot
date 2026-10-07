package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	githubReleasesAPI = "https://api.github.com/repos/ClaymanTwinkle/lark-agent-bot/releases"
	githubLatestPage  = "https://github.com/ClaymanTwinkle/lark-agent-bot/releases/latest"
	githubDownload    = "https://github.com/ClaymanTwinkle/lark-agent-bot/releases/download"
)

type ReleaseInfo struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Prerelease bool   `json:"prerelease"`
	CreatedAt  string `json:"created_at"`
}

// CheckForUpdate queries GitHub for newer releases.
func CheckForUpdate(currentVersion string) (*ReleaseInfo, error) {
	return checkForUpdateFrom(currentVersion, githubReleasesAPI+"?per_page=20", githubLatestPage)
}

func checkForUpdateFrom(currentVersion, apiURL, latestPageURL string) (*ReleaseInfo, error) {
	best, err := newestReleaseFrom(apiURL)
	if err != nil {
		// Without a token the API allows 60 requests/hour per IP, which a
		// shared egress IP (proxy, VPN, NAT) exhausts easily and then answers
		// 403. The release page redirect has no such quota; it only lacks the
		// release notes and skips pre-releases.
		logArgs := []any{"error", err}
		if githubToken() == "" {
			logArgs = append(logArgs, "hint", "set GH_TOKEN or GITHUB_TOKEN to raise the API rate limit")
		}
		slog.Warn("updater: releases API failed, falling back to release page redirect", logArgs...)
		tag, pageURL, ferr := latestTagFromRedirect(latestPageURL)
		if ferr != nil {
			return nil, fmt.Errorf("check releases: %w (fallback: %v)", err, ferr)
		}
		best = &ReleaseInfo{TagName: tag, Body: pageURL}
	}
	if best == nil {
		return nil, nil
	}

	cur := normalizeVersion(currentVersion)
	latest := normalizeVersion(best.TagName)
	if cur == latest || semverCompare(best.TagName, currentVersion) <= 0 {
		return nil, nil
	}

	return best, nil
}

// newestReleaseFrom returns the highest-versioned release listed by the
// releases API, or nil when there is none.
func newestReleaseFrom(apiURL string) (*ReleaseInfo, error) {
	releases, err := fetchReleasesFrom(apiURL)
	if err != nil {
		return nil, err
	}
	var best *ReleaseInfo
	for i := range releases {
		r := &releases[i]
		if r.TagName == "" {
			continue
		}
		if best == nil || semverCompare(r.TagName, best.TagName) > 0 {
			best = r
		}
	}
	return best, nil
}

// latestTagFromRedirect reads the tag that GitHub's /releases/latest page
// redirects to (.../releases/tag/<tag>) without following the redirect.
// It returns the tag and the release page URL.
func latestTagFromRedirect(pageURL string) (tag, releaseURL string, err error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "lark-agent-bot-updater")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	_, rest, ok := strings.Cut(loc, "/releases/tag/")
	if !ok {
		return "", "", fmt.Errorf("no release tag in redirect (HTTP %d, location %q)", resp.StatusCode, loc)
	}
	rest, _, _ = strings.Cut(rest, "?")
	tag, err = url.PathUnescape(strings.TrimSuffix(rest, "/"))
	if err != nil || tag == "" {
		return "", "", fmt.Errorf("bad release tag in redirect %q", loc)
	}
	return tag, loc, nil
}

func fetchReleasesFrom(apiURL string) ([]ReleaseInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lark-agent-bot-updater")
	req.Header.Set("Accept", "application/json")
	SetGitHubAuth(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// githubTokenEnvVars are read, in this order, for a token to call the GitHub
// API with: the variables the gh CLI reads, in its order of precedence.
var githubTokenEnvVars = []string{"GH_TOKEN", "GITHUB_TOKEN"}

func githubToken() string {
	for _, name := range githubTokenEnvVars {
		if tok := strings.TrimSpace(os.Getenv(name)); tok != "" {
			return tok
		}
	}
	return ""
}

// SetGitHubAuth authenticates a GitHub API request with the token in
// GH_TOKEN or GITHUB_TOKEN, if either is set. Without one, every process on
// the host shares GitHub's quota of 60 requests per hour per IP; with one,
// the request counts against the token's account (5000 per hour). Use it
// only for api.github.com requests, so the token is not sent elsewhere. It
// must never be logged.
func SetGitHubAuth(req *http.Request) {
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// SelfUpdate downloads and installs the given release version.
func SelfUpdate(tag string) error {
	binary, err := DownloadReleaseBinary(tag)
	if err != nil {
		return err
	}
	return replaceBinary(binary)
}

// releaseChecksumsName is the release asset that lists the SHA-256 of every
// archive: `sha256sum *.tar.gz *.zip` output from the Makefile's release-all
// target.
const releaseChecksumsName = "checksums.txt"

// ErrUnverifiedRelease is wrapped by the error of a release download that
// could not be checked against its release's checksums.txt.
var ErrUnverifiedRelease = errors.New("refusing to install an unverified download")

// releaseArchiveName is the release asset holding the binary for goos/goarch.
// release.yml publishes these names; keep them in sync.
func releaseArchiveName(tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("lark-agent-bot-%s-%s-%s%s", tag, goos, goarch, ext)
}

// ReleaseArchiveURL is the URL DownloadReleaseBinary downloads tag's archive
// for this platform from.
func ReleaseArchiveURL(tag string) string {
	return fmt.Sprintf("%s/%s/%s", githubDownload, tag, releaseArchiveName(tag, runtime.GOOS, runtime.GOARCH))
}

// DownloadReleaseBinary downloads tag's release archive for this platform,
// checks it against the SHA-256 the release's checksums.txt lists for it,
// and returns the lark-agent-bot binary inside. It is the download step
// shared by /upgrade and the `lark-agent-bot update` command.
func DownloadReleaseBinary(tag string) ([]byte, error) {
	return downloadReleaseBinaryFrom(githubDownload, tag, runtime.GOOS, runtime.GOARCH)
}

// downloadReleaseBinaryFrom is DownloadReleaseBinary for the release
// downloads under base. Unlike the npm installer, which installs with a
// warning when no checksums.txt lists the archive, it fails closed: the
// archive is only extracted once checksums.txt has been read, lists it, and
// matches it.
func downloadReleaseBinaryFrom(base, tag, goos, goarch string) ([]byte, error) {
	dir := base + "/" + tag
	archive := releaseArchiveName(tag, goos, goarch)

	// checksums.txt is small; reading it first skips the archive download
	// when the update cannot be verified anyway.
	sums, err := downloadFile(dir + "/" + releaseChecksumsName)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot get %s: %w", ErrUnverifiedRelease, releaseChecksumsName, err)
	}
	want, ok := parseChecksums(string(sums))[archive]
	if !ok {
		return nil, fmt.Errorf("%w: %s does not list %s", ErrUnverifiedRelease, releaseChecksumsName, archive)
	}

	archiveURL := dir + "/" + archive
	slog.Info("updater: downloading", "url", archiveURL)
	data, err := downloadFile(archiveURL)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%w: the SHA-256 of %s is %s, but %s lists %s",
			ErrUnverifiedRelease, archive, got, releaseChecksumsName, want)
	}
	slog.Info("updater: SHA-256 matches checksums.txt", "asset", archive)

	var binary []byte
	if goos == "windows" {
		binary, err = extractBinaryFromZip(data)
	} else {
		binary, err = extractBinaryFromTarGz(data)
	}
	if err != nil {
		return nil, fmt.Errorf("extract binary: %w", err)
	}
	return binary, nil
}

// checksumLineRe matches a sha256sum output line: the hex digest, then the
// file name, with "*" before it in binary mode.
var checksumLineRe = regexp.MustCompile(`^([0-9a-fA-F]{64})\s+\*?(\S.*?)\s*$`)

// parseChecksums reads sha256sum output (a release's checksums.txt) into a
// map from file name to lowercase hex digest. It reads the same lines as
// parseChecksums in npm/install.js.
func parseChecksums(text string) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		if m := checksumLineRe.FindStringSubmatch(strings.TrimSuffix(line, "\r")); m != nil {
			sums[m[2]] = strings.ToLower(m[1])
		}
	}
	return sums
}

func downloadFile(url string) ([]byte, error) {
	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lark-agent-bot-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

func extractBinaryFromTarGz(data []byte) ([]byte, error) {
	r := bytes.NewReader(data)
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := filepath.Base(hdr.Name)
		if strings.HasPrefix(name, "lark-agent-bot") && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("lark-agent-bot binary not found in archive")
}

func extractBinaryFromZip(data []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if strings.HasPrefix(name, "lark-agent-bot") && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("lark-agent-bot binary not found in zip archive")
}

func replaceBinary(newBinary []byte) error {
	execPath, err := runningExecutablePath()
	if err != nil {
		return err
	}
	_, err = replaceBinaryAt(execPath, newBinary)
	return err
}

// InstallBinary installs newBinary as the standard lark-agent-bot executable
// next to the running one (see replaceBinaryAt) and returns its path. It is
// the install step shared by /upgrade and the `lark-agent-bot update` command.
func InstallBinary(newBinary []byte) (string, error) {
	execPath, err := runningExecutablePath()
	if err != nil {
		return "", err
	}
	return replaceBinaryAt(execPath, newBinary)
}

// StandardBinaryName is the file name every install and update uses, so
// agents can always call the `lark-agent-bot` command from the directory the
// engine puts on their PATH.
func StandardBinaryName() string {
	if runtime.GOOS == "windows" {
		return "lark-agent-bot.exe"
	}
	return "lark-agent-bot"
}

// installTargetPath is where an update installs the binary: the standard
// name in the running executable's directory.
func installTargetPath(execPath string) string {
	return filepath.Join(filepath.Dir(execPath), StandardBinaryName())
}

// runningExecutablePath returns this process's executable path. A path that
// no longer resolves is kept as is: on Windows it is the name the process was
// started from, which an update by another process may have renamed.
func runningExecutablePath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	// After a self-update, os.Executable() can report the renamed ".old"
	// image on Linux; the installed binary sits at the original path.
	if trimmed, ok := strings.CutSuffix(execPath, ".old"); ok {
		if _, err := os.Stat(trimmed); err == nil {
			execPath = trimmed
		}
	}
	return execPath, nil
}

// InstalledBinaryPath is the executable to restart into and to read the
// installed version from: the standard-named binary next to the running one
// when it exists (an update installed it there), otherwise the running
// executable itself.
func InstalledBinaryPath() (string, error) {
	execPath, err := runningExecutablePath()
	if err != nil {
		return "", err
	}
	return installedPathFor(execPath), nil
}

func installedPathFor(execPath string) string {
	if target := installTargetPath(execPath); target != execPath {
		if fi, err := os.Stat(target); err == nil && fi.Mode().IsRegular() {
			return target
		}
	}
	return execPath
}

// backupPath returns path+".old", or a unique ".old-<unix time>" name when
// the previous backup is still in use: another process sharing the binary
// has not restarted since the last update and runs that image, which Windows
// cannot replace.
func backupPath(path string) string {
	oldPath := path + ".old"
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		oldPath = fmt.Sprintf("%s.old-%d", path, time.Now().UnixNano())
		slog.Info("updater: previous backup in use, using a new backup name", "path", oldPath)
	}
	return oldPath
}

// replaceBinaryAt installs newBinary as lark-agent-bot[.exe] in execPath's
// directory and returns that path. The binary it replaces is kept as a
// ".old" backup. When the running executable has another name (e.g. the
// versioned name from an old release archive), it is renamed to a backup
// too, so scripts pointing at it fail loudly instead of silently starting a
// stale build.
func replaceBinaryAt(execPath string, newBinary []byte) (string, error) {
	target := installTargetPath(execPath)
	tmpFile, err := os.CreateTemp(filepath.Dir(target), "lark-agent-bot-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(newBinary); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write new binary: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("chmod: %w", err)
	}

	oldPath := ""
	if _, err := os.Stat(target); err == nil {
		oldPath = backupPath(target)
		if err := os.Rename(target, oldPath); err != nil {
			os.Remove(tmpPath)
			return "", fmt.Errorf("backup old binary: %w", err)
		}
	}

	if err := os.Rename(tmpPath, target); err != nil {
		if oldPath != "" {
			if restoreErr := os.Rename(oldPath, target); restoreErr != nil {
				slog.Error("updater: failed to restore old binary after install failed", "error", restoreErr)
			}
		}
		os.Remove(tmpPath)
		return "", fmt.Errorf("install new binary: %w", err)
	}

	if execPath != target {
		if _, err := os.Stat(execPath); err == nil {
			if err := os.Rename(execPath, backupPath(execPath)); err != nil {
				slog.Warn("updater: could not retire the old binary name", "path", execPath, "error", err)
			} else {
				slog.Warn("updater: binary renamed to the standard name; update scripts or services that start the old path",
					"old_path", execPath, "new_path", target)
			}
		}
	}

	// Backups are not removed: on Linux the running process may still need
	// its image for os.Executable() until restart. The next update reuses them.
	slog.Info("updater: binary installed", "path", target)
	return target, nil
}

// InstalledVersion reports the version of the installed executable (see
// InstalledBinaryPath). It is newer than CurrentVersion once another process
// sharing the binary has upgraded it: the file was replaced, but this
// process still runs the old image until it restarts.
func InstalledVersion() (string, error) {
	execPath, err := InstalledBinaryPath()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, execPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run %s --version: %w", execPath, err)
	}
	v := parseVersionOutput(string(out))
	if v == "" {
		return "", fmt.Errorf("unrecognized %s --version output %q", execPath, strings.TrimSpace(string(out)))
	}
	return v, nil
}

// installedVersionFunc is InstalledVersion, replaceable in tests.
var installedVersionFunc = InstalledVersion

// upgradeAlreadyInstalled reports whether the binary on disk is already at
// least target, returning its version. It is false when the version cannot
// be read, so the caller falls back to downloading.
func upgradeAlreadyInstalled(target string) (string, bool) {
	installed, err := installedVersionFunc()
	if err != nil {
		slog.Warn("updater: cannot read installed version, downloading", "error", err)
		return "", false
	}
	if parseSemver(installed) == (semver{}) || semverCompare(installed, target) < 0 {
		return installed, false
	}
	return installed, true
}

// parseVersionOutput extracts the version from `lark-agent-bot --version`
// output ("lark-agent-bot v1.2.3\ncommit: ...").
func parseVersionOutput(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "lark-agent-bot" {
		return ""
	}
	return fields[1]
}

// --- semver comparison ---

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(.+))?$`)

type semver struct {
	major, minor, patch int
	pre                 string
	preNum              int
}

func parseSemver(v string) semver {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return semver{}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	pre := m[4]
	preNum := 0
	if idx := strings.LastIndex(pre, "."); idx >= 0 {
		preNum, _ = strconv.Atoi(pre[idx+1:])
	}
	return semver{major: major, minor: minor, patch: patch, pre: pre, preNum: preNum}
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// semverCompare returns >0 if a > b, <0 if a < b, 0 if equal.
func semverCompare(a, b string) int {
	sa := parseSemver(a)
	sb := parseSemver(b)

	if d := sa.major - sb.major; d != 0 {
		return d
	}
	if d := sa.minor - sb.minor; d != 0 {
		return d
	}
	if d := sa.patch - sb.patch; d != 0 {
		return d
	}
	// No pre-release > has pre-release (1.0.0 > 1.0.0-beta.1)
	if sa.pre == "" && sb.pre != "" {
		return 1
	}
	if sa.pre != "" && sb.pre == "" {
		return -1
	}
	// Both have pre-release: compare lexicographically, then by number
	if sa.pre != sb.pre {
		if d := strings.Compare(sa.pre, sb.pre); d != 0 {
			// "beta" prefix comparison; if same prefix, compare numbers
			aPre := strings.TrimRight(sa.pre, "0123456789.")
			bPre := strings.TrimRight(sb.pre, "0123456789.")
			if aPre == bPre {
				return sa.preNum - sb.preNum
			}
			return d
		}
	}
	return 0
}
