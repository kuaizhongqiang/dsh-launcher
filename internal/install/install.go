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

// Run 执行完整安装流程。dir 为 --dir 覆盖（空串表示默认 %LOCALAPPDATA%\dsh）。
func Run(dir string) error {
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

	// 3. npm install
	log.Info("正在安装 %s（首次需要下载，请耐心等待）……", node.DshPackage)
	out, err := node.Install(node.DshPackage, abs)
	if err != nil {
		log.Error("npm install 失败：%v", err)
		log.Info("npm 输出：\n%s", out)
		return fmt.Errorf("npm install 失败，详情见日志 %s", log.Path())
	}
	log.Info("npm install 完成")
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

	// 5. 写入 launcher.json
	cfg := &config.Config{
		DshInstallDir: abs,
		DshVersion:    ver,
		Port:          config.DefaultPort,
		InstalledAt:   time.Now(),
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
