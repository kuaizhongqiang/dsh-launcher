package ui

import (
	"syscall"
	"unsafe"

	"dsh-launcher/internal/log"
)

const (
	srcCopy   = 0x00CC0020
	nullBrush = 5 // GetStockObject(NULL_BRUSH)
)

// onPaint 双缓冲绘制整个界面。
// 必须用 BeginPaint/EndPaint：BeginPaint 会清除无效区域，
// 用 GetDC 会导致 WM_PAINT 无限重发（消息队列被占满 → UI 未响应）。
func (u *uiState) onPaint() {
	log.Debug("UI: WM_PAINT enter")
	defer log.Debug("UI: WM_PAINT leave")
	// 防重入：若正在绘制（如绘制中触发了新的 WM_PAINT），跳过本次
	if !u.painting.CompareAndSwap(false, true) {
		return
	}
	defer u.painting.Store(false)
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(uintptr(u.hwnd), uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		// BeginPaint 失败：至少清除无效区域，否则 WM_PAINT 会无限重发（风暴→卡死）
		pValidateRect.Call(uintptr(u.hwnd), 0)
		log.Warn("UI: BeginPaint 失败，已清除无效区域")
		return
	}
	defer pEndPaint.Call(uintptr(u.hwnd), uintptr(unsafe.Pointer(&ps)))

	L := u.lay()
	w := L.titleBar.right
	// 客户区高度
	rc := rect{0, 0, w, 0}
	pGetClientRect.Call(uintptr(u.hwnd), uintptr(unsafe.Pointer(&rc)))
	hgt := rc.bottom

	// 双缓冲
	memDC, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(hgt))
	oldBmp, _, _ := pSelectObject.Call(memDC, bmp)

	u.paintAll(syscall.Handle(memDC), w, hgt)

	pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(hgt), memDC, 0, 0, srcCopy)
	pSelectObject.Call(memDC, oldBmp)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(memDC)
}

func (u *uiState) paintAll(hdc syscall.Handle, w, h int32) {
	L := u.lay()

	// 背景
	bg := rect{0, 0, w, h}
	fillRect(hdc, &bg, u.brushBg)

	// 标题栏
	u.paintTitleBar(hdc, &L)

	// 状态卡片
	u.paintCard(hdc, &L)

	// 安装目录行：标签（路径框是子控件自动绘制）
	oldFont, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(u.fontUI))
	drawText(hdc, "安装目录", &L.pathLbl, colTextDim, dtSingleLine|dtVcenter)
	pSelectObject.Call(uintptr(hdc), oldFont)

	// 按钮
	u.paintButton(hdc, &L.browse, u.buttons[btnBrowse], "浏览…", styleSec)
	u.paintButton(hdc, &L.install, u.buttons[btnInstall], "安装", styleSec)
	u.paintButton(hdc, &L.move, u.buttons[btnMove], "移动", styleSec)
	u.paintButton(hdc, &L.startBtn, u.buttons[btnStart], u.startLabel(), stylePrimary)
	u.paintButton(hdc, &L.stopBtn, u.buttons[btnStop], "停止", styleSec)
	u.paintButton(hdc, &L.exitBtn, u.buttons[btnExit], "退出", styleSec)
	u.paintButton(hdc, &L.closeBtn, u.buttons[btnClose], "×", styleClose)
}

// ---------- 标题栏 ----------

func (u *uiState) paintTitleBar(hdc syscall.Handle, L *layout) {
	// 关闭按钮（由 paintButton 负责），这里画图标与标题（单行，垂直居中，不拥挤）
	if u.icon != 0 {
		pDrawIconEx.Call(uintptr(hdc),
			uintptr(L.titleBar.left+sc(u.dpi, 16)), uintptr(sc(u.dpi, 10)),
			uintptr(u.icon), uintptr(sc(u.dpi, 24)), uintptr(sc(u.dpi, 24)),
			0, 0, diNormal)
	}
	titleRc := rect{L.titleBar.left + sc(u.dpi, 48), L.titleBar.top + sc(u.dpi, 2), L.titleBar.right - sc(u.dpi, 70), L.titleBar.bottom - sc(u.dpi, 2)}
	oldFont, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(u.fontTitle))
	drawText(hdc, windowTitle, &titleRc, colText, dtSingleLine|dtVcenter)
	pSelectObject.Call(uintptr(hdc), oldFont)
}

