// Package ui 实现 dsh-launcher 的图形界面：深色 dsh 风格窗口
// （自绘标题栏 / 状态卡片 / 按钮，实时日志区）。
package ui

import (
	"syscall"
	"unsafe"
)

// ---------- 颜色（取自 dsh web 主题：neutral-bluish + deepseek 品牌蓝） ----------

func rgb(r, g, b int) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

const (
	colBg          = 0x171515 // #151517 窗口背景（neutral-bluish-950）
	colPanel       = 0x2E2C2C // #2C2C2E 卡片/面板（bluish-850）
	colInput       = 0x242323 // #232324 输入框（bluish-875）
	colBorder      = 0x3E3A3A // 边框
	colPrimary     = 0xFE5686 // #5686FE 主按钮（deepseek-450）
	colPrimaryHov  = 0xE67641 // #4176E6 hover（deepseek-500）
	colPrimaryPrs  = 0xB26848 // #4868B2 pressed（deepseek-600）
	colDanger      = 0x2311E8 // #E81123 关闭按钮 hover
	colSec         = 0x363536 // #353638 次要按钮（bluish-800）
	colSecHov      = 0x3C3B3E
	colSecPrs      = 0x242323
	colText        = 0xEDEAE8 // #E8EAED 主文字
	colTextDim     = 0xA6A09C // 次级文字
	colGreen       = 0x50B93F // #3FB950 正常状态
	colRed         = 0x4951F8 // #F85149 异常状态
	colTitleHover  = 0x2E2A2A
)

// ---------- Win32 常量 ----------

const (
	wsPopup        = 0x80000000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipChildren = 0x02000000
	wsClipSiblings = 0x04000000
	wsVScroll      = 0x00200000

	esMultiLine = 0x0004
	esReadOnly  = 0x0800
	esAutoVScroll = 0x0040
	esLeft      = 0x0000

	wmPaint        = 0x000F
	wmEraseBkgnd   = 0x0014
	wmClose        = 0x0010
	wmDestroy      = 0x0002
	wmCommand      = 0x0111
	wmNcHitTest    = 0x0084
	wmCtlColorEdit = 0x0133
	wmCtlColorStat = 0x0138
	wmDrawItem     = 0x002B
	wmMouseMove    = 0x0200
	wmMouseLeave   = 0x02A3
	wmLButtonDown  = 0x0201
	wmLButtonUp    = 0x0202
	wmApp          = 0x8000

	htCaption = 2
	htClient  = 1

	swShow = 5

	odtButton = 0x0004
	odsSelected = 0x0004
	odsDisabled = 0x0008

	dtCenter     = 0x0001
	dtVcenter    = 0x0004
	dtSingleLine = 0x0020

	emSetSel     = 0x00B1
	emReplaceSel = 0x00C2
	emScrollCaret = 0x00B7

	tmeLeave = 0x0002

	diNormal = 0x0003
	imageIcon = 1

	fwBold = 700

	psSolid = 0

	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle  = 0x0040

	// WM_APP 消息
	msgLog      = wmApp + 1 // lParam: *string 日志行
	msgRefresh  = wmApp + 2 // 刷新状态/按钮
)

// ---------- 类型 ----------

type rect struct{ left, top, right, bottom int32 }

type msg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type trackMouseEvent struct {
	cbSize    uint32
	dwFlags   uint32
	hwndTrack syscall.Handle
	dwHoverTime uint32
}

