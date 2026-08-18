// Package update 提供升级检测，覆盖两条独立的分发渠道：
//
//   - dsh 本体（@deepseek-ai/dsh）走 npm registry，检测 latest dist-tag；
//   - dsh-launcher.exe 走 GitHub Release，检测仓库最新 tag。
//
// 本包只做“检测”，不执行升级：dsh 升级 = 重新 install（npm install），
// 启动器升级 = 下载新 exe（打开 Release 页），由调用方（GUI/CLI）决定动作。
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dsh-launcher/internal/node"
)

const (
	// LauncherReleaseURL 是启动器 GitHub Release 最新版页面（升级启动器时打开）。
	LauncherReleaseURL = "https://github.com/kuaizhongqiang/dsh-launcher/releases/latest"
	launcherAPI        = "https://api.github.com/repos/kuaizhongqiang/dsh-launcher/releases/latest"
	httpTimeout        = 10 * time.Second
)

// ---------- 语义化版本比较（支持 v 前缀与 -prerelease） ----------

type semver struct {
	major, minor, patch int
	pre                 []string // 预发布标识符（空 = 正式版）
}

// parseSemver 解析语义化版本：可选 v/V 前缀、major.minor.patch、
// 可选 -prerelease（如 0.1.0-rc.6），忽略 +build 元数据。
func parseSemver(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	main, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		if p == "" {
			return semver{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	if hasPre {
		if pre == "" {
			return semver{}, false // 形如 0.1.0- 的非法预发布
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return semver{}, false // 形如 0.1.0-alpha. 的非法预发布
			}
		}
	}
	return v, true
}

// compareSemver 按 semver 规范比较：返回 -1（a<b）、0（a==b）、1（a>b）。
// 正式版 > 预发布版（如 0.1.0 > 0.1.0-rc.6）。
func compareSemver(a, b semver) int {
	if a.major != b.major {
		return sign(a.major - b.major)
	}
	if a.minor != b.minor {
		return sign(a.minor - b.minor)
	}
	if a.patch != b.patch {
		return sign(a.patch - b.patch)
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := comparePreID(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return sign(len(a.pre) - len(b.pre))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// comparePreID 比较两个预发布标识符：数字按数值、字母按 ASCII；数字 < 字母。
func comparePreID(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return sign(an - bn)
	case aerr == nil:
		return -1 // 数字 < 字母
	case berr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// ---------- 检测 ----------

// CheckLauncher 检测启动器自身（GitHub Release 最新 tag）是否有新版。
// current 为构建注入的版本（形如 v0.1.0）；dev 构建返回错误提示跳过。
// 返回最新 tag（形如 v0.2.0）与是否有更新。
func CheckLauncher(current string) (latest string, hasUpdate bool, err error) {
	if current == "" || current == "dev" {
		return "", false, fmt.Errorf("当前为 dev 构建，跳过启动器升级检测")
	}
	cur, ok := parseSemver(current)
	if !ok {
		return "", false, fmt.Errorf("当前版本 %q 无法解析", current)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := getJSON(launcherAPI, &rel); err != nil {
		return "", false, fmt.Errorf("查询 GitHub Release 失败：%w", err)
	}
	lat, ok := parseSemver(rel.TagName)
	if !ok {
		return "", false, fmt.Errorf("最新 Release 版本 %q 无法解析", rel.TagName)
	}
	return rel.TagName, compareSemver(lat, cur) > 0, nil
}

// CheckDsh 检测 npm registry 上 @deepseek-ai/dsh 的 latest 版本是否有新版。
// current 为已安装版本（如 0.1.0-rc.6）；spec 为下载源策略——主源查询失败且
// 未强制镜像/未禁用自动切换时回退镜像再试。返回最新版本与是否有更新。
func CheckDsh(current string, spec node.RegistrySpec) (latest string, hasUpdate bool, err error) {
	cur, ok := parseSemver(current)
	if !ok {
		return "", false, fmt.Errorf("当前版本 %q 无法解析", current)
	}
	primary, mirror := spec.Effective()
	registries := []string{primary}
	if !spec.PreferMirror && !spec.DisableAutoSwitch && mirror != primary {
		registries = append(registries, mirror)
	}
	var firstErr, lastErr error
	for _, reg := range registries {
		v, e := dshLatest(reg)
		if e == nil {
			lat, ok2 := parseSemver(v)
			if !ok2 {
				return v, false, fmt.Errorf("最新版本 %q 无法解析", v)
			}
			return v, compareSemver(lat, cur) > 0, nil
		}
		if firstErr == nil {
			firstErr = e
		}
		lastErr = e
	}
	if firstErr != nil && lastErr != nil && firstErr != lastErr {
		return "", false, fmt.Errorf("主源与镜像均查询失败：主源 %v；镜像 %v", firstErr, lastErr)
	}
	return "", false, lastErr
}

// dshLatest 查询某个 registry 上 @deepseek-ai/dsh 的 latest dist-tag 版本。
func dshLatest(registry string) (string, error) {
	base := strings.TrimRight(registry, "/")
	url := base + "/@deepseek-ai/dsh/latest"
	var p struct {
		Version string `json:"version"`
	}
	if err := getJSON(url, &p); err != nil {
		return "", err
	}
	if p.Version == "" {
		return "", fmt.Errorf("%s 未返回 version 字段", url)
	}
	return p.Version, nil
}

// getJSON 请求 JSON 接口并解码到 out。
func getJSON(url string, out any) error {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// GitHub API 拒绝无 User-Agent 的请求（403）。
	req.Header.Set("User-Agent", "dsh-launcher/update-check")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d（%s）", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
