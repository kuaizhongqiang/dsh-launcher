// Package launch 实现 dsh 的启动与停止。
//
// 生命周期设计（v0.1.0 起）：dsh server 以独立进程运行，**不绑定启动器**。
// 启动器（GUI/CLI）负责：确保 dsh 运行 → 打开浏览器 → 退出（dsh 继续服务）。
// 停止 dsh 通过按端口定位进程并结束（stop 命令 / GUI「停止」按钮）。
package launch

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
	"dsh-launcher/internal/win"
)

const (
	readyTimeout = 60 * time.Second
	pollInterval = 500 * time.Millisecond
	httpTimeout  = 2 * time.Second
)

// ChildLogPath 是 dsh 子进程 stdout/stderr 的落盘路径。
func ChildLogPath() string {
	return filepath.Join(os.TempDir(), "dsh-launcher-child.log")
}

// URL 返回配置端口对应的访问地址。
func URL(cfg *config.Config) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", cfg.Port)
}

// VerifyInstall 校验配置中的安装目录可用（bin.js 存在且可执行）。
func VerifyInstall(cfg *config.Config) error {
	if !cfg.IsInstalled() {
		return errors.New("未检测到已安装的 dsh：请先安装")
	}
	bin := node.DshBinPath(cfg.DshInstallDir)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("dsh 安装目录损坏：找不到 %s，请重新安装", bin)
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return errors.New("未找到 Node.js：请先安装 Node.js（https://nodejs.org）")
	}
	out, err := node.DshVersion(nodePath, cfg.DshInstallDir)
	if err != nil {
		// 回退：package.json 存在即视为可用（部分版本 bin.js 可能不响应 --version）。
		ver, verErr := node.DshVersionFromPackage(cfg.DshInstallDir)
		if verErr != nil {
			return fmt.Errorf("dsh 校验失败（%v；输出：%q），请重新安装", err, strings.TrimSpace(out))
		}
		log.Warn("bin.js --version 未通过（%v），按 package.json 版本 %s 继续", err, ver)
		return nil
	}
	log.Info("dsh 版本校验通过：%s", strings.TrimSpace(out))
	return nil
}

// StartDetached 确保 dsh 运行（独立进程，不绑定本进程生命周期）：
//   - dsh 已在运行（端口有响应）→ 不重复启动，直接打开浏览器，返回 alreadyRunning=true
//   - 未运行 → 启动 dsh 子进程（无 Job Object、不回收），就绪后 detach 并打开浏览器
func StartDetached(cfg *config.Config, noBrowser bool) (alreadyRunning bool, err error) {
	if err := VerifyInstall(cfg); err != nil {
		return false, err
	}
	url := URL(cfg)

	if IsRunning(cfg) {
		log.Info("dsh 已在运行：%s", url)
		if !noBrowser {
			if berr := win.OpenURL(url); berr != nil {
				log.Warn("打开浏览器失败：%v（可手动访问 %s）", berr, url)
			} else {
				log.Info("已在默认浏览器打开 %s", url)
			}
		}
		return true, nil
	}

	nodePath, err := exec.LookPath("node")
	if err != nil {
		return false, errors.New("未找到 Node.js：请先安装 Node.js（https://nodejs.org）")
	}
	bin := node.DshBinPath(cfg.DshInstallDir)

	// 子进程输出写入日志文件（windowsgui 无控制台，绝不能继承无效句柄）。
	childLog, err := os.OpenFile(ChildLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, fmt.Errorf("无法打开子进程日志：%w", err)
	}

	cmd := exec.Command(nodePath, bin, "web")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	cmd.Stdout = childLog
	cmd.Stderr = childLog
	if err := cmd.Start(); err != nil {
		childLog.Close()
		return false, fmt.Errorf("启动 dsh 失败：%w", err)
	}
	log.Info("dsh 子进程已启动（PID %d）：%s %s web", cmd.Process.Pid, nodePath, bin)
	// 注意：不创建 Job Object、不 Kill——dsh 独立运行，本进程退出不影响它。

	// 等待就绪
	log.Info("等待 %s 就绪（超时 %s）……", url, readyTimeout)
	select {
	case <-waitReady(url):
		log.Info("%s 已就绪", url)
	case <-time.After(readyTimeout):
		childLog.Close()
		return false, fmt.Errorf("等待 %s 超时（%s），子进程日志：%s", url, readyTimeout, ChildLogPath())
	}

	if !noBrowser {
		if berr := win.OpenURL(url); berr != nil {
			log.Warn("打开浏览器失败：%v（可手动访问 %s）", berr, url)
		} else {
			log.Info("已在默认浏览器打开 %s", url)
		}
	}

	// detach：解除关联，dsh 独立运行
	if rerr := cmd.Process.Release(); rerr != nil {
		log.Warn("解除子进程关联失败：%v", rerr)
	}
	childLog.Close()
	log.Info("dsh 已启动并独立运行：%s（关闭本程序不影响 dsh，可用 stop 停止）", url)
	return false, nil
}

// Stop 结束配置端口上的 dsh 进程（按端口定位 PID 并强制结束）。
func Stop(cfg *config.Config) error {
	pids := findPIDsByPort(cfg.Port)
	if len(pids) == 0 {
		return fmt.Errorf("端口 %d 上没有监听中的进程，dsh 可能未在运行", cfg.Port)
	}
	for _, pid := range pids {
		log.Info("结束进程 PID %d……", pid)
		if err := killPID(pid); err != nil {
			log.Warn("结束 PID %d 失败：%v", pid, err)
		}
	}
	return nil
}

// findPIDsByPort 用 netstat 找监听端口的 PID。
func findPIDsByPort(port int) []int {
	out, err := node.RunNoWindow("netstat", "-ano")
	if err != nil {
		log.Warn("netstat 执行失败：%v", err)
		return nil
	}
	marker := fmt.Sprintf(":%d", port)
	var pids []int
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, marker) || !strings.Contains(line, "LISTENING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// killPID 强制结束进程（taskkill /F）。
func killPID(pid int) error {
	out, err := node.RunNoWindow("taskkill", "/F", "/PID", strconv.Itoa(pid))
	if err != nil {
		return fmt.Errorf("taskkill 失败：%s", strings.TrimSpace(out))
	}
	return nil
}

// waitReady 轮询 url 直到有 HTTP 响应。
func waitReady(url string) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		client := &http.Client{Timeout: httpTimeout}
		for {
			resp, err := client.Get(url)
			if err == nil {
				resp.Body.Close()
				ch <- struct{}{}
				return
			}
			time.Sleep(pollInterval)
		}
	}()
	return ch
}

// IsRunning 报告配置端口当前是否有服务响应。
func IsRunning(cfg *config.Config) bool {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(URL(cfg))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
