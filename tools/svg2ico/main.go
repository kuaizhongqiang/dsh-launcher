// svg2ico：一次性工具，把 dsh 的 favicon.svg（单一 path 矢量）光栅化为多尺寸
// ICO 文件（16/24/32/48/64/128/256，PNG 内嵌格式，Vista+ 支持）。
//
// 用法：go run ./tools/svg2ico <in.svg> <out.ico>
//
// 仅支持本图标用到的 SVG 子集：单个 <path>，指令 M/L/C/Z（绝对/相对），
// 非零填充规则。足够渲染 DeepSeek Harness 的 favicon。
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ---------- SVG path 解析 ----------

type pt struct{ x, y float64 }

type seg struct {
	a, b pt // 线段（贝塞尔已采样）
}

type subpath struct {
	segs []seg
}

// parsePath 把 d 属性解析为子路径列表（折线化）。
func parsePath(d string) []subpath {
	// tokenize：命令字母与数字
	re := regexp.MustCompile(`[MLCZmlcz]|-?\d*\.?\d+(?:[eE][+-]?\d+)?`)
	toks := re.FindAllString(d, -1)

	var subs []subpath
	var cur subpath
	curX, curY := 0.0, 0.0
	startX, startY := 0.0, 0.0
	first := true // 是否在子路径首个点

	consume := func(n int) []float64 {
		out := []float64{}
		for len(toks) > 0 && len(out) < n {
			if _, err := strconv.ParseFloat(toks[0], 64); err != nil {
				break
			}
			v, _ := strconv.ParseFloat(toks[0], 64)
			toks = toks[1:]
			out = append(out, v)
		}
		return out
	}

	flushSub := func() {
		if len(cur.segs) > 0 {
			subs = append(subs, cur)
			cur = subpath{}
		}
	}

	cubic := func(p0, p1, p2, p3 pt) {
		// 8 段折线采样
		const n = 8
		prev := p0
		for i := 1; i <= n; i++ {
			t := float64(i) / n
			mt := 1 - t
			x := mt*mt*mt*p0.x + 3*mt*mt*t*p1.x + 3*mt*t*t*p2.x + t*t*t*p3.x
			y := mt*mt*mt*p0.y + 3*mt*mt*t*p1.y + 3*mt*t*t*p2.y + t*t*t*p3.y
			np := pt{x, y}
			cur.segs = append(cur.segs, seg{prev, np})
			prev = np
		}
	}

	for len(toks) > 0 {
		cmd := toks[0]
		toks = toks[1:]
		switch cmd {
		case "M", "m":
			flushSub()
			nums := consume(2)
			if len(nums) < 2 {
				continue
			}
			if cmd == "m" {
				curX += nums[0]
				curY += nums[1]
			} else {
				curX, curY = nums[0], nums[1]
			}
			startX, startY = curX, curY
			first = true
			px, py := curX, curY // 上一个点（M 后隐式 L 用）
			// M 后多余坐标对按隐式 L 处理
			for {
				nums = consume(2)
				if len(nums) < 2 {
					break
				}
				nx, ny := nums[0], nums[1]
				if cmd == "m" {
					nx, ny = curX+nx, curY+ny
				}
				cur.segs = append(cur.segs, seg{pt{px, py}, pt{nx, ny}})
				px, py, curX, curY = nx, ny, nx, ny
				first = false
			}
		case "L", "l":
			for {
				nums := consume(2)
				if len(nums) < 2 {
					break
				}
				nx, ny := nums[0], nums[1]
				if cmd == "l" {
					nx, ny = curX+nx, curY+ny
				}
				if !first {
					cur.segs = append(cur.segs, seg{pt{curX, curY}, pt{nx, ny}})
				}
				curX, curY = nx, ny
				first = false
			}
		case "C", "c":
			for {
				nums := consume(6)
				if len(nums) < 6 {
					break
				}
				c1x, c1y := nums[0], nums[1]
				c2x, c2y := nums[2], nums[3]
				ex, ey := nums[4], nums[5]
				if cmd == "c" {
					c1x, c1y = curX+c1x, curY+c1y
					c2x, c2y = curX+c2x, curY+c2y
					ex, ey = curX+ex, curY+ey
				}
				if !first {
					cubic(pt{curX, curY}, pt{c1x, c1y}, pt{c2x, c2y}, pt{ex, ey})
				}
				curX, curY = ex, ey
				first = false
			}
		case "Z", "z":
			if !first && (curX != startX || curY != startY) {
				cur.segs = append(cur.segs, seg{pt{curX, curY}, pt{startX, startY}})
			}
			curX, curY = startX, startY
			first = false
		}
	}
	flushSub()
	return subs
}

