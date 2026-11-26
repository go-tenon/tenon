package deploy

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// defaultGitHubBaseURL 是拉取 GitHub Release 产物的默认基础地址。
const defaultGitHubBaseURL = "https://github.com"

// DownloadReleaseBinary 从 GitHub Release 下载指定 tag 和架构的二进制到 destPath；
// 若 Release 附带 checksums.txt，则强制校验 SHA-256 摘要，返回二进制的十六进制摘要。
func DownloadReleaseBinary(ctx context.Context, client *http.Client, baseURL, repo, tag, app, arch, destPath string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("deploy: --tag requires 'repo: owner/repo' in deploy/app.yaml or a GitHub git origin remote")
	}
	if !isSafeVersion(tag) {
		return "", fmt.Errorf("deploy: invalid tag %q", tag)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultGitHubBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	assetName := fmt.Sprintf("%s-linux-%s", app, arch)
	assetURL := fmt.Sprintf("%s/%s/releases/download/%s/%s", baseURL, repo, tag, assetName)
	checksumsURL := fmt.Sprintf("%s/%s/releases/download/%s/checksums.txt", baseURL, repo, tag)

	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", fmt.Errorf("deploy: build release request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("deploy: download %s: %w", assetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deploy: download %s: HTTP %d", assetURL, resp.StatusCode)
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("deploy: create %s: %w", destPath, err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return "", fmt.Errorf("deploy: write %s: %w", destPath, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("deploy: close %s: %w", destPath, closeErr)
	}
	gotSum := hex.EncodeToString(h.Sum(nil))

	// 若存在 checksums.txt 则强制比对 SHA-256，404 时跳过。
	wantSum, found, err := fetchReleaseChecksum(ctx, client, checksumsURL, token, assetName)
	if err != nil {
		return "", err
	}
	if found && !strings.EqualFold(gotSum, wantSum) {
		return "", fmt.Errorf("deploy: checksum mismatch for %s: got %s want %s", assetName, gotSum, wantSum)
	}
	return gotSum, nil
}

// fetchReleaseChecksum 读取 checksums.txt 并提取 assetName 对应的 SHA-256；文件不存在（404）时返回 found=false。
func fetchReleaseChecksum(ctx context.Context, client *http.Client, url, token, assetName string) (sum string, found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("deploy: fetch checksums.txt: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("deploy: fetch checksums.txt: HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		name = strings.TrimPrefix(name, "./")
		if name == assetName {
			return fields[0], true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", false, fmt.Errorf("deploy: parse checksums.txt: %w", err)
	}
	return "", false, fmt.Errorf("deploy: checksums.txt does not contain %s", assetName)
}
