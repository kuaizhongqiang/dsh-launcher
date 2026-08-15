package ui

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"dsh-launcher/internal/config"
	"dsh-launcher/internal/launch"
	"dsh-launcher/internal/log"
	"dsh-launcher/internal/node"
)

const (
	className = "dshLauncherMainWnd"
	windowTitle = "dsh-launcher"
	baseW, baseH = 700, 480
)

// ---------- 按钮 ----------

type btnKind int

const (
	btnClose btnKind = iota
	btnBrowse
	btnInstall
	btnMove
	btnStart
	btnStop
	btnExit
)

type btnUI struct {
	kind    btnKind
	hover   bool
	pressed bool // 鼠标按下且在本按钮内
}

// ---------- 状态 ----------

type uiState struct {
	hwnd syscall.Handle
	hPath syscall.Handle // 安装目录编辑框
	hLog  syscall.Handle // 日志编辑框

	dpi   int32
	fontUI    syscall.Handle
	fontTitle syscall.Handle
	fontSub   syscall.Handle
	fontLog   syscall.Handle
	brushBg    syscall.Handle
	brushPanel syscall.Handle
	brushInput syscall.Handle
	brushPri   syscall.Handle
	brushPriH  syscall.Handle
	brushPriP  syscall.Handle
	brushSec   syscall.Handle
	brushSecH  syscall.Handle
	brushSecP  syscall.Handle
	brushDanger syscall.Handle
	brushGreen syscall.Handle
	brushRed   syscall.Handle
	brushTitleH syscall.Handle
	cursorHand syscall.Handle
	icon       syscall.Handle

	buttons map[btnKind]*btnUI

	mu      sync.Mutex
	busy    bool // 安装/启动/停止中
	running atomic.Bool // dsh 运行中（refreshStatus 定期刷新）

	// 状态行（UI 线程读，刷新 goroutine 写——由 msgRefresh 传递，故仅 UI 线程读写）
	nodeLine, npmLine, dshLine, portLine string
	nodeCol,  npmCol,  dshCol,  portCol  uint32

	logCh chan string // 日志行队列（后台 → UI 线程）
	logCount int     // 日志框当前行数（限行用）
	lastHoverPaint time.Time // hover 重绘限流（UI 线程）
	painting  atomic.Bool // WM_PAINT 防重入
	lastMsg   atomic.Uint32 // 最后处理的窗口消息（卡死诊断）
	inMsg     atomic.Int64 // 消息处理开始时间戳（0=不在处理中），看门狗用
	quit      atomic.Bool // 消息循环退出标志（closeApp 直接 os.Exit，正常不会走到）
	unsub     func()
	hb        atomic.Int64 // 兼容保留（不再用于看门狗判定）
	modal     atomic.Bool  // 模态对话框（浏览目录）进行中，看门狗豁免
	modalFrom atomic.Int64 // 模态开始时间（UnixNano），超时兜底
}

const wmTimer = 0x0113

// maxLogLines 日志框最多保留的行数（超出清空，避免长文本拖慢编辑框）。
const maxLogLines = 600

// watchdog 后台看门狗：检测"单条消息处理耗时"——窗口过程卡住（inMsg 停留）
// 超过 30 秒 → 自动退出；模态对话框期间豁免，但超过 60 秒（目录框卡死）也退出。
// 闲置（GetMessage 阻塞）inMsg 为 0，不会误判。
// 触发时 dump goroutine 栈，帮助定位卡点。
func (u *uiState) watchdog() {
	for {
		time.Sleep(3 * time.Second)
		if u.modal.Load() {
			from := time.Unix(0, u.modalFrom.Load())
			if time.Since(from) > 60*time.Second {
				log.Error("目录选择框无响应超过 60 秒，自动退出。最后处理消息 %#x", u.lastMsg.Load())
				os.Exit(1)
			}
			continue
		}
		if t := u.inMsg.Load(); t != 0 {
			if time.Since(time.Unix(0, t)) > 30*time.Second {
				buf := make([]byte, 64*1024)
				n := runtime.Stack(buf, true)
				log.Error("窗口消息处理卡住超过 30 秒，自动退出。最后处理消息 %#x。goroutine 栈：\n%s",
					u.lastMsg.Load(), buf[:n])
				os.Exit(1)
			}
		}
	}
}

// ---------- 布局 ----------

type layout struct {
	titleBar rect
	closeBtn rect
	card     rect
	cardTxt  rect
	lines    [4]rect
	pathLbl  rect
	pathBox  rect
	browse   rect
	install  rect
	move     rect
	logBox   rect
	startBtn rect
	stopBtn  rect
	exitBtn  rect
}

