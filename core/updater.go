package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	githubReleasesAPI = "https://api.github.com/repos/ClaymanTwinkle/lark-connect/releases"
	githubLatestPage  = "https://github.com/ClaymanTwinkle/lark-connect/releases/latest"
	githubDownload    = "https://github.com/ClaymanTwinkle/lark-connect/releases/download"
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
		// The unauthenticated API allows 60 requests/hour per IP, which a
		// shared egress IP (proxy, VPN, NAT) exhausts easily and then answers
		// 403. The release page redirect has no such quota; it only lacks the
		// release notes and skips pre-releases.
		slog.Warn("updater: releases API failed, falling back to release page redirect", "error", err)
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
	req.Header.Set("User-Agent", "lark-connect-updater")

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
	req.Header.Set("User-Agent", "lark-connect-updater")
	req.Header.Set("Accept", "application/json")

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

// SelfUpdate downloads and installs the given release version.
func SelfUpdate(tag string) error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	filename := fmt.Sprintf("lark-connect-%s-%s-%s%s", tag, goos, goarch, ext)

	url := fmt.Sprintf("%s/%s/%s", githubDownload, tag, filename)
	slog.Info("updater: downloading", "url", url)
	data, err := downloadFile(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	var binary []byte
	if goos == "windows" {
		binary, err = extractBinaryFromZip(data)
	} else {
		binary, err = extractBinaryFromTarGz(data)
	}
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}

	return replaceBinary(binary)
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
	req.Header.Set("User-Agent", "lark-connect-updater")

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
		if strings.HasPrefix(name, "lark-connect") && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("lark-connect binary not found in archive")
}

func extractBinaryFromZip(data []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if strings.HasPrefix(name, "lark-connect") && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("lark-connect binary not found in zip archive")
}

func replaceBinary(newBinary []byte) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	dir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(dir, "lark-connect-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(newBinary); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write new binary: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", err)
	}

	oldPath := execPath + ".old"
	os.Remove(oldPath)

	if err := os.Rename(execPath, oldPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("backup old binary: %w", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		// Try to restore
		if restoreErr := os.Rename(oldPath, execPath); restoreErr != nil {
			slog.Error("updater: failed to restore old binary after install failed", "error", restoreErr)
		}
		return fmt.Errorf("install new binary: %w", err)
	}

	// Don't remove .old file on Linux - the running process may still need it
	// for os.Executable() to work correctly after restart.
	// The .old file will be overwritten on next update.

	slog.Info("updater: binary replaced successfully", "path", execPath)
	return nil
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
