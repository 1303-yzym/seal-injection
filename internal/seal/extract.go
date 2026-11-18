// Package seal 负责把白底（纸面）上拍下来的红色印章分离成透明背景的印章。
//
// 核心思路：印章是红色的，纸面是近似中性色（白/灰/偏黄）。用 R - max(G, B)
// 作为“红度”，这个量对整体亮度、阴影、色温都不敏感；再减去纸张底色（取图像
// 边框区域的中位数）即可得到只属于印泥的成分，最后用软阈值把它映射成 alpha。
package seal

import (
	"errors"
	"image"
	"image/color"
	"math"
)

// ErrNoInk 表示图中检测不到红色印泥。
var ErrNoInk = errors.New("seal: 未在图片中检测到红色印泥（可能是图片不是白底红章）")

// Options 控制提取过程。
type Options struct {
	// Ink 非 nil 时强制使用该颜色作为印泥颜色，否则从图中自动估计。
	Ink *color.NRGBA
}

// Result 是提取结果。
type Result struct {
	// Image 是透明背景的印章图。
	Image *image.NRGBA
	// Ink 是估计（或指定）的印泥颜色。
	Ink color.NRGBA
	// AlphaLow/AlphaHigh 是软阈值区间 (0..1)。
	AlphaLow  float64
	AlphaHigh float64
	// Background 是纸张底色的红度估计。
	Background float64
	// Coverage 是 alpha > 50% 的像素占比。
	Coverage float64
	// Bounds 是不透明印章的外接矩形（整幅图为空时不建议使用）。
	Bounds image.Rectangle
}

const (
	histBins = 256
	// inkPercentile 用来估计“实心印泥”的红度水平。
	inkPercentile = 0.995
)

// Extract 把 src 中的红色印章提取为透明背景图。
func Extract(src image.Image, opt Options) (*Result, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("seal: 图片为空")
	}

	// 第一遍：红度直方图 + 边框区域（通常是纸面）直方图。
	var hist, borderHist [histBins]int
	band := max(3, min(w, h)/25)
	total := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			v := redness(src.At(x, y))
			hist[binIndex(v)]++
			total++
			if x-b.Min.X < band || b.Max.X-1-x < band || y-b.Min.Y < band || b.Max.Y-1-y < band {
				borderHist[binIndex(v)]++
			}
		}
	}
	if total == 0 {
		return nil, errors.New("seal: 图片为空")
	}

	bg := percentile(&borderHist, 0.5)
	inkLevel := percentile(&hist, inkPercentile) - bg
	if inkLevel < 0.06 {
		return nil, ErrNoInk
	}

	// 软阈值：低于 lo 完全透明，高于 hi 完全不透明，中间线性过渡，避免锯齿。
	lo := clamp(0.10*inkLevel, 0.02, 0.25)
	hi := clamp(0.45*inkLevel, lo+0.03, 0.85)

	// 第二遍：计算 alpha，并统计实心像素的颜色分布。
	alpha := make([]uint8, w*h)
	var cr, cg, cb [histBins]int
	solid := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			adj := math.Max(0, redness(src.At(x, y))-bg)
			a := smoothstep(lo, hi, adj)
			av := uint8(math.Round(a * 255))
			alpha[(y-b.Min.Y)*w+(x-b.Min.X)] = av
			if av >= 242 {
				r, g, bl := nrgba(src.At(x, y))
				cr[binIndex(r)]++
				cg[binIndex(g)]++
				cb[binIndex(bl)]++
				solid++
			}
		}
	}
	if solid == 0 {
		return nil, ErrNoInk
	}

	ink := color.NRGBA{
		R: to8(percentile(&cr, 0.5)),
		G: to8(percentile(&cg, 0.5)),
		B: to8(percentile(&cb, 0.5)),
		A: 255,
	}
	if opt.Ink != nil {
		ink = *opt.Ink
		ink.A = 255
	}

	// 第三遍：按统一的印泥颜色 + alpha 合成，去掉纸面的色偏与纹理。
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	bounds := image.Rectangle{}
	opaque := 0
	for i, av := range alpha {
		out.Pix[i*4+0] = ink.R
		out.Pix[i*4+1] = ink.G
		out.Pix[i*4+2] = ink.B
		out.Pix[i*4+3] = av
		if av == 0 {
			continue
		}
		x, y := i%w, i/w
		if bounds.Empty() {
			bounds = image.Rect(x, y, x+1, y+1)
		} else {
			bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
		}
		if av > 127 {
			opaque++
		}
	}

	return &Result{
		Image:      out,
		Ink:        ink,
		AlphaLow:   lo,
		AlphaHigh:  hi,
		Background: bg,
		Coverage:   float64(opaque) / float64(total),
		Bounds:     bounds,
	}, nil
}

// redness 返回像素的“红度” R - max(G, B)，范围 0..1。
// 使用非预乘的 RGBA 分量，避免透明 PNG 的边缘被误判。
func redness(c color.Color) float64 {
	r, g, b := nrgba(c)
	return r - math.Max(g, b)
}

// nrgba 把任意 color.Color 转成 0..1 的非预乘 RGB 分量。
func nrgba(c color.Color) (float64, float64, float64) {
	r16, g16, b16, a16 := c.RGBA()
	if a16 == 0 {
		// 全透明像素视为纸面（红度 0）。
		return 1, 1, 1
	}
	f := float64(a16)
	return float64(r16) / f, float64(g16) / f, float64(b16) / f
}

func binIndex(v float64) int {
	i := int(v * float64(histBins-1))
	if i < 0 {
		return 0
	}
	if i >= histBins {
		return histBins - 1
	}
	return i
}

// percentile 返回直方图的第 p 分位数（取区间中点，0..1）。
func percentile(h *[histBins]int, p float64) float64 {
	total := 0
	for _, c := range h {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := int(math.Ceil(p * float64(total)))
	acc := 0
	for i, c := range h {
		acc += c
		if acc >= target {
			return (float64(i) + 0.5) / float64(histBins-1)
		}
	}
	return 1
}

// to8 把 0..1 的分量转成 0..255。
func to8(v float64) uint8 {
	return uint8(math.Round(clamp(v, 0, 1) * 255))
}

func smoothstep(lo, hi, x float64) float64 {
	if hi <= lo {
		if x >= hi {
			return 1
		}
		return 0
	}
	t := (x - lo) / (hi - lo)
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

func clamp(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}