func (u *uiState) lay() layout {
	s := float32(u.dpi) / 96.0
	sc := func(v int32) int32 { return int32(float32(v) * s) }
	w := sc(baseW)
	L := layout{
		titleBar: rect{0, 0, w, sc(44)},
		closeBtn: rect{w - sc(56), 0, w, sc(44)},
		card:     rect{sc(16), sc(60), w - sc(16), sc(196)},
		cardTxt:  rect{sc(28), sc(68), w - sc(28), sc(92)},
		pathLbl:  rect{sc(16), sc(208), sc(84), sc(232)},
		pathBox:  rect{sc(84), sc(204), w - sc(316), sc(230)},
		browse:   rect{w - sc(308), sc(202), w - sc(228), sc(232)},
		install:  rect{w - sc(220), sc(202), w - sc(140), sc(232)},
		move:     rect{w - sc(132), sc(202), w - sc(52), sc(232)},
		logBox:   rect{sc(16), sc(244), w - sc(16), sc(404)},
		startBtn: rect{sc(16), sc(418), sc(146), sc(458)},
		stopBtn:  rect{sc(154), sc(418), sc(274), sc(458)},
		exitBtn:  rect{w - sc(116), sc(418), w - sc(16), sc(458)},
	}
	baseY := int32(100)
	for i := 0; i < 4; i++ {
		L.lines[i] = rect{sc(28), sc(baseY + int32(i)*24), w - sc(28), sc(baseY + int32(i)*24 + 20)}
	}
	return L
}

// ---------- 窗口生命周期 ----------

var (
	registerOnce sync.Once
	wndProc      = syscall.NewCallback(wndProcGo)
	hInstance    syscall.Handle
	mainUI       *uiState // 单窗口应用
)

func Run() int {
	if r, _, _ := pGetModuleHandleW.Call(0); r != 0 {
		hInstance = syscall.Handle(r)
	}

	registerOnce.Do(func() {
		cls := wndClassEx{
			cbSize:      uint32(unsafe.Sizeof(wndClassEx{})),
			style:       0,
			lpfnWndProc: wndProc,
			hInstance:   hInstance,
			hCursor:     loadArrow(),
			lpszClassName: utf16Ptr(className),
		}
		cls.hIcon, _ = loadAppIcon()
		cls.hIconSm = cls.hIcon
		if r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls))); r == 0 {
			return
		}
	})

	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(className))),
		uintptr(unsafe.Pointer(utf16Ptr(windowTitle))),
		wsPopup|wsVisible|wsClipChildren|wsClipSiblings,
		0x80000000 /*CW_USEDEFAULT*/, 0x80000000,
		0, 0, // 尺寸在创建后按 DPI 设置
		0, 0, uintptr(hInstance), 0,
	)
	if hwnd == 0 {
		return 1
	}
	u := &uiState{hwnd: syscall.Handle(hwnd), buttons: map[btnKind]*btnUI{}}
	for _, k := range []btnKind{btnClose, btnBrowse, btnInstall, btnMove, btnStart, btnStop, btnExit} {
		u.buttons[k] = &btnUI{kind: k}
	}
	mainUI = u
	u.onCreate(u.hwnd) // 创建子控件、资源，启动状态刷新
	go u.watchdog()     // UI 看门狗：卡死自动退出

	pShowWindow.Call(hwnd, swShow)
	pUpdateWindow.Call(hwnd)

	// 消息循环：GetMessage 阻塞等待消息（闲置时阻塞是正常的，不是卡死）。
	// 看门狗检测的是"单条消息处理耗时"（inMsg），而不是心跳——
	// 心跳在闲置时会停，会把"正常闲置"误判为"卡死"。
	var m msg
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	if u.unsub != nil {
		u.unsub()
	}
	u.destroyResources()
	return 0
}

func loadArrow() syscall.Handle {
	r, _, _ := pLoadCursorW.Call(0, 32512) // IDC_ARROW
	return syscall.Handle(r)
}

// loadAppIcon 提取 exe 自带的第一个图标（ExtractIconExW，按索引不依赖资源 ID）。
func loadAppIcon() (syscall.Handle, error) {
	var large, small syscall.Handle
	n, _, _ := pExtractIconExW.Call(
		uintptr(hInstance), 0,
		uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1,
	)
	if n == 0 {
		// 兜底：系统应用图标
		r, _, _ := pLoadIconW.Call(0, 32512)
		return syscall.Handle(r), nil
	}
	if small != 0 {
		pDestroyIcon.Call(uintptr(small))
	}
	return large, nil
}

