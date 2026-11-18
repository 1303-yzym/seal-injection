package seal

import (
	"image"
	"image/color"
	"testing"
)

// newTestSheet 造一张“纸上盖红章”的合成图：偏黄的纸 + 灰色文字 + 红色圆环。
func newTestSheet() *image.NRGBA {
	const size = 200
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// 纸张不是纯白：偏暖、且越往下越暗（模拟光照）。
			shade := uint8(245 - y/5)
			img.Set(x, y, color.NRGBA{R: shade, G: shade - 8, B: shade - 18, A: 255})
		}
	}
	// 灰色印刷文字（不应被当成印章）。
	for y := 0; y < size; y++ {
		if y%40 < 3 {
			for x := 0; x < size; x++ {
				img.Set(x, y, color.NRGBA{R: 55, G: 55, B: 55, A: 255})
			}
		}
	}
	// 红色圆环：r 在 60..80 之间。
	cx, cy := size/2, size/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := x-cx, y-cy
			d2 := dx*dx + dy*dy
			if d2 >= 60*60 && d2 <= 80*80 {
				img.Set(x, y, color.NRGBA{R: 205, G: 38, B: 45, A: 255})
			}
		}
	}
	return img
}

func TestExtractKeepsOnlyInk(t *testing.T) {
	src := newTestSheet()
	res, err := Extract(src, Options{})
	if err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}

	cx, cy := 100, 100
	at := func(x, y int) uint8 { return res.Image.NRGBAAt(x, y).A }

	if a := at(cx, cy); a != 0 {
		t.Errorf("印章内部应为透明，实际 alpha=%d", a)
	}
	if a := at(cx, cy-70); a != 255 { // 圆环正中
		t.Errorf("圆环应完全不透明，实际 alpha=%d", a)
	}
	if a := at(5, 41); a != 0 { // 灰色文字
		t.Errorf("灰色文字不应被保留，实际 alpha=%d", a)
	}
	if a := at(18, 180); a != 0 { // 偏黄的纸面（含阴影）
		t.Errorf("纸面应为透明，实际 alpha=%d", a)
	}

	// 印泥颜色应接近原图的红。
	if res.Ink.R < 180 || res.Ink.G > 80 || res.Ink.B > 90 {
		t.Errorf("印泥颜色异常: #%02X%02X%02X", res.Ink.R, res.Ink.G, res.Ink.B)
	}
	if res.Coverage <= 0 || res.Coverage > 0.4 {
		t.Errorf("覆盖率异常: %.3f", res.Coverage)
	}
}

func TestExtractBlank(t *testing.T) {
	blank := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := range blank.Pix {
		blank.Pix[i] = 255
	}
	if _, err := Extract(blank, Options{}); err != ErrNoInk {
		t.Fatalf("纯白图片应返回 ErrNoInk，实际: %v", err)
	}
}

func TestExtractForceInk(t *testing.T) {
	ink := color.NRGBA{R: 0xC8, G: 0x32, B: 0x2B, A: 255}
	res, err := Extract(newTestSheet(), Options{Ink: &ink})
	if err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}
	got := res.Image.NRGBAAt(100, 30)
	if got.R != ink.R || got.G != ink.G || got.B != ink.B {
		t.Errorf("强制印泥颜色未生效: #%02X%02X%02X", got.R, got.G, got.B)
	}
}
