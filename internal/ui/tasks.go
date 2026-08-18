package ui

import (
	"os"
	"strings"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/install"
	"dsh-launcher/internal/launch"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
	"dsh-launcher/internal/update"
	"dsh-launcher/internal/win"
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

// onUpdate：点击「检查更新 / 一键升级」按钮。
// 无已知更新 → 触发一次升级检测；有更新 → 执行升级（dsh 重新 install，启动器打开 Release 页）。
func (u *uiState) onUpdate() {
	if u.isBusy() || u.updChecking || u.updBusy {
		return
	}
	if u.updDshAvail || u.updLauncherAvail {
		u.upgradeAll()
		return
	}
	u.checkUpdates()
}

// checkUpdates 检测 dsh（npm）与启动器（GitHub Release）是否有新版。
// 在后台运行，完成后经 msgRefresh 刷新状态行与按钮；失败只记日志，不打扰用户。
func (u *uiState) checkUpdates() {
	if u.updChecking || u.updBusy {
		return
	}
	u.updChecking = true
	u.updLine = "更新     正在检查…"
	u.updCol = colTextDim
	u.invalidateAll()
	go func() {
		defer func() {
			u.updChecking = false
			postMessage(u.hwnd, msgRefresh, 0, 0)
		}()

		cfg, _ := config.Load()
		var dshCur string
		if cfg != nil && cfg.IsInstalled() {
			dshCur = cfg.DshVersion
		}
		spec := install.RegistrySpecFromConfig(cfg)

		type chk struct {
			latest string
			hasUpd bool
			err    error
		}
		dshCh := make(chan chk, 1)
		laCh := make(chan chk, 1)
		go func() {
			if dshCur == "" {
				dshCh <- chk{}
				return
			}
			latest, hasUpd, err := update.CheckDsh(dshCur, spec)
			dshCh <- chk{latest, hasUpd, err}
		}()
		go func() {
			latest, hasUpd, err := update.CheckLauncher(u.launcherVer)
			laCh <- chk{latest, hasUpd, err}
		}()
		d := <-dshCh
		la := <-laCh

		u.updDshAvail = d.hasUpd
		u.updLauncherAvail = la.hasUpd

		var parts []string
		if d.hasUpd {
			parts = append(parts, "dsh "+d.latest+" 可升级")
		}
		if la.hasUpd {
			parts = append(parts, "启动器 "+la.latest+" 可升级")
		}
		switch {
		case len(parts) > 0:
			u.updLine = "更新     " + strings.Join(parts, "、")
			u.updCol = colPrimary
		case d.err == nil && la.err == nil:
			u.updLine = "更新     已是最新"
			u.updCol = colGreen
		default:
			u.updLine = "更新     检测失败（详见日志）"
			u.updCol = colRed
		}

		if dshCur == "" {
			log.Info("升级检测：dsh 未安装，跳过（先点「安装」）。")
		} else if d.err != nil {
			log.Warn("升级检测：dsh %v", d.err)
		} else if d.hasUpd {
			log.Info("升级检测：dsh 当前 %s → 最新 %s（可升级）", dshCur, d.latest)
		} else {
			log.Info("升级检测：dsh 当前 %s 已是最新。", dshCur)
		}
		if la.err != nil {
			log.Warn("升级检测：启动器 %v", la.err)
		} else if la.hasUpd {
			log.Info("升级检测：启动器 当前 %s → 最新 %s（可升级，下载页 %s）", u.launcherVer, la.latest, update.LauncherReleaseURL)
		} else if u.launcherVer != "" && u.launcherVer != "dev" {
			log.Info("升级检测：启动器 当前 %s 已是最新。", u.launcherVer)
		} else {
			log.Info("升级检测：启动器 dev 构建，跳过。")
		}
	}()
}

// upgradeAll：执行升级——dsh 重新 npm install（后台下载最新版）；
// 启动器有新版本时打开 GitHub Release 页。
func (u *uiState) upgradeAll() {
	cfg, err := config.Load()
	doDsh := u.updDshAvail && err == nil && cfg.IsInstalled()
	if u.updDshAvail && !doDsh {
		log.Warn("未找到安装配置，无法升级 dsh。请先点「安装」。")
	}
	if u.updLauncherAvail {
		log.Info("打开启动器最新 Release 页：%s", update.LauncherReleaseURL)
		if berr := win.OpenURL(update.LauncherReleaseURL); berr != nil {
			log.Warn("打开浏览器失败：%v（可手动访问 %s）", berr, update.LauncherReleaseURL)
		}
	}
	if !doDsh {
		return
	}
	u.setBusy(true)
	u.updBusy = true
	u.invalidateAll()
	go func() {
		defer func() {
			u.setBusy(false)
			postMessage(u.hwnd, msgRefresh, 0, 0)
		}()
		log.Info("开始升级 dsh（重新安装到 %s，下载最新版）……", cfg.DshInstallDir)
		if ierr := install.Run(cfg.DshInstallDir, install.RegistrySpecFromConfig(cfg)); ierr != nil {
			log.Error("dsh 升级失败：%v", ierr)
			u.updBusy = false
			return
		}
		log.Info("dsh 升级完成。")
		u.updDshAvail = false
		u.updBusy = false
		u.checkUpdates() // 复查最新状态（内部另起 goroutine，本 goroutine 随即结束）
	}()
}

// noBrowser 返回是否跳过打开浏览器（环境变量 DSH_LAUNCHER_NO_BROWSER=1，供测试/无头场景）。
func noBrowser() bool {
	return os.Getenv("DSH_LAUNCHER_NO_BROWSER") != ""
}