// ---------- 窗口过程 ----------

func wndProcGo(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	// 窗口过程内任何 panic 都兜底为默认处理，绝不把崩溃带进系统回调
	defer func() {
		if r := recover(); r != nil {
			if mainUI != nil {
				mainUI.appendLog("窗口过程异常：" + fmt.Sprint(r) + "\n")
			}
		}
	}()
	if mainUI == nil {
		r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
		return r
	}
	return mainUI.wndProc(hwnd, msg, wParam, lParam)
}

func (u *uiState) wndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	// 记录消息处理开始（看门狗：处理卡住超过 30 秒判定卡死）
	u.lastMsg.Store(msg)
	u.inMsg.Store(time.Now().UnixNano())
	defer u.inMsg.Store(0)
	// 慢消息诊断：处理超过 500ms 记录警告（帮助定位卡点）
	start := time.Now()
	defer func() {
		if d := time.Since(start); d > 500*time.Millisecond {
			log.Warn("UI: 消息 %#x 处理耗时 %v", msg, d)
		}
	}()
	switch msg {
	case wmTimer:
		return 0
	case wmPaint:
		u.onPaint()
		return 0
	case wmEraseBkgnd:
		return 1 // 全部由 WM_PAINT 绘制，避免闪烁
	case wmNcHitTest:
		x := int32(lParam & 0xFFFF)
		y := int32(lParam>>16) & 0xFFFF
		pt := point{x, y}
		pScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
		L := u.lay()
		if pt.y < L.titleBar.bottom && !inRect(&L.closeBtn, pt.x, pt.y) {
			return htCaption
		}
		return htClient
	case wmMouseMove:
		u.onMouseMove(lParam)
		return 0
	case wmLButtonDown:
		log.Debug("UI: WM_LBUTTONDOWN enter")
		u.onLButtonDown(lParam)
		log.Debug("UI: WM_LBUTTONDOWN leave")
		return 0
	case wmLButtonUp:
		log.Debug("UI: WM_LBUTTONUP enter")
		u.onLButtonUp(lParam)
		log.Debug("UI: WM_LBUTTONUP leave")
		return 0
	case wmCtlColorEdit, wmCtlColorStat:
		// 深色编辑框/只读框
		pSetTextColor.Call(wParam, uintptr(colText))
		pSetBkColor.Call(wParam, uintptr(colInput))
		return uintptr(u.brushInput)
	case wmApp + 1:
		// 日志行：一次最多消费 200 条，避免队列积压时长时间占用 UI 线程
		for i := 0; i < 200; i++ {
			select {
			case line := <-u.logCh:
				u.appendLog(line)
			default:
				return 0
			}
		}
		return 0
	case wmApp + 2:
		log.Debug("UI: refresh")
		u.invalidateAll()
		return 0
	case wmClose:
		u.closeApp()
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

type point struct{ x, y int32 }

func inRect(r *rect, x, y int32) bool {
	return x >= r.left && x < r.right && y >= r.top && y < r.bottom
}

// ---------- 创建与资源 ----------

func (u *uiState) onCreate(hwnd syscall.Handle) {
	u.hwnd = hwnd
	u.dpi = getDpi(hwnd)
	s := float32(u.dpi) / 96.0

	// 看门狗心跳定时器（每秒唤醒消息循环一次，证明 UI 线程存活）
	pSetTimer.Call(uintptr(hwnd), 1, 1000, 0)
	u.hb.Store(time.Now().UnixNano())

	// 按 DPI 设置窗口尺寸
	w := int32(float32(baseW) * s)
	h := int32(float32(baseH) * s)
	pSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, uintptr(w), uintptr(h), 0x0004 /*SWP_NOMOVE*/|0x0010 /*SWP_NOZORDER*/)

	// 字体
	u.fontUI = makeFont(u.dpi, 11, false, "Microsoft YaHei UI")
	u.fontTitle = makeFont(u.dpi, 13, true, "Microsoft YaHei UI")
	u.fontSub = makeFont(u.dpi, 9, false, "Microsoft YaHei UI")
	u.fontLog = makeFont(u.dpi, 11, false, "Consolas")

	// 画刷
	u.brushBg = makeBrush(colBg)
	u.brushPanel = makeBrush(colPanel)
	u.brushInput = makeBrush(colInput)
	u.brushPri = makeBrush(colPrimary)
	u.brushPriH = makeBrush(colPrimaryHov)
	u.brushPriP = makeBrush(colPrimaryPrs)
	u.brushSec = makeBrush(colSec)
	u.brushSecH = makeBrush(colSecHov)
	u.brushSecP = makeBrush(colSecPrs)
	u.brushDanger = makeBrush(colDanger)
	u.brushGreen = makeBrush(colGreen)
	u.brushRed = makeBrush(colRed)
	u.brushTitleH = makeBrush(colTitleHover)

	u.cursorHand, _ = loadCursorHand()
	u.icon, _ = loadAppIcon()

	// 子控件：安装目录编辑框 + 日志编辑框
	L := u.lay()
	u.hPath = createEdit(u.hwnd, &L.pathBox, 1, 0)
	u.hLog = createEdit(u.hwnd, &L.logBox, 2, esMultiLine|esReadOnly|esAutoVScroll|wsVScroll)
	// 日志框默认字体
	sendMessage(u.hLog, 0x30 /*WM_SETFONT*/, uintptr(u.fontLog), 1)

	// 初始路径显示
	if cfg, err := config.Load(); err == nil && cfg.IsInstalled() {
		pSetWindowTextW.Call(uintptr(u.hPath), uintptr(unsafe.Pointer(utf16Ptr(cfg.DshInstallDir))))
	}

	// 日志订阅 → 推送到窗口（经 channel 中转，避免跨线程指针传递）
	u.logCh = make(chan string, 256)
	u.unsub = log.Subscribe(func(line string) {
		select {
		case u.logCh <- line:
			postMessage(u.hwnd, msgLog, 0, 0)
		default:
			// 队列满则丢弃（界面显示滞后可接受）
		}
	})

	go u.refreshStatus()
}

