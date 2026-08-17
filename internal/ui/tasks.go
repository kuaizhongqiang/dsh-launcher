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
		// 下载源策略：既有配置 + 环境变量（官方源慢/不可达时自动切换国内镜像）
		cfg, _ := config.Load()
		if err := install.Run(dir, install.RegistrySpecFromConfig(cfg)); err != nil {
			log.Error("安装失败：%v", err)
			return
		}
		log.Info("安装完成。可以点击「启动」。")
	}()
}

// onStart：点击「启动」——确保 dsh 运行（已在运行则直接打开浏览器；未安装则引导安装）。
func (u *uiState) onStart() {
	if u.isBusy() {
		return
	}
	cfg, err := config.Load()
	if err == nil && cfg.IsInstalled() {
		if _, serr := os.Stat(node.DshBinPath(cfg.DshInstallDir)); serr == nil {
			u.setBusy(true)
			u.invalidateAll()
			go func() {
				defer func() {
					u.setBusy(false)
					postMessage(u.hwnd, msgRefresh, 0, 0)
				}()
				already, serr := launch.StartDetached(cfg, noBrowser())
				if serr != nil {
					log.Error("启动失败：%v", serr)
					return
				}
				if already {
					log.Info("dsh 已在运行。")
				} else {
					log.Info("dsh 已启动并独立运行（关闭本窗口不影响 dsh）。")
				}
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
		// 下载源策略：既有配置 + 环境变量（官方源慢/不可达时自动切换国内镜像）
		cfg0, _ := config.Load()
		if err := install.Run(dir, install.RegistrySpecFromConfig(cfg0)); err != nil {
			log.Error("安装失败：%v", err)
			return
		}
		cfg, err := config.Load()
		if err != nil {
			log.Error("安装后读取配置失败：%v", err)
			return
		}
		already, serr := launch.StartDetached(cfg, noBrowser())
		if serr != nil {
			log.Error("启动失败：%v", serr)
			return
		}
		if already {
			log.Info("dsh 已在运行。")
		} else {
			log.Info("dsh 已启动并独立运行（关闭本窗口不影响 dsh）。")
		}
	}()
}

// onStop：点击「停止」按钮——结束端口上的 dsh 进程。
func (u *uiState) onStop() {
	if u.isBusy() {
		return
	}
	cfg, err := config.Load()
	if err != nil || !cfg.IsInstalled() {
		log.Warn("尚未安装 dsh。")
		return
	}
	u.setBusy(true)
	u.invalidateAll()
	go func() {
		defer func() {
			u.setBusy(false)
			postMessage(u.hwnd, msgRefresh, 0, 0)
		}()
		if err := launch.Stop(cfg); err != nil {
			log.Error("停止失败：%v", err)
			return
		}
		log.Info("已停止 dsh（端口 %d 的进程已结束）。", cfg.Port)
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
	// 运行中禁止移动（独立进程的 dsh 由 install.Move 按端口检测拒绝）
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
