package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func readPart(t *testing.T, zr *zip.ReadCloser, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("打开 %s 失败: %v", name, err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("读取 %s 失败: %v", name, err)
			}
			return string(b)
		}
	}
	t.Fatalf("docx 缺少 part: %s", name)
	return ""
}

func TestWriteNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seal.docx")
	pic := Picture{
		PNG:       []byte("\x89PNG\r\n\x1a\nfake"),
		WidthEMU:  1440000,
		HeightEMU: 720000,
		XEMU:      5400000,
		YEMU:      8280000,
	}
	if err := WriteNew(path, pic); err != nil {
		t.Fatalf("WriteNew 失败: %v", err)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("生成的 docx 不是合法 zip: %v", err)
	}
	defer zr.Close()

	for _, part := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/_rels/document.xml.rels", "word/media/seal.png"} {
		readPart(t, zr, part) // 缺失会 t.Fatal
	}

	doc := readPart(t, zr, "word/document.xml")
	var probe struct{ XMLName xml.Name }
	if err := xml.Unmarshal([]byte(doc), &probe); err != nil {
		t.Fatalf("document.xml 不是合法 XML: %v", err)
	}
	for _, want := range []string{`cx="1440000"`, `cy="720000"`, `<wp:posOffset>5400000</wp:posOffset>`, `<wp:posOffset>8280000</wp:posOffset>`, `r:embed="rId1"`, `<w:sectPr>`} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml 缺少 %q", want)
		}
	}
}

func TestWriteNewRejectsEmpty(t *testing.T) {
	if err := WriteNew(filepath.Join(t.TempDir(), "x.docx"), Picture{WidthEMU: 1, HeightEMU: 1}); err == nil {
		t.Fatal("空图片数据应当报错")
	}
}

func TestCMToEMU(t *testing.T) {
	if got := CMToEMU(1); got != 360000 {
		t.Errorf("CMToEMU(1) = %d, want 360000", got)
	}
	if got := CMToEMU(4); got != 1440000 {
		t.Errorf("CMToEMU(4) = %d, want 1440000", got)
	}
}
