package ui

import (
	"os"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/install"
	"dsh-launcher/internal/launch"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
)

// onInstall：点击「安装」按钮。
func (u *uiState) onInstall() {
	if u.isBusy() {
		return
	}
	dir := u.pathText()
	if dir == "" {
		log.Warn("请先点「浏览…」选择安装目录，或直接输入路径，再点「安装」。")
		return
	}
	u.setBusy(true)
	u.invalidateAll()
	go func() {
		defer func() {
			u.setBusy(false)
			postMessage(u.hwnd, msgRefresh, 0, 0)
		}()
		if err := install.Run(dir); err != nil {
			log.Error("安装失败：%v", err)
			return
		}
		log.Info("安装完成。可以点击「启动」。")
	}()
}

// onStart：点击「启动」。未安装 → 选路径 → 安装 → 自动启动。
func (u *uiState) onStart() {
	if u.isBusy() || u.isRunning() {
		return
	}
	cfg, err := config.Load()
	if err == nil && cfg.IsInstalled() {
		if _, serr := os.Stat(node.DshBinPath(cfg.DshInstallDir)); serr == nil {
			u.setBusy(true)
			u.invalidateAll()
			go func() {
				u.startOnly(cfg)
				u.setBusy(false)
				postMessage(u.hwnd, msgRefresh, 0, 0)
			}()
			return
		}
	}

	// 未安装 → 需要安装目录（用户先点「浏览…」选择，或直接输入）
	dir := u.pathText()
	if dir == "" {
		log.Warn("未检测到已安装的 dsh，且未填写安装目录。请点「浏览…」选择安装目录后，再点「启动」即可自动安装并启动。")
		return
	}
	u.setBusy(true)
	u.invalidateAll()
	go func() {
		defer func() {
			u.setBusy(false)
			postMessage(u.hwnd, msgRefresh, 0, 0)
		}()
		log.Info("未检测到已安装的 dsh，开始安装到 %s……", dir)
		if err := install.Run(dir); err != nil {
			log.Error("安装失败：%v", err)
			return
		}
		cfg, err := config.Load()
		if err != nil {
			log.Error("安装后读取配置失败：%v", err)
			return
		}
		u.startOnly(cfg)
	}()
}

// onMove：点击「移动」按钮——把已安装的 dsh 挪到新路径。
func (u *uiState) onMove() {
	if u.isBusy() {
		return
	}
	cfg, err := config.Load()
	if err != nil || !cfg.IsInstalled() {
		log.Warn("尚未安装 dsh，无需移动。请先点「安装」或「启动」。")
		return
	}
	// 运行中禁止移动：仅检查本 launcher 启动的 dsh 子进程
	//（不用端口检测——3080 可能被其他 dsh 实例占用，误判会让用户无法移动）
	u.mu.Lock()
	srv := u.server
	u.mu.Unlock()
	if srv != nil {
		log.Warn("dsh 正在运行，请先停止（关闭窗口 / 退出）后再移动。")
		return
	}
	dir := browseFolder(u.hwnd)
	if dir == "" {
		return // 用户取消
	}
	u.setBusy(true)
	u.invalidateAll()
	go func() {
		defer func() {
			u.setBusy(false)
			postMessage(u.hwnd, msgRefresh, 0, 0)
		}()
		if err := install.Move(dir); err != nil {
			log.Error("移动失败：%v", err)
			return
		}
		log.Info("移动完成。新安装目录：%s", dir)
	}()
}

// noBrowser 返回是否跳过打开浏览器（环境变量 DSH_LAUNCHER_NO_BROWSER=1，供测试/无头场景）。
func noBrowser() bool {
	return os.Getenv("DSH_LAUNCHER_NO_BROWSER") != ""
}

// startOnly：启动 dsh 并等待就绪（在 goroutine 中调用）。
func (u *uiState) startOnly(cfg *config.Config) {
	srv, err := launch.Spawn(cfg)
	if err != nil {
		log.Error("启动失败：%v", err)
		return
	}
	u.setServer(srv)
	if err := srv.Ready(); err != nil {
		log.Error("启动失败：%v", err)
		srv.Stop()
		u.setServer(nil)
		return
	}
	srv.OpenBrowser(noBrowser())
	u.setRunning(true)
	log.Info("dsh 运行中：%s。关闭本窗口将结束 dsh。", srv.URL())
	postMessage(u.hwnd, msgRefresh, 0, 0)

	// 子进程退出时同步状态
	go func() {
		_ = srv.Wait()
		log.Warn("dsh 进程已退出")
		u.setServer(nil)
		u.setRunning(false)
		postMessage(u.hwnd, msgRefresh, 0, 0)
	}()
}
