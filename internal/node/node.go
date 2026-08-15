// Package node 负责 Node.js / npm 探测与命令封装，以及 dsh 安装目录的路径约定。
package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// DshPackage 是 npm 包名。
const DshPackage = "@deepseek-ai/dsh"

// Version 是语义化版本号。
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compatible 判断是否满足 README 要求：^22.19 || >=24。
func (v Version) Compatible() bool {
	return (v.Major == 22 && v.Minor >= 19) || v.Major >= 24
}

// Info 是探测结果。
type Info struct {
	NodePath string
	NodeVer  Version
	NPMPath  string
	NPMVer   string
}

var versionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func parseVersion(out string) (Version, bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return Version{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return Version{Major: major, Minor: minor, Patch: patch}, true
}

// createNoWindow 是 CREATE_NO_WINDOW 标志：windowsgui 父进程下，
// 子进程（node/npm/cmd）默认会新建控制台窗口闪黑框，必须禁止。
const createNoWindow = 0x08000000

func noWindow(cmd *exec.Cmd) *exec.Cmd {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	return cmd
}

// Detect 探测 node 与 npm，返回版本信息。任一缺失即报错并给出指引。
func Detect() (*Info, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("未找到 Node.js：请先安装 Node.js（建议 22.19+ 或 24+，下载 https://nodejs.org），并确保 node 在 PATH 中")
	}
	nodeOut, err := noWindow(exec.Command(nodePath, "--version")).Output()
	if err != nil {
		return nil, fmt.Errorf("执行 node --version 失败：%w", err)
	}
	nodeVer, ok := parseVersion(string(nodeOut))
	if !ok {
		return nil, fmt.Errorf("无法解析 node 版本：%q", strings.TrimSpace(string(nodeOut)))
	}

	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return nil, errors.New("未找到 npm：请确认 Node.js 安装完整（npm 随 Node.js 一起安装）")
	}
	npmOut, err := noWindow(exec.Command(npmPath, "--version")).Output()
	if err != nil {
		return nil, fmt.Errorf("执行 npm --version 失败：%w", err)
	}

	return &Info{
		NodePath: nodePath,
		NodeVer:  nodeVer,
		NPMPath:  npmPath,
		NPMVer:   strings.TrimSpace(string(npmOut)),
	}, nil
}

// Install 执行 `npm install -g --prefix <dir> <pkg>`，返回合并输出。
func Install(pkg, dir string) (string, error) {
	cmd := noWindow(exec.Command("npm", "install", "-g", "--no-fund", "--no-audit", "--prefix", dir, pkg))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// NPMGlobalRoot 返回 `npm root -g` 的 node_modules 目录。
func NPMGlobalRoot() (string, error) {
	out, err := noWindow(exec.Command("npm", "root", "-g")).Output()
	return strings.TrimSpace(string(out)), err
}

// RunScript 执行 `node <script> [args...]`，返回合并输出。
func RunScript(nodePath, script string, args ...string) (string, error) {
	all := append([]string{script}, args...)
	cmd := noWindow(exec.Command(nodePath, all...))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// DshBinPath 返回安装目录中 dsh 的 bin.js 路径。
func DshBinPath(installDir string) string {
	return filepath.Join(installDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
}

// DshPackageDir 返回安装目录中 dsh 包的目录。
func DshPackageDir(installDir string) string {
	return filepath.Join(installDir, "node_modules", "@deepseek-ai", "dsh")
}

// DshVersion 运行 `node bin.js --version` 获取版本字符串。
func DshVersion(nodePath, installDir string) (string, error) {
	out, err := RunScript(nodePath, DshBinPath(installDir), "--version")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(out)
	if v == "" {
		return "", errors.New("bin.js --version 无输出")
	}
	return v, nil
}

// DshVersionFromPackage 直接从 package.json 读取版本（bin.js 不可用时的回退）。
func DshVersionFromPackage(installDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(DshPackageDir(installDir), "package.json"))
	if err != nil {
		return "", err
	}
	var p struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return "", err
	}
	if p.Version == "" {
		return "", errors.New("package.json 缺少 version 字段")
	}
	return p.Version, nil
}
