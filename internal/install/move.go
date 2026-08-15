// 移动已安装的 dsh：把整个安装目录挪到新路径（跨盘自动复制+删除），
// 更新 launcher.json 并在移动后校验。
package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/launch"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
)

// Move 把 dsh 安装目录移动到 newDir（任意路径，跨盘自动复制+删除）。
// 要求：已安装、目标路径合法且非空目录、dsh 未在运行。
// 成功后更新 launcher.json 的 dshInstallDir，并校验新位置可用。
func Move(newDir string) error {
	cfg, err := config.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("未找到 launcher.json，请先运行 install")
		}
		return fmt.Errorf("读取配置失败：%w", err)
	}
	if !cfg.IsInstalled() {
		return errors.New("未记录安装目录，请先运行 install")
	}
	old := cfg.DshInstallDir
	newAbs, err := filepath.Abs(newDir)
	if err != nil {
		return fmt.Errorf("解析目标路径失败：%w", err)
	}
	if strings.EqualFold(filepath.Clean(old), filepath.Clean(newAbs)) {
		return fmt.Errorf("目标路径与当前安装目录相同：%s", old)
	}
	if _, err := os.Stat(old); err != nil {
		return fmt.Errorf("当前安装目录不存在：%s", old)
	}
	// 目标路径校验：不存在，或存在但为空目录
	if fi, err := os.Stat(newAbs); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("目标路径已存在且不是目录：%s", newAbs)
		}
		entries, rerr := os.ReadDir(newAbs)
		if rerr != nil {
			return fmt.Errorf("无法读取目标目录：%s", newAbs)
		}
		if len(entries) > 0 {
			return fmt.Errorf("目标目录已存在且非空：%s（可能是上次移动中断的残留，请先删除该目录后再试）", newAbs)
		}
	}
	// 运行中禁止移动（端口有响应即拒绝；报错说明可能是其他服务）
	if launch.IsRunning(cfg) {
		return fmt.Errorf("端口 %d 有服务响应（可能是 dsh 或其他服务在运行），请先停止 dsh 后再移动", cfg.Port)
	}

	log.Info("开始移动 dsh：%s → %s", old, newAbs)
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return fmt.Errorf("创建父目录失败：%w", err)
	}
	if err := moveDir(old, newAbs); err != nil {
		return fmt.Errorf("移动失败：%w", err)
	}

	// 更新配置；失败则回滚目录
	cfg.DshInstallDir = newAbs
	if err := cfg.Save(); err != nil {
		_ = moveDir(newAbs, old) // 尽力回滚
		return fmt.Errorf("更新 launcher.json 失败（已尝试回滚）：%w", err)
	}
	log.Info("launcher.json 已更新：%s", newAbs)

	// 移动后校验
	verifyMoved(newAbs)
	log.Info("完成。dsh 已移动到 %s", newAbs)
	return nil
}

// verifyMoved 校验新位置的 dsh 可用（bin.js --version，回退 package.json）。
func verifyMoved(newAbs string) {
	bin := node.DshBinPath(newAbs)
	if _, err := os.Stat(bin); err != nil {
		log.Warn("移动后未找到 %s，请手动确认安装完整性", bin)
		return
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		log.Warn("移动后未找到 node，跳过版本校验")
		return
	}
	if ver, verr := node.DshVersion(nodePath, newAbs); verr == nil {
		log.Info("移动后校验通过：%s@%s", node.DshPackage, ver)
		return
	}
	if pver, perr := node.DshVersionFromPackage(newAbs); perr == nil {
		log.Info("移动后校验通过：%s@%s（package.json）", node.DshPackage, pver)
		return
	}
	log.Warn("移动后版本校验失败，请手动确认安装完整性")
}

// moveDir 移动目录：同盘直接 Rename；跨盘（或失败）回退为复制 + 删除。
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	log.Info("目标与源不在同一磁盘，使用复制方式（大目录可能较慢）……")
	if err := copyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// copyDir 递归复制目录（含进度日志）。
func copyDir(src, dst string) error {
	count := 0
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// 普通文件：复制
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, cerr := os.Create(target)
		if cerr != nil {
			in.Close()
			return cerr
		}
		_, cerr = io.Copy(out, in)
		in.Close()
		oerr := out.Close()
		if cerr != nil {
			return cerr
		}
		if oerr != nil {
			return oerr
		}
		count++
		if count%200 == 0 {
			log.Info("已复制 %d 个文件……", count)
		}
		return nil
	})
}
