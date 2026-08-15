// Package win 封装少量 Windows 原生能力：控制台检测、消息框、打开默认浏览器。
package win

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procMessageBoxW   = user32.NewProc("MessageBoxW")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// MessageBox 弹出模态错误对话框（仅 GUI 正式版由 main 调用）。
func MessageBox(title, text string) {
	_, _, _ = procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(title))),
		0x10, // MB_ICONERROR
	)
}

// OpenURL 用默认浏览器打开 url（ShellExecuteW，open 动词）。
func OpenURL(url string) error {
	r, _, _ := procShellExecuteW.Call(
		0, // hwnd
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(url))),
		0, // parameters
		0, // directory
		1, // SW_SHOWNORMAL
	)
	// ShellExecute 返回 >32 表示成功。
	if r <= 32 {
		return fmt.Errorf("ShellExecuteW 返回 %d（url=%s）", r, url)
	}
	return nil
}