func sc(dpi int32, v int32) int32 { return int32(float32(v) * float32(dpi) / 96.0) }

// ---------- 状态卡片 ----------

func (u *uiState) paintCard(hdc syscall.Handle, L *layout) {
	fillRoundRect(hdc, &L.card, sc(u.dpi, 10), u.brushPanel)
	frameRoundRect(hdc, &L.card, sc(u.dpi, 10), colBorder)

	oldFont, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(u.fontSub))
	drawText(hdc, "环境状态", &L.cardTxt, colTextDim, dtSingleLine|dtVcenter)
	pSelectObject.Call(uintptr(hdc), oldFont)

	rows := []struct {
		text  string
		color uint32
	}{
		{u.nodeLine, u.nodeCol},
		{u.npmLine, u.npmCol},
		{u.dshLine, u.dshCol},
		{u.portLine, u.portCol},
	}
	oldFont2, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(u.fontUI))
	for i, r := range rows {
		rc := L.lines[i]
		// 状态圆点
		dotColor := colTextDim
		if r.color == colGreen {
			dotColor = colGreen
		} else if r.color == colRed {
			dotColor = colRed
		}
		dotBrush := u.brushSec
		if dotColor == colGreen {
			dotBrush = u.brushGreen
		} else if dotColor == colRed {
			dotBrush = u.brushRed
		}
		cy := (rc.top + rc.bottom) / 2
		fillCircle(hdc, rc.left+sc(u.dpi, 6), cy, sc(u.dpi, 4), dotBrush)
		// 文本
		trc := rect{rc.left + sc(u.dpi, 18), rc.top, rc.right, rc.bottom}
		drawText(hdc, r.text, &trc, r.color, dtSingleLine|dtVcenter)
	}
	pSelectObject.Call(uintptr(hdc), oldFont2)
}

// ---------- 按钮自绘 ----------

type btnStyle int

const (
	stylePrimary btnStyle = iota
	styleSec
	styleClose
)

func (u *uiState) startLabel() string {
	if u.isRunning() {
		return "已运行"
	}
	return "启动"
}

func (u *uiState) btnDisabled(k btnKind) bool {
	switch k {
	case btnStart:
		return u.isBusy()
	case btnStop:
		return u.isBusy() || !u.isRunning()
	case btnInstall, btnBrowse, btnMove:
		return u.isBusy()
	}
	return false
}

func (u *uiState) paintButton(hdc syscall.Handle, rc *rect, b *btnUI, text string, style btnStyle) {
	radius := sc(u.dpi, 6)
	disabled := u.btnDisabled(b.kind)

	fill := u.brushSec
	textColor := uint32(colText)
	switch style {
	case stylePrimary:
		fill = u.brushPri
		if disabled {
			fill = u.brushSec
			textColor = colTextDim
		} else if b.pressed {
			fill = u.brushPriP
		} else if b.hover {
			fill = u.brushPriH
		}
	case styleSec:
		if disabled {
			fill = u.brushSec
			textColor = colTextDim
		} else if b.pressed {
			fill = u.brushSecP
		} else if b.hover {
			fill = u.brushSecH
		}
	case styleClose:
		if b.pressed {
			fill = u.brushDanger
		} else if b.hover {
			fill = u.brushDanger
			textColor = colText
		} else {
			fill = u.brushBg
			textColor = colTextDim
		}
		radius = 0
	}

	fillRoundRect(hdc, rc, radius, fill)
	if style != styleClose {
		frameRoundRect(hdc, rc, radius, colBorder)
	}

	oldFont, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(u.fontUI))
	drawText(hdc, text, rc, textColor, dtCenter|dtVcenter|dtSingleLine)
	pSelectObject.Call(uintptr(hdc), oldFont)
}
