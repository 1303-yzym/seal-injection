// Command seal-injection-gui 是印章注入器的图形界面：
// 启动一个只监听本机的网页服务并自动打开浏览器，选图 → 预览透明印章 → 生成 .docx。
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "image/jpeg" // 支持 jpg 输入

	"github.com/1303-yzym/seal-injection/internal/docx"
	"github.com/1303-yzym/seal-injection/internal/seal"
)

//go:embed web/index.html
var webFS embed.FS

// maxUpload 限制上传图片大小。
const maxUpload = 32 << 20

// item 保存一次提取的结果，供预览与下载复用。
type item struct {
	name    string // 原始文件名，用来生成默认下载名
	srcData []byte // 原始图片字节，预览“原图”时直接返回，避免重新编码
	srcType string
	sealPNG []byte
	width   int
	height  int
}

type server struct {
	mu    sync.Mutex
	items map[string]*item
	order []string // 按插入顺序，用来淘汰旧结果
	seq   int64
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7788", "监听地址")
	noBrowser := flag.Bool("no-browser", false, "不自动打开浏览器")
	flag.Parse()

	ln, err := listen(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法启动服务:", err)
		os.Exit(1)
	}

	srv := &server{items: map[string]*item{}}
	httpSrv := &http.Server{
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	u := "http://" + ln.Addr().String() + "/"
	fmt.Println("印章注入器已启动:", u)
	fmt.Println("在浏览器里操作即可；关掉窗口后按 Ctrl+C 或点页面上的“退出”结束。")

	if !*noBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(u)
		}()
	}

	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "服务异常退出:", err)
		os.Exit(1)
	}
}

// listen 优先用指定端口，被占用时就退回随机端口。
func listen(addr string) (net.Listener, error) {
	if ln, err := net.Listen("tcp", addr); err == nil {
		return ln, nil
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/extract", s.handleExtract)
	mux.HandleFunc("/api/source", s.handleSource)
	mux.HandleFunc("/api/seal", s.handleSealPNG)
	mux.HandleFunc("/api/docx", s.handleDocx)
	mux.HandleFunc("/api/png", s.handleDownloadPNG)
	mux.HandleFunc("/api/quit", s.handleQuit)
	return mux
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// handleExtract 接收图片，提取红色印章，返回结果编号与统计信息。
func (s *server) handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "读取上传内容失败："+err.Error())
		return
	}
	file, header, err := r.FormFile("seal")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "没有收到图片")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "读取图片失败："+err.Error())
		return
	}
	srcType := http.DetectContentType(raw)

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "无法解析图片："+err.Error())
		return
	}

	res, err := seal.Extract(img, seal.Options{})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var sealBuf bytes.Buffer
	if err := png.Encode(&sealBuf, res.Image); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sb := res.Image.Bounds()
	it := &item{
		name:    header.Filename,
		srcData: raw,
		srcType: srcType,
		sealPNG: sealBuf.Bytes(),
		width:   sb.Dx(),
		height:  sb.Dy(),
	}

	id := strconv.FormatInt(atomic.AddInt64(&s.seq, 1), 10)
	s.mu.Lock()
	s.items[id] = it
	s.order = append(s.order, id)
	// 只保留最近 8 次结果，避免内存无限增长。
	for len(s.order) > 8 {
		delete(s.items, s.order[0])
		s.order = s.order[1:]
	}
	s.mu.Unlock()

	writeJSON(w, map[string]any{
		"id":       id,
		"ink":      fmt.Sprintf("#%02X%02X%02X", res.Ink.R, res.Ink.G, res.Ink.B),
		"coverage": res.Coverage,
		"width":    it.width,
		"height":   it.height,
	})
}

func (s *server) get(r *http.Request) (*item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[r.URL.Query().Get("id")]
	return it, ok
}

func (s *server) handleSource(w http.ResponseWriter, r *http.Request) {
	it, ok := s.get(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", it.srcType)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(it.srcData)
}

func (s *server) handleSealPNG(w http.ResponseWriter, r *http.Request) {
	it, ok := s.get(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(it.sealPNG)
}

// handleDocx 生成只含该印章的 .docx 并作为附件下载。
func (s *server) handleDocx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	it, ok := s.get(r)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "结果已过期，请重新选择图片")
		return
	}
	q := r.URL.Query()
	sizeCM := queryFloat(q.Get("size"), 4)
	xCM := queryFloat(q.Get("x"), 15)
	yCM := queryFloat(q.Get("y"), 23)
	behind := q.Get("behind") == "1"
	if sizeCM <= 0 || sizeCM > 20 {
		writeJSONError(w, http.StatusBadRequest, "印章宽度需要在 0~20 厘米之间")
		return
	}

	wEMU := docx.CMToEMU(sizeCM)
	hEMU := int64(float64(wEMU) * float64(it.height) / float64(it.width))

	data, err := docx.Bytes(docx.Picture{
		PNG:        it.sealPNG,
		WidthEMU:   wEMU,
		HeightEMU:  hEMU,
		XEMU:       docx.CMToEMU(xCM),
		YEMU:       docx.CMToEMU(yCM),
		BehindText: behind,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	setAttachment(w, downloadName(it.name, "-盖章", ".docx"))
	w.Write(data)
}

func (s *server) handleDownloadPNG(w http.ResponseWriter, r *http.Request) {
	it, ok := s.get(r)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "结果已过期，请重新选择图片")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	setAttachment(w, downloadName(it.name, "-透明印章", ".png"))
	w.Write(it.sealPNG)
}

func (s *server) handleQuit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("bye"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	}()
}

func queryFloat(s string, def float64) float64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

func downloadName(orig, suffix, ext string) string {
	base := "seal"
	if orig != "" {
		base = strings.TrimSuffix(filepath.Base(orig), filepath.Ext(orig))
	}
	return base + suffix + ext
}

func setAttachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
