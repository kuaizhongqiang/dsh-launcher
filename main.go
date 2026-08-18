// dsh-launcher：本机原生 dsh（@deepseek-ai/dsh，npm 版）的安装 + 启动引导器。
// 子命令：install / start（默认）/ stop / status / check-update / --help。
package main

import (
	"fmt"
	"os"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/install"
	"dsh-launcher/internal/launch"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
	"dsh-launcher/internal/ui"
	"dsh-launcher/internal/update"
	"dsh-launcher/internal/win"
)

const usage = `dsh-launcher — 本机原生 dsh 的安装 / 启动引导器

用法：
  dsh-launcher.exe                  无参数（双击）→ 打开图形界面：
                                     状态展示 + 路径选择 + 安装 + 一键启动
  dsh-launcher.exe install [--dir <目录>] [--registry <url>] [--mirror] [--no-mirror]
                                     命令行安装 @deepseek-ai/dsh
                                     默认安装目录 %LOCALAPPDATA%\dsh，
                                     --dir 可覆盖（例如 D:\Tools\dsh）
                                     --registry 指定 npm 源（默认 registry.npmjs.org）
                                     --mirror 强制使用国内镜像（registry.npmmirror.com）
                                     --no-mirror 关闭镜像自动切换
                                     未指定时自动探测：主源不可达/明显慢于镜像则切换镜像
  dsh-launcher.exe move --dir <目录>        把已安装的 dsh 挪到新路径
                                            （跨盘自动复制+删除，运行中禁止）
  dsh-launcher.exe start [--no-browser]     确保 dsh 运行并打开浏览器（dsh 独立运行，
                                            启动器退出不影响；已在运行则不重复启动）
  dsh-launcher.exe stop                     停止 dsh（按配置端口结束进程）
  dsh-launcher.exe status                   显示安装目录、版本与运行状态
  dsh-launcher.exe check-update             检查升级：dsh（npm registry）与
                                            启动器自身（GitHub Release）是否有新版
  dsh-launcher.exe --version | -v           显示版本
  dsh-launcher.exe --help | -h              显示本帮助

说明：
  - 配置 launcher.json 与 exe 同目录，跟随 exe 走（便携）
  - 运行日志写入 %TEMP%\dsh-launcher.log（windowsgui 版无控制台输出，以此为准）
  - dsh 以独立进程运行，不绑定启动器：关闭本窗口/退出不影响 dsh，用「停止」结束
  - 国内下载加速：install 自动探测 npm 官方源与 npmmirror 镜像的延迟，
    官方源不可达或明显更慢时自动使用镜像（可 --no-mirror 关闭）；
    也可用环境变量 DSH_LAUNCHER_NPM_REGISTRY / DSH_LAUNCHER_NPM_MIRROR /
    DSH_LAUNCHER_PREFER_MIRROR=1 / DSH_LAUNCHER_NO_MIRROR=1 控制
`

// guiBuild 由正式构建通过 -ldflags "-X main.guiBuild=1" 注入（配合 -H windowsgui）。
// 为 "1" 时，致命错误以 MessageBox 弹窗提示（双击场景无控制台可见）。
var guiBuild = "0"

// version 由构建注入：-ldflags "-X main.version=v0.0.1"；本地开发为 "dev"。
var version = "dev"

func main() {
	_ = log.Init() // 失败仅降级为控制台输出
	log.SetDebug(os.Getenv("DSH_LAUNCHER_DEBUG") != "")

	args := os.Args[1:]
	if len(args) == 0 {
		// 无参数（双击 exe）→ 图形界面：状态展示 + 路径选择 + 安装 + 一键启动
		os.Exit(ui.Run(version))
		return
	}

	switch args[0] {
	case "--help", "-h", "help":
		log.Raw(usage)
	case "--version", "-v":
		log.Raw("dsh-launcher " + version + "\n")
	case "install":
		runInstall(args[1:])
	case "move":
		runMove(args[1:])
	case "start":
		runStart(args[1:])
	case "stop":
		runStop()
	case "status":
		runStatus()
	case "check-update":
		runCheckUpdate()
	default:
		log.Raw(usage)
		log.Error("未知命令：%s", args[0])
		os.Exit(2)
	}
}

// runInstall 处理 install 子命令。
func runInstall(rest []string) {
	dir := ""
	spec := install.RegistrySpecFromEnv(node.RegistrySpec{})
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dir", "-d":
			if i+1 >= len(rest) {
				fail("--dir 缺少参数")
			}
			i++
			dir = rest[i]
		case "--registry":
			if i+1 >= len(rest) {
				fail("--registry 缺少参数")
			}
			i++
			spec.Registry = rest[i]
		case "--mirror":
			spec.PreferMirror = true
		case "--no-mirror":
			spec.DisableAutoSwitch = true
		default:
			log.Raw(usage)
			fail("install 未知参数：%s", rest[i])
		}
	}
	if err := install.Run(dir, spec); err != nil {
		fail("安装失败：%v", err)
	}
	log.Info("安装完成。现在可以运行 dsh-launcher.exe（或 dsh-launcher.exe start）启动 dsh。")
}