func createEdit(parent syscall.Handle, rc *rect, id int32, style uint32) syscall.Handle {
	h, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
		0,
		uintptr(wsChild|wsVisible|esLeft|style),
		uintptr(rc.left), uintptr(rc.top),
		uintptr(rc.right-rc.left), uintptr(rc.bottom-rc.top),
		uintptr(parent), uintptr(id), 0, 0,
	)
	return syscall.Handle(h)
}

func (u *uiState) destroyResources() {
	for _, h := range []syscall.Handle{u.fontUI, u.fontTitle, u.fontSub, u.fontLog} {
		deleteObject(h)
	}
	for _, h := range []syscall.Handle{u.brushBg, u.brushPanel, u.brushInput, u.brushPri, u.brushPriH,
		u.brushPriP, u.brushSec, u.brushSecH, u.brushSecP, u.brushDanger, u.brushGreen, u.brushRed, u.brushTitleH} {
		deleteObject(h)
	}
}

// ---------- 按钮交互 ----------

func (u *uiState) hitBtn(x, y int32) btnKind {
	L := u.lay()
	switch {
	case inRect(&L.closeBtn, x, y):
		return btnClose
	case inRect(&L.browse, x, y):
		return btnBrowse
	case inRect(&L.install, x, y):
		return btnInstall
	case inRect(&L.move, x, y):
		return btnMove
	case inRect(&L.startBtn, x, y):
		return btnStart
	case inRect(&L.stopBtn, x, y):
		return btnStop
	case inRect(&L.exitBtn, x, y):
		return btnExit
	}
	return -1
}

func (u *uiState) onMouseMove(lParam uintptr) {
	x, y := mouseXY(lParam)
	k := u.hitBtn(x, y)
	changed := false
	for kind, b := range u.buttons {
		if (kind == k) != b.hover {
			b.hover = kind == k
			changed = true
		}
	}
	if changed {
		if k >= 0 {
			// 捕获离开通知
			tme := trackMouseEvent{cbSize: uint32(unsafe.Sizeof(trackMouseEvent{})), dwFlags: tmeLeave, hwndTrack: u.hwnd}
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			pSetCursor.Call(uintptr(u.cursorHand))
		}
		// 重绘限流：鼠标在按钮边缘抖动会高频触发 hover 变化，
		// 全窗口重绘风暴会拖死 UI 线程。80ms 内只重绘一次。
		now := time.Now()
		if now.Sub(u.lastHoverPaint) >= 80*time.Millisecond {
			u.lastHoverPaint = now
			u.invalidateAll()
		}
	}
}

func (u *uiState) onLButtonDown(lParam uintptr) {
	x, y := mouseXY(lParam)
	k := u.hitBtn(x, y)
	if k < 0 {
		return
	}
	u.buttons[k].pressed = true
	pSetCapture.Call(uintptr(u.hwnd))
	u.invalidateAll()
}

