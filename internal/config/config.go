// Package config 负责 launcher.json 的读写。
// 配置文件与 exe 同目录，跟随 exe 走（便携）。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultPort 是 dsh web 默认监听端口。
const DefaultPort = 3080

// Config 是运行期配置（launcher.json）。
type Config struct {
	DshInstallDir string    `json:"dshInstallDir"`
	DshVersion    string    `json:"dshVersion,omitempty"`
	Port          int       `json:"port"`
	InstalledAt   time.Time `json:"installedAt"`
}

// Path 返回 launcher.json 路径（与 exe 同目录）。
func Path() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "launcher.json"), nil
}

// Load 读取 launcher.json。文件不存在时返回 os.ErrNotExist。
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Port <= 0 {
		c.Port = DefaultPort
	}
	return &c, nil
}

// Save 把配置写回 launcher.json。
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// IsInstalled 报告是否已记录可用安装目录。
func (c *Config) IsInstalled() bool {
	return c != nil && c.DshInstallDir != ""
}