// paintStruct 是 Win32 PAINTSTRUCT（BeginPaint/EndPaint 用）。
type paintStruct struct {
	hdc         syscall.Handle
	fErase      int32
	rcPaint     rect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

type browseInfo struct {
	hwndOwner      syscall.Handle
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// ---------- 动态库 ----------

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	gdi32   = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")
	ole32   = syscall.NewLazyDLL("ole32.dll")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	pRegisterClassExW  = user32.NewProc("RegisterClassExW")
	pCreateWindowExW   = user32.NewProc("CreateWindowExW")
	pDefWindowProcW    = user32.NewProc("DefWindowProcW")
	pGetMessageW       = user32.NewProc("GetMessageW")
	pTranslateMessage  = user32.NewProc("TranslateMessage")
	pDispatchMessageW  = user32.NewProc("DispatchMessageW")
	pShowWindow        = user32.NewProc("ShowWindow")
	pUpdateWindow      = user32.NewProc("UpdateWindow")
	pGetMessagePos     = user32.NewProc("GetMessagePos")
	pScreenToClient    = user32.NewProc("ScreenToClient")
	pGetClientRect     = user32.NewProc("GetClientRect")
	pInvalidateRect    = user32.NewProc("InvalidateRect")
	pSendMessageW      = user32.NewProc("SendMessageW")
	pPostMessageW      = user32.NewProc("PostMessageW")
	pSetWindowTextW    = user32.NewProc("SetWindowTextW")
	pLoadIconW         = user32.NewProc("LoadIconW")
	pDrawIconEx        = user32.NewProc("DrawIconEx")
	pGetDpiForWindow   = user32.NewProc("GetDpiForWindow")
	pSetCursor         = user32.NewProc("SetCursor")
	pLoadCursorW       = user32.NewProc("LoadCursorW")
	pSetCapture        = user32.NewProc("SetCapture")
	pReleaseCapture    = user32.NewProc("ReleaseCapture")
	pTrackMouseEvent   = user32.NewProc("TrackMouseEvent")
	pDestroyWindow     = user32.NewProc("DestroyWindow")
	pDestroyIcon       = user32.NewProc("DestroyIcon")
	pPostQuitMessage   = user32.NewProc("PostQuitMessage")
	pFillRect          = user32.NewProc("FillRect")
	pDrawTextW         = user32.NewProc("DrawTextW")
	pBeginPaint        = user32.NewProc("BeginPaint")
	pEndPaint          = user32.NewProc("EndPaint")
	pValidateRect      = user32.NewProc("ValidateRect")
	pSetTimer          = user32.NewProc("SetTimer")
	pKillTimer         = user32.NewProc("KillTimer")
	pGetDC             = user32.NewProc("GetDC")
	pReleaseDC         = user32.NewProc("ReleaseDC")
	pGetSysColorBrush  = user32.NewProc("GetSysColorBrush")

	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pCreateFontW      = gdi32.NewProc("CreateFontW")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pRoundRect        = gdi32.NewProc("RoundRect")
	pCreatePen        = gdi32.NewProc("CreatePen")
	pGetStockObject   = gdi32.NewProc("GetStockObject")
	pCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	pFillRgn          = gdi32.NewProc("FillRgn")
	pDeleteRgn        = gdi32.NewProc("DeleteRgn")

	pSHBrowseForFolder = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	pExtractIconExW    = shell32.NewProc("ExtractIconExW")
	pCoTaskMemFree     = ole32.NewProc("CoTaskMemFree")

	pSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	pSetWindowPos      = user32.NewProc("SetWindowPos")

	pCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt                = gdi32.NewProc("BitBlt")
	pDeleteDC              = gdi32.NewProc("DeleteDC")
	pEllipse               = gdi32.NewProc("Ellipse")
)

// gwlpUserdata 是 SetWindowLongPtrW 的 GWLP_USERDATA（-21，以 uintptr 表示）。
var gwlpUserdata = uintptr(^uintptr(20))

const (
	transparent = 1
	opaque      = 2
)

// ---------- 辅助 ----------

func utf16Ptr(s string) *uint16 {
	return syscall.StringToUTF16Ptr(s)
}

// mulDiv 模拟 MulDiv：ceil 保证控件最小尺寸。
func mulDiv(n, num, den int32) int32 {
	return (n*num + den - 1) / den
}

func makeBrush(color uint32) syscall.Handle {
	r, _, _ := pCreateSolidBrush.Call(uintptr(color))
	return syscall.Handle(r)
}

func deleteObject(h syscall.Handle) {
	_, _, _ = pDeleteObject.Call(uintptr(h))
}

func makeFont(dpi int32, pt int32, bold bool, face string) syscall.Handle {
	height := -mulDiv(pt, dpi, 72)
	weight := uintptr(0)
	if bold {
		weight = fwBold
	}
	r, _, _ := pCreateFontW.Call(
		uintptr(height), 0, 0, 0, weight, 0, 0, 0, 1 /*DEFAULT_CHARSET*/,
		0, 0, 4 /*CLEARTYPE_QUALITY*/, 0, uintptr(unsafe.Pointer(utf16Ptr(face))),
	)
	return syscall.Handle(r)
}

// drawText 在矩形内绘制文本（flags 常用 dtCenter|dtVcenter|dtSingleLine）。
func drawText(hdc syscall.Handle, s string, rc *rect, color uint32, flags uint32) {
	pSetTextColor.Call(uintptr(hdc), uintptr(color))
	pSetBkMode.Call(uintptr(hdc), transparent)
	pDrawTextW.Call(
		uintptr(hdc),
		uintptr(unsafe.Pointer(utf16Ptr(s))),
		uintptr(len(s)),
		uintptr(unsafe.Pointer(rc)),
		uintptr(flags),
	)
}

// fillRoundRect 用画刷填充圆角矩形（半径 radius）。
func fillRoundRect(hdc syscall.Handle, rc *rect, radius int32, brush syscall.Handle) {
	rgn, _, _ := pCreateRoundRectRgn.Call(
		uintptr(rc.left), uintptr(rc.top), uintptr(rc.right+1), uintptr(rc.bottom+1),
		uintptr(radius), uintptr(radius),
	)
	if rgn != 0 {
		pFillRgn.Call(uintptr(hdc), rgn, uintptr(brush))
		pDeleteObject.Call(rgn) // DeleteObject 对区域句柄同样适用
	}
}

// frameRoundRect 画圆角矩形边框（空画刷，不填充内部）。
func frameRoundRect(hdc syscall.Handle, rc *rect, radius int32, color uint32) {
	pen, _, _ := pCreatePen.Call(psSolid, 1, uintptr(color))
	if pen == 0 {
		return
	}
	old, _, _ := pSelectObject.Call(uintptr(hdc), pen)
	nullBrush, _, _ := pGetStockObject.Call(5) // NULL_BRUSH
	oldBr, _, _ := pSelectObject.Call(uintptr(hdc), nullBrush)
	pRoundRect.Call(uintptr(hdc), uintptr(rc.left), uintptr(rc.top), uintptr(rc.right), uintptr(rc.bottom), uintptr(radius*2), uintptr(radius*2))
	pSelectObject.Call(uintptr(hdc), oldBr)
	pSelectObject.Call(uintptr(hdc), old)
	pDeleteObject.Call(pen)
}

// fillRect 填充普通矩形。
func fillRect(hdc syscall.Handle, rc *rect, brush syscall.Handle) {
	pFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rc)), uintptr(brush))
}

func sendMessage(hwnd syscall.Handle, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pSendMessageW.Call(uintptr(hwnd), uintptr(msg), w, l)
	return r
}

func postMessage(hwnd syscall.Handle, msg uint32, w, l uintptr) {
	_, _, _ = pPostMessageW.Call(uintptr(hwnd), uintptr(msg), w, l)
}

// getDpi 获取窗口 DPI。
func getDpi(hwnd syscall.Handle) int32 {
	r, _, _ := pGetDpiForWindow.Call(uintptr(hwnd))
	if r == 0 {
		return 96
	}
	return int32(r)
}

// loadCursorHand 加载手型光标（按钮 hover）。
func loadCursorHand() (syscall.Handle, error) {
	r, _, err := pLoadCursorW.Call(0, 32649) // IDC_HAND
	return syscall.Handle(r), err
}

// fillCircle 画实心圆。
func fillCircle(hdc syscall.Handle, cx, cy, r int32, brush syscall.Handle) {
	old, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(brush))
	pEllipse.Call(uintptr(hdc), uintptr(cx-r), uintptr(cy-r), uintptr(cx+r), uintptr(cy+r))
	pSelectObject.Call(uintptr(hdc), old)
}
