package ui

import (
	"syscall"
	"time"
	"unsafe"
)

// browseFolder 弹出系统目录选择框，返回所选路径；取消返回空串。
// 模态期间主窗口不响应是 Windows 模态对话框的正常行为，用 modal 标志豁免看门狗。
func browseFolder(owner syscall.Handle) string {
	mainUI.modal.Store(true)
	mainUI.modalFrom.Store(time.Now().UnixNano())
	defer mainUI.modal.Store(false)

	display := make([]uint16, 260)
	bi := browseInfo{
		hwndOwner:      owner,
		pszDisplayName: &display[0],
		lpszTitle:      utf16Ptr("选择 dsh 安装目录"),
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle,
	}
	pidl, _, _ := pSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "" // 用户取消
	}
	defer pCoTaskMemFree.Call(pidl)

	buf := make([]uint16, 260)
	ok, _, _ := pSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
