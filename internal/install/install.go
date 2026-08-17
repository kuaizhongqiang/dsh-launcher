// Package install 实现 install 子命令：探测 node/npm、安装 @deepseek-ai/dsh、
// 校验并写入 launcher.json。
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
)

// Run 执行完整安装流程。dir 为 --dir 覆盖（空串表示默认 %LOCALAPPDATA%\dsh），
// spec 为下载源选择策略（可为零值，走默认自动切换）。
func Run(dir string, spec node.RegistrySpec) error {
	// 1. 探测 node / npm
	ni, err := node.Detect()
	if err != nil {
		return err
	}
	if !ni.NodeVer.Compatible() {
		return fmt.Errorf("Node.js 版本 %s 不满足要求（需要 ^22.19 或 >=24），请到 https://nodejs.org 升级后重试", ni.NodeVer)
	}
	log.Info("检测到 Node.js %s（%s）", ni.NodeVer, ni.NodePath)
	log.Info("检测到 npm %s（%s）", ni.NPMVer, ni.NPMPath)

	// 2. 解析安装目录
	if dir == "" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			dir = filepath.Join(la, "dsh")
		} else {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, "AppData", "Local", "dsh")
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("解析安装目录失败：%w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("创建安装目录失败：%w", err)
	}
	log.Info("安装目录：%s", abs)

	// 2.5 提示：npm 全局已装过 dsh 的话可以跳过本次安装
	hintGlobalInstall()

	// 3. 选择下载源并 npm install（失败自动换另一源重试一次）
	registry, viaMirror, probe := spec.ChooseRegistry()
	log.Info("下载源：%s（%s）", registry, probe)
	if viaMirror {
		log.Info("已使用国内镜像 %s 下载（国内用户加速）", registry)
	}
	installOnce := func(r string) (string, error) {
		log.Info("正在通过 %s 安装 %s（首次需要下载，请耐心等待）……", r, node.DshPackage)
		return node.Install(node.DshPackage, abs, r)
	}
	out, err := installOnce(registry)
	if err != nil && !spec.PreferMirror && !spec.DisableAutoSwitch {
		// 自动回退：主源失败且镜像未尝试 → 镜像；镜像失败 → 主源
		primary, mirror := spec.Effective()
		alt := ""
		if registry == primary {
			alt = mirror
		} else if registry == mirror {
			alt = primary
		}
		if alt != "" && alt != registry {
			log.Warn("从 %s 下载失败，自动切换 %s 重试……", registry, alt)
			out, err = installOnce(alt)
			registry = alt
		}
	}
	if err != nil {
		log.Error("npm install 失败：%v", err)
		log.Info("npm 输出：\n%s", out)
		return fmt.Errorf("npm install 失败，详情见日志 %s", log.Path())
	}
	log.Info("npm install 完成（registry：%s）", registry)
	log.Debug("npm 输出：\n%s", out)

	// 4. 校验安装结果
	bin := node.DshBinPath(abs)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("安装完成但未找到 %s，安装可能不完整，请重试 install", bin)
	}
	ver, err := node.DshVersion(ni.NodePath, abs)
	if err != nil {
		log.Warn("bin.js --version 校验失败（%v），回退读取 package.json", err)
		ver, err = node.DshVersionFromPackage(abs)
		if err != nil {
			return fmt.Errorf("无法确认 dsh 版本：%v", err)
		}
	}
	log.Info("dsh 校验通过：%s@%s", node.DshPackage, ver)

	// 5. 写入 launcher.json（含下载源信息，供后续 install 复用）
	cfg := &config.Config{
		DshInstallDir: abs,
		DshVersion:    ver,
		Port:          config.DefaultPort,
		InstalledAt:   time.Now(),
		Registry:      primaryOrDefault(spec.Registry),
		RegistryMirror: mirrorOrDefault(spec.Mirror),
		PreferMirror:  spec.PreferMirror,
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("写入配置失败：%w", err)
	}
	cfgPath, _ := config.Path()
	log.Info("配置已写入 %s", cfgPath)
	log.Info("完成。~/.dsh 配置目录保持默认，无需手动处理。")
	log.Info("现在可以运行 dsh-launcher.exe（或 dsh-launcher.exe start）启动 dsh。")
	return nil
}

// primaryOrDefault 返回显式主源（未设置则留空，表示 npm 默认）。
func primaryOrDefault(r string) string {
	if r == "" || r == node.DefaultNpmRegistry {
		return ""
	}
	return r
}

// mirrorOrDefault 返回显式镜像（未设置则留空，表示默认 npmmirror）。
func mirrorOrDefault(m string) string {
	if m == "" || m == node.DefaultNpmMirror {
		return ""
	}
	return m
}

// RegistrySpecFromEnv 用环境变量补齐下载源策略（CLI 已显式设置的字段不被覆盖）：
//   DSH_LAUNCHER_NPM_REGISTRY / DSH_LAUNCHER_NPM_MIRROR /
//   DSH_LAUNCHER_PREFER_MIRROR=1 / DSH_LAUNCHER_NO_MIRROR=1
func RegistrySpecFromEnv(spec node.RegistrySpec) node.RegistrySpec {
	if v := os.Getenv("DSH_LAUNCHER_NPM_REGISTRY"); v != "" && spec.Registry == "" {
		spec.Registry = v
	}
	if v := os.Getenv("DSH_LAUNCHER_NPM_MIRROR"); v != "" && spec.Mirror == "" {
		spec.Mirror = v
	}
	if os.Getenv("DSH_LAUNCHER_PREFER_MIRROR") != "" {
		spec.PreferMirror = true
	}
	if os.Getenv("DSH_LAUNCHER_NO_MIRROR") != "" {
		spec.DisableAutoSwitch = true
	}
	return spec
}

// RegistrySpecFromConfig 从既有 launcher.json 配置 + 环境变量构建下载源策略（GUI 安装用）。
func RegistrySpecFromConfig(cfg *config.Config) node.RegistrySpec {
	var spec node.RegistrySpec
	if cfg != nil {
		spec.Registry = cfg.Registry
		spec.Mirror = cfg.RegistryMirror
		spec.PreferMirror = cfg.PreferMirror
	}
	return RegistrySpecFromEnv(spec)
}

// hintGlobalInstall 检测 npm 全局是否已装 dsh，有则提示可跳过。
func hintGlobalInstall() {
	root, err := node.NPMGlobalRoot()
	if err != nil || root == "" {
		return
	}
	pkgDir := filepath.Join(root, "@deepseek-ai", "dsh")
	if _, err := os.Stat(pkgDir); err == nil {
		log.Info("提示：npm 全局已安装 dsh（%s）。若不想重复安装，可终止本次 install 直接运行 start。", pkgDir)
	}
}
