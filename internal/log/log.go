// Package log 提供 dsh-launcher 的统一日志：同时写控制台（若存在）与
// %TEMP%\dsh-launcher.log。windowsgui 构建没有控制台，输出以日志文件为准。
package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu          sync.Mutex
	logFile     *os.File
	debug       bool
	subscribers []func(string)
)

// Subscribe 注册日志行回调（每行含时间戳前缀与换行，供 GUI 实时显示）。
// 返回取消订阅的函数。回调在调用日志的 goroutine 中执行，须非阻塞。
func Subscribe(fn func(string)) func() {
	mu.Lock()
	subscribers = append(subscribers, fn)
	i := len(subscribers) - 1
	mu.Unlock()
	return func() {
		mu.Lock()
		subscribers = append(subscribers[:i], subscribers[i+1:]...)
		mu.Unlock()
	}
}

// Path 返回日志文件路径：%TEMP%\dsh-launcher.log。
func Path() string {
	return filepath.Join(os.TempDir(), "dsh-launcher.log")
}

// Init 以追加方式打开日志文件。失败时仅降级为控制台输出，不返回致命错误。
func Init() error {
	f, err := os.OpenFile(Path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	mu.Lock()
	logFile = f
	mu.Unlock()
	return nil
}

// SetDebug 开启/关闭 Debug 级别输出。
func SetDebug(b bool) { debug = b }

// Raw 无时间戳地原样写入（用于 --help 输出）。
func Raw(text string) {
	_, _ = fmt.Fprint(os.Stdout, text)
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		_, _ = logFile.WriteString(text)
	}
}

func line(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	full := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), level, msg)

	// 控制台构建可见；windowsgui 构建写失败被忽略。
	_, _ = fmt.Fprint(os.Stdout, full)

	mu.Lock()
	if logFile != nil {
		_, _ = logFile.WriteString(full)
	}
	subs := make([]func(string), len(subscribers))
	copy(subs, subscribers)
	mu.Unlock()

	// 锁外回调，避免与 Subscribe/其他日志互相死锁。
	for _, s := range subs {
		s(full)
	}
}

// Info 记录普通信息。
func Info(format string, args ...any) { line("INFO", format, args...) }

// Warn 记录警告。
func Warn(format string, args ...any) { line("WARN", format, args...) }

// Error 记录错误。
func Error(format string, args ...any) { line("ERROR", format, args...) }

// Debug 记录调试信息（需 SetDebug(true)）。
func Debug(format string, args ...any) {
	if debug {
		line("DEBUG", format, args...)
	}
}
