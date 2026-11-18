// Command seal-injection 把白底的红色印章图片分离成透明背景的印章，
// 再把它盖到一个 .docx 文件上。
//
// 用法：
//
//	seal-injection -seal assets/seals/demo.png -out out/seal.docx
//	seal-injection -seal seal.jpg -out out/seal.docx -size 4 -x 15 -y 23 -png out/seal.png
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	_ "image/jpeg" // 支持 jpg/jpeg 输入

	"github.com/1303-yzym/seal-injection/internal/docx"
	"github.com/1303-yzym/seal-injection/internal/seal"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	sealPath := flag.String("seal", "", "白底红色印章图片路径（png/jpg）")
	outPath := flag.String("out", filepath.Join("out", "seal.docx"), "输出的 .docx 路径")
	pngPath := flag.String("png", "", "可选：同时导出透明背景印章 PNG 的路径")
	sizeCM := flag.Float64("size", 4.0, "印章边长（厘米），宽度，高度按比例缩放")
	xCM := flag.Float64("x", 15.0, "印章左上角距页面左边的距离（厘米，A4）")
	yCM := flag.Float64("y", 23.0, "印章左上角距页面上边的距离（厘米，A4）")
	inkHex := flag.String("ink", "", "可选：强制印泥颜色，如 #C8322B")
	behind := flag.Bool("behind", false, "把印章放在文字下方（默认在文字上方）")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "印章注入器：白底红章 → 透明背景 → 盖到 .docx\n\n用法:\n  %s -seal <图片> [选项]\n\n选项:\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	if *sealPath == "" {
		flag.Usage()
		return fmt.Errorf("必须用 -seal 指定印章图片")
	}
	if *sizeCM <= 0 {
		return fmt.Errorf("-size 必须大于 0")
	}

	var ink *color.NRGBA
	if s := strings.TrimSpace(*inkHex); s != "" {
		c, err := parseHexColor(s)
		if err != nil {
			return err
		}
		ink = &c
	}

	// 1. 读图并提取红色印章。
	f, err := os.Open(*sealPath)
	if err != nil {
		return err
	}
	defer f.Close()

	src, format, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("无法解析印章图片 %s: %w", *sealPath, err)
	}

	res, err := seal.Extract(src, seal.Options{Ink: ink})
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, res.Image); err != nil {
		return fmt.Errorf("编码 PNG 失败: %w", err)
	}
	if *pngPath != "" {
		if err := writeFile(*pngPath, buf.Bytes()); err != nil {
			return err
		}
	}

	// 2. 计算尺寸与位置（EMU），保持宽高比。
	wEMU := docx.CMToEMU(*sizeCM)
	sb := res.Image.Bounds()
	hEMU := int64(float64(wEMU) * float64(sb.Dy()) / float64(sb.Dx()))
	if err := docx.WriteNew(*outPath, docx.Picture{
		PNG:        buf.Bytes(),
		WidthEMU:   wEMU,
		HeightEMU:  hEMU,
		XEMU:       docx.CMToEMU(*xCM),
		YEMU:       docx.CMToEMU(*yCM),
		BehindText: *behind,
	}); err != nil {
		return err
	}

	// 3. 输出摘要。
	fmt.Printf("源图      : %s (%s %dx%d)\n", *sealPath, format, sb.Dx(), sb.Dy())
	fmt.Printf("纸张红度  : %.3f\n", res.Background)
	fmt.Printf("印泥颜色  : #%02X%02X%02X\n", res.Ink.R, res.Ink.G, res.Ink.B)
	fmt.Printf("软阈值    : %.3f ~ %.3f\n", res.AlphaLow, res.AlphaHigh)
	fmt.Printf("印章覆盖  : %.1f%% (alpha>50%%)\n", res.Coverage*100)
	if *pngPath != "" {
		fmt.Printf("透明 PNG  : %s\n", *pngPath)
	}
	fmt.Printf("输出文档  : %s (%.2fcm x %.2fcm @ %.2f,%.2fcm)\n",
		*outPath, float64(wEMU)/docx.EMUPerCM, float64(hEMU)/docx.EMUPerCM, *xCM, *yCM)
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// parseHexColor 解析 #RRGGBB 或 RRGGBB。
func parseHexColor(s string) (color.NRGBA, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.NRGBA{}, fmt.Errorf("-ink 需要 #RRGGBB 格式，例如 #C8322B")
	}
	var rgb [3]uint8
	for i := 0; i < 3; i++ {
		var v int
		if _, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &v); err != nil {
			return color.NRGBA{}, fmt.Errorf("-ink 颜色解析失败: %q", s)
		}
		rgb[i] = uint8(v)
	}
	return color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}, nil
}
