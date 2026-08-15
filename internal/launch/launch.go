// Package launch 实现 dsh 的启动与生命周期管理：
//   - Spawn/Ready/Stop/Wait：供 GUI 精细控制（启动→就绪→运行中→停止）
//   - Start：命令行完整流程（Spawn + Ready + 打开浏览器 + 信号等待）
package launch

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
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

// Server 表示一个已启动的 dsh 子进程。
type Server struct {
	cfg       *config.Config
	cmd       *exec.Cmd
	job       *win.Job
	childLog  *os.File
	done      chan error
	stoppedCh chan struct{}
	stopOnce  sync.Once
}

// URL 返回本服务监听地址。
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/", s.cfg.Port)
}

// VerifyInstall 校验配置中的安装目录可用（bin.js 存在且可执行）。
func VerifyInstall(cfg *config.Config) error {
	if !cfg.IsInstalled() {
		return errors.New("未检测到已安装的 dsh：请先安装（GUI 中点击「启动」或「安装」）")
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

// Spawn 校验配置并启动 dsh 子进程（不等待就绪）。
// 返回的 Server 必须由调用方在退出前 Stop（或让其自然退出后 Wait）。
func Spawn(cfg *config.Config) (*Server, error) {
	if err := VerifyInstall(cfg); err != nil {
		return nil, err
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("未找到 Node.js：请先安装 Node.js（https://nodejs.org）")
	}
	bin := node.DshBinPath(cfg.DshInstallDir)

	// 子进程输出写入日志文件（windowsgui 无控制台，绝不能继承无效句柄）。
	childLog, err := os.OpenFile(ChildLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("无法打开子进程日志：%w", err)
	}

	cmd := exec.Command(nodePath, bin, "web")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW：禁止子进程弹控制台
	cmd.Stdout = childLog
	cmd.Stderr = childLog
	if err := cmd.Start(); err != nil {
		childLog.Close()
		return nil, fmt.Errorf("启动 dsh 失败：%w", err)
	}
	log.Info("dsh 子进程已启动（PID %d）：%s %s web", cmd.Process.Pid, nodePath, bin)

	srv := &Server{
		cfg:       cfg,
		cmd:       cmd,
		childLog:  childLog,
		done:      make(chan error, 1),
		stoppedCh: make(chan struct{}),
	}
	go func() { srv.done <- cmd.Wait() }()

	// Job Object：启动器被强制结束（Task Manager）时同样回收子进程。
	if job, jerr := win.NewKillOnCloseJob(); jerr != nil {
		log.Warn("创建 Job Object 失败（%v），进程回收依赖正常退出路径", jerr)
	} else {
		srv.job = job
		if aerr := job.AssignPID(uint32(cmd.Process.Pid)); aerr != nil {
			log.Warn("子进程加入 Job Object 失败：%v", aerr)
		} else {
			log.Debug("子进程已加入 KILL_ON_JOB_CLOSE Job Object")
		}
	}
	return srv, nil
}

// Ready 阻塞直到端口就绪 / 子进程退出 / 被停止 / 超时。
func (s *Server) Ready() error {
	url := s.URL()
	log.Info("等待 %s 就绪（超时 %s）……", url, readyTimeout)
	select {
	case <-waitReady(url):
		log.Info("%s 已就绪", url)
		return nil
	case err := <-s.done:
		return fmt.Errorf("dsh 进程提前退出（%v），子进程日志：%s", err, ChildLogPath())
	case <-s.stoppedCh:
		return errors.New("已停止等待就绪")
	case <-time.After(readyTimeout):
		return fmt.Errorf("等待 %s 超时（%s），子进程日志：%s", url, readyTimeout, ChildLogPath())
	}
}

// OpenBrowser 打开默认浏览器（noBrowser 为 true 时跳过）。
func (s *Server) OpenBrowser(noBrowser bool) {
	url := s.URL()
	if noBrowser {
		log.Info("已跳过打开浏览器（--no-browser），请手动访问 %s", url)
		return
	}
	if err := win.OpenURL(url); err != nil {
		log.Warn("打开浏览器失败：%v（可手动访问 %s）", err, url)
	} else {
		log.Info("已在默认浏览器打开 %s", url)
	}
}

// Wait 返回子进程退出结果（阻塞直到子进程结束）。
func (s *Server) Wait() error {
	return <-s.done
}

// Stop 结束子进程并关闭 Job Object（幂等）。强制结束 launcher 时由 Job 兜底回收。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.stoppedCh)
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		if s.job != nil {
			s.job.Close()
		}
		if s.childLog != nil {
			_ = s.childLog.Close()
		}
	})
}

// Start 命令行完整流程：Spawn + Ready + 浏览器 + 前台等待。
func Start(cfg *config.Config, noBrowser bool) error {
	srv, err := Spawn(cfg)
	if err != nil {
		return err
	}
	defer srv.Stop()
	if err := srv.Ready(); err != nil {
		return err
	}
	srv.OpenBrowser(noBrowser)
	log.Info("dsh 正在运行（%s）。结束本程序（Task Manager / Ctrl+C）将同时结束 dsh 进程。", srv.URL())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-srv.done:
		if err == nil {
			log.Info("dsh 进程已退出")
		} else {
			log.Info("dsh 进程已退出：%v", err)
		}
		return nil
	case <-sigCh:
		log.Info("收到退出信号，正在结束 dsh 子进程……")
		srv.Stop()
		<-srv.done
		return nil
	}
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

// IsRunning 报告配置端口当前是否有服务响应（用于 status）。
func IsRunning(cfg *config.Config) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/", cfg.Port)
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