// runMove 处理 move 子命令：把 dsh 安装目录挪到新路径。
func runMove(rest []string) {
	dir := ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dir", "-d":
			if i+1 >= len(rest) {
				fail("--dir 缺少参数")
			}
			i++
			dir = rest[i]
		default:
			log.Raw(usage)
			fail("move 未知参数：%s", rest[i])
		}
	}
	if dir == "" {
		log.Raw(usage)
		fail("move 需要 --dir <目标路径>")
	}
	if err := install.Move(dir); err != nil {
		fail("移动失败：%v", err)
	}
	log.Info("移动完成。")
}

// runStart 处理 start 子命令（默认）。
func runStart(rest []string) {
	noBrowser := false
	for _, a := range rest {
		switch a {
		case "--no-browser":
			noBrowser = true
		default:
			log.Raw(usage)
			fail("start 未知参数：%s", a)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		if os.IsNotExist(err) {
			fail("未找到 launcher.json：请先运行 dsh-launcher.exe install 完成安装")
		}
		fail("读取配置失败：%v", err)
	}
	if _, err := launch.StartDetached(cfg, noBrowser); err != nil {
		fail("启动失败：%v", err)
	}
	log.Info("dsh 已独立运行（启动器退出不影响 dsh）。")
}

// runStop 处理 stop 子命令：结束配置端口上的 dsh 进程。
func runStop() {
	cfg, err := config.Load()
	if err != nil {
		if os.IsNotExist(err) {
			fail("未找到 launcher.json：请先运行 dsh-launcher.exe install 完成安装")
		}
		fail("读取配置失败：%v", err)
	}
	if err := launch.Stop(cfg); err != nil {
		fail("停止失败：%v", err)
	}
	log.Info("停止完成。")
}

// runStatus 处理 status 子命令。
func runStatus() {
	cfg, err := config.Load()
	if err != nil {
		if os.IsNotExist(err) {
			log.Info("状态：未安装（缺少 launcher.json）")
			log.Info("请先运行：dsh-launcher.exe install")
			return
		}
		fail("读取配置失败：%v", err)
	}
	if !cfg.IsInstalled() {
		log.Info("状态：未安装（launcher.json 中未记录安装目录）")
		log.Info("请先运行：dsh-launcher.exe install")
		return
	}

	log.Info("安装目录：%s", cfg.DshInstallDir)
	log.Info("dsh 版本：%s", cfg.DshVersion)
	log.Info("端口：%d", cfg.Port)
	log.Info("安装时间：%s", cfg.InstalledAt.Format("2006-01-02 15:04:05 -07:00"))

	bin := node.DshBinPath(cfg.DshInstallDir)
	if _, err := os.Stat(bin); err != nil {
		log.Info("运行状态：未就绪（找不到 %s，安装可能损坏，请重新 install）", bin)
		return
	}
	if launch.IsRunning(cfg) {
		log.Info("运行状态：运行中（端口 %d 有响应）", cfg.Port)
	} else {
		log.Info("运行状态：未运行（端口 %d 无响应）", cfg.Port)
	}
}

// runCheckUpdate 检查 dsh 与启动器自身是否有新版（检测只读，不执行升级）。
func runCheckUpdate() {
	cfg, _ := config.Load()
	var dshCur string
	if cfg != nil && cfg.IsInstalled() {
		dshCur = cfg.DshVersion
	}
	spec := install.RegistrySpecFromConfig(cfg)
	if dshCur == "" {
		log.Info("dsh：未安装（先运行 dsh-launcher.exe install）")
	} else if latest, hasUpd, err := update.CheckDsh(dshCur, spec); err != nil {
		log.Error("dsh 升级检测失败：%v", err)
	} else if hasUpd {
		log.Info("dsh：当前 %s → 最新 %s（可升级，运行 install 即升级到最新）", dshCur, latest)
	} else {
		log.Info("dsh：当前 %s 已是最新", dshCur)
	}

	latest, hasUpd, err := update.CheckLauncher(version)
	if err != nil {
		log.Error("启动器升级检测失败：%v", err)
	} else if hasUpd {
		log.Info("启动器：当前 %s → 最新 %s（可升级，下载页 %s）", version, latest, update.LauncherReleaseURL)
	} else {
		log.Info("启动器：当前 %s 已是最新", version)
	}
}

// fail 记录错误；GUI 正式版（guiBuild=1）额外弹错误框，随后以退出码 1 结束。
func fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Error("%s", msg)
	if guiBuild == "1" {
		win.MessageBox("dsh-launcher 错误", msg)
	}
	os.Exit(1)
}