// ---------- 光栅化（非零填充，扫描线） ----------

type edge struct {
	y0, y1 float64
	x0, x1 float64
	dir    int // +1 向下，-1 向上
}

func render(subs []subpath, size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	scale := float64(size) / 50.0 // viewBox 50x50

	var edges []edge
	for _, sp := range subs {
		for _, s := range sp.segs {
			a := pt{s.a.x * scale, s.a.y * scale}
			b := pt{s.b.x * scale, s.b.y * scale}
			if a.y == b.y {
				continue // 水平边不参与扫描
			}
			e := edge{}
			if a.y < b.y {
				e = edge{a.y, b.y, a.x, b.x, 1}
			} else {
				e = edge{b.y, a.y, b.x, a.x, -1}
			}
			edges = append(edges, e)
		}
	}

	// 逐扫描行（像素中心）
	for py := 0; py < size; py++ {
		y := float64(py) + 0.5
		type xw struct{ x float64; w int }
		var active []xw
		for _, e := range edges {
			if y >= e.y0 && y < e.y1 {
				t := (y - e.y0) / (e.y1 - e.y0)
				x := e.x0 + t*(e.x1-e.x0)
				active = append(active, xw{x, e.dir})
			}
		}
		// 按 x 排序
		for i := 1; i < len(active); i++ {
			for j := i; j > 0 && active[j].x < active[j-1].x; j-- {
				active[j], active[j-1] = active[j-1], active[j]
			}
		}
		// 非零规则填充
		w := 0
		for i := 0; i < len(active); i++ {
			if w != 0 {
				x0 := int(math.Floor(active[i-1].x))
				x1 := int(math.Ceil(active[i].x))
				if x0 < 0 {
					x0 = 0
				}
				if x1 > size {
					x1 = size
				}
				for px := x0; px < x1; px++ {
					img.SetRGBA(px, py, white)
				}
			}
			w += active[i].w
		}
	}
	return img
}

// ---------- ICO 打包（PNG 内嵌） ----------

func encodePNG(img image.Image) []byte {
	var buf strings.Builder
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return []byte(buf.String())
}

func buildICO(sizes []int, imgs map[int]*image.RGBA) []byte {
	var out []byte
	// ICONDIR
	out = append(out, 0, 0, 1, 0)
	out = append(out, byte(len(sizes)), 0)

	offset := 6 + 16*len(sizes)
	var blobs [][]byte
	for _, s := range sizes {
		blob := encodePNG(imgs[s])
		blobs = append(blobs, blob)
		// ICONDIRENTRY
		w, h := byte(s), byte(s)
		if s >= 256 {
			w, h = 0, 0
		}
		out = append(out, w, h, 0, 0)
		out = append(out, 1, 0, 32, 0) // planes=1, bpp=32
		out = append(out, byte(len(blob)), byte(len(blob)>>8), byte(len(blob)>>16), byte(len(blob)>>24))
		out = append(out, byte(offset), byte(offset>>8), byte(offset>>16), byte(offset>>24))
		offset += len(blob)
	}
	for _, b := range blobs {
		out = append(out, b...)
	}
	return out
}

func main() {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		fmt.Fprintln(os.Stderr, "usage: svg2ico <in.svg> <out.ico> [preview.png]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	re := regexp.MustCompile(`<path[^>]*\bd="([^"]+)"`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		fmt.Fprintln(os.Stderr, "no path found in svg")
		os.Exit(1)
	}
	subs := parsePath(m[1])
	fmt.Printf("parsed %d subpaths\n", len(subs))

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	imgs := map[int]*image.RGBA{}
	for _, s := range sizes {
		imgs[s] = render(subs, s)
	}
	if len(os.Args) >= 4 { // 可选第 3 参数：输出 256px 预览 PNG 便于人工检查
		var buf strings.Builder
		if err := png.Encode(&buf, imgs[256]); err != nil {
			fmt.Fprintln(os.Stderr, "preview:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[3], []byte(buf.String()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "preview write:", err)
			os.Exit(1)
		}
		fmt.Println("wrote preview", os.Args[3])
	}
	ico := buildICO(sizes, imgs)
	if err := os.WriteFile(os.Args[2], ico, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, %d sizes)\n", os.Args[2], len(ico), len(sizes))
}
