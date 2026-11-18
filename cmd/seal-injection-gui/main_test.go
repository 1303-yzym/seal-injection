package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestImage 画一张"浅黄纸 + 红方块"的图，模拟白底红章照片。
func newTestImage(t *testing.T) []byte {
	t.Helper()
	const size = 120
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := color.NRGBA{R: 250, G: 246, B: 232, A: 255}
			if x >= 40 && x < 80 && y >= 40 && y < 80 {
				c = color.NRGBA{R: 190, G: 32, B: 40, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractAndDocxOverHTTP(t *testing.T) {
	s := &server{items: map[string]*item{}}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	// 1. 上传并提取
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("seal", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(newTestImage(t)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/api/extract", mw.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("extract 状态码 = %d", resp.StatusCode)
	}
	var info struct {
		ID       string  `json:"id"`
		Ink      string  `json:"ink"`
		Coverage float64 `json:"coverage"`
		Width    int     `json:"width"`
		Height   int     `json:"height"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.ID == "" || !strings.HasPrefix(info.Ink, "#") {
		t.Fatalf("提取结果异常: %+v", info)
	}
	if info.Width != 120 || info.Height != 120 {
		t.Fatalf("图片尺寸 = %dx%d，希望 120x120", info.Width, info.Height)
	}
	if info.Coverage <= 0 || info.Coverage > 0.3 {
		t.Fatalf("覆盖率 %.3f 不合理", info.Coverage)
	}

	// 2. 预览：纸面应该变透明，红章保持不透明
	presp, err := http.Get(ts.URL + "/api/seal?id=" + info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer presp.Body.Close()
	if ct := presp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("预览 Content-Type = %q", ct)
	}
	sealImg, err := png.Decode(presp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := sealImg.At(2, 2).RGBA(); a != 0 {
		t.Errorf("纸面像素 alpha = %d，希望 0（应完全透明）", a)
	}
	if _, _, _, a := sealImg.At(60, 60).RGBA(); a != 0xffff {
		t.Errorf("章体像素 alpha = %d，希望 65535", a)
	}

	// 3. 生成 docx
	dresp, err := http.Post(ts.URL+"/api/docx?id="+info.ID+"&size=4&x=15&y=23", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("docx 状态码 = %d", dresp.StatusCode)
	}
	if cd := dresp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("缺少附件下载头: %q", cd)
	}
	data, err := io.ReadAll(dresp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("docx 不是合法 zip: %v", err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = string(content)
	}
	for _, want := range []string{"[Content_Types].xml", "word/document.xml", "word/media/seal.png"} {
		if _, ok := parts[want]; !ok {
			t.Errorf("docx 缺少 %s", want)
		}
	}
	doc := parts["word/document.xml"]
	if !strings.Contains(doc, "<wp:anchor") {
		t.Error("document.xml 里没有浮动图片（wp:anchor）")
	}
	if !strings.Contains(doc, `cx="1440000"`) {
		t.Errorf("4 厘米宽应对应 1440000 EMU，实际 document.xml 不含该值")
	}
}

func TestExtractRejectsNonImage(t *testing.T) {
	s := &server{items: map[string]*item{}}
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("seal", "note.txt")
	fw.Write([]byte("这不是图片"))
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/extract", mw.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，希望 400", resp.StatusCode)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if e.Error == "" {
		t.Error("错误响应缺少 error 字段")
	}
}

func TestDownloadName(t *testing.T) {
	cases := map[string]string{
		"a.png":     "a-盖章.docx",
		"dir/b.jpg": "b-盖章.docx",
		"":          "seal-盖章.docx",
	}
	for in, want := range cases {
		if got := downloadName(in, "-盖章", ".docx"); got != want {
			t.Errorf("downloadName(%q) = %q，希望 %q", in, got, want)
		}
	}
}