func (u *uiState) onLButtonUp(lParam uintptr) {
	pReleaseCapture.Call()
	x, y := mouseXY(lParam)
	for _, b := range u.buttons {
		if b.pressed {
			b.pressed = false
			if u.hitBtn(x, y) == b.kind {
				u.onClick(b.kind)
			}
			break
		}
	}
	u.invalidateAll()
}

func mouseXY(lParam uintptr) (int32, int32) {
	return int32(lParam & 0xFFFF), int32(lParam>>16) & 0xFFFF
}

func (u *uiState) onClick(k btnKind) {
	switch k {
	case btnClose, btnExit:
		u.closeApp()
	case btnBrowse:
		if dir := browseFolder(u.hwnd); dir != "" {
			pSetWindowTextW.Call(uintptr(u.hPath), uintptr(unsafe.Pointer(utf16Ptr(dir))))
		}
	case btnInstall:
		u.onInstall()
	case btnMove:
		u.onMove()
	case btnStart:
		u.onStart()
	case btnStop:
		u.onStop()
	}
}

func (u *uiState) closeApp() {
	// 解绑后 dsh 独立运行，关闭窗口不影响 dsh
	if u.running.Load() {
		log.Info("dsh 仍在后台运行（独立进程）。需要停止时请用「停止」按钮或 stop 命令。")
	}
	u.quit.Store(true)
	os.Exit(0)
}

// ---------- 状态查询 ----------

func (u *uiState) isBusy() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.busy
}

func (u *uiState) isRunning() bool {
	return u.running.Load()
}

func (u *uiState) setBusy(b bool) {
	u.mu.Lock()
	u.busy = b
	u.mu.Unlock()
}

func (u *uiState) setRunning(b bool) {
	u.running.Store(b)
}

func (u *uiState) pathText() string {
	n := sendMessage(u.hPath, 0x000E /*WM_GETTEXTLENGTH*/, 0, 0)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	sendMessage(u.hPath, 0x000D /*WM_GETTEXT*/, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func (u *uiState) setPath(dir string) {
	pSetWindowTextW.Call(uintptr(u.hPath), uintptr(unsafe.Pointer(utf16Ptr(dir))))
}

func (u *uiState) appendLog(line string) {
	// 日志框限行：超过 maxLogLines 行时清空（保留最新），避免长文本拖慢 EM_REPLACESEL
	u.logCount++
	if u.logCount > maxLogLines {
		sendMessage(u.hLog, emSetSel, 0, ^uintptr(0))
		sendMessage(u.hLog, emReplaceSel, 0, 0)
		u.logCount = 0
	}
	sendMessage(u.hLog, emSetSel, ^uintptr(0), ^uintptr(0))
	sendMessage(u.hLog, emReplaceSel, 0, uintptr(unsafe.Pointer(utf16Ptr(line))))
	sendMessage(u.hLog, emScrollCaret, 0, 0)
}

func (u *uiState) invalidateAll() {
	pInvalidateRect.Call(uintptr(u.hwnd), 0, 1)
}

// refreshStatus 在后台刷新环境状态文本，完成后通知 UI。
// refreshStatus 刷新环境状态（node/npm 探测一次）并定期更新端口运行状态（每 3 秒）。
func (u *uiState) refreshStatus() {
	ni, err := node.Detect()
	if err != nil {
		u.nodeLine = "Node.js   未安装"
		u.nodeCol = colRed
		u.npmLine = "npm       未安装"
		u.npmCol = colRed
	} else {
		u.nodeLine = "Node.js   v" + ni.NodeVer.String()
		u.nodeCol = colText
		u.npmLine = "npm       " + ni.NPMVer
		u.npmCol = colText
	}
	for {
		cfg, err := config.Load()
		if err != nil || !cfg.IsInstalled() {
			u.dshLine = "dsh       未安装"
			u.dshCol = colRed
			u.portLine = "端口      -"
			u.portCol = colTextDim
			u.setRunning(false)
		} else {
			u.dshLine = "dsh       " + cfg.DshVersion + "  已安装"
			u.dshCol = colGreen
			if launch.IsRunning(cfg) {
				u.portLine = "端口      " + itoa(cfg.Port) + "  运行中"
				u.portCol = colGreen
				u.setRunning(true)
			} else {
				u.portLine = "端口      " + itoa(cfg.Port) + "  未运行"
				u.portCol = colTextDim
				u.setRunning(false)
			}
		}
		postMessage(u.hwnd, wmApp+2, 0, 0)
		time.Sleep(3 * time.Second)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
