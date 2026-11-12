package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo"
)

// WordDocxPayload 把选中的 docx 送到 WebView 渲染页。
type WordDocxPayload struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// WordDocx 通知渲染页开始转换。
var WordDocx = mygo.NewEvent[WordDocxPayload]("word:docx")

// WordRender 是 Word 转图片的 RPC 服务：前端渲染完把 PNG 交回来。
type WordRender struct {
	app *app

	mu     sync.Mutex
	source string
}

func (w *WordRender) setSource(path string) {
	w.mu.Lock()
	w.source = path
	w.mu.Unlock()
}

// Log 把前端日志写到 Go 进程的标准错误，便于排查渲染卡住的阶段。
func (w *WordRender) Log(message string) {
	log.Printf("[word] %s", message)
}

// Report 更新原生界面的阶段、页码与进度。
func (w *WordRender) Report(stage string, done int, total int) {
	if w.app != nil {
		w.app.reportWord(stage, done, total)
	}
}

// Fail 让原生界面结束等待并显示错误。
func (w *WordRender) Fail(message string) {
	if w.app != nil {
		w.app.finishWordError(message)
	}
}

// SavePages 把前端回传的 base64 PNG 依次保存到 Word 文件旁的 <名字>_images 目录。
func (w *WordRender) SavePages(pages []string) (string, error) {
	if len(pages) == 0 {
		return "", fmt.Errorf("没有收到图片")
	}
	if len(pages) > 500 {
		return "", fmt.Errorf("页数过多（%d），最多支持 500 页", len(pages))
	}
	w.mu.Lock()
	source := w.source
	w.mu.Unlock()
	if source == "" {
		return "", fmt.Errorf("请先选择 Word 文件")
	}

	stem := safeFileBase(strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)))
	dir := filepath.Join(filepath.Dir(source), stem+"_images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	digits := len(strconv.Itoa(len(pages)))
	if digits < 3 {
		digits = 3
	}
	for i, encoded := range pages {
		if comma := strings.Index(encoded, ","); strings.HasPrefix(encoded, "data:") && comma >= 0 {
			encoded = encoded[comma+1:]
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("第 %d 页解码失败: %w", i+1, err)
		}
		name := fmt.Sprintf("%s_%0*d.png", stem, digits, i+1)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return "", fmt.Errorf("保存第 %d 页失败: %w", i+1, err)
		}
	}
	if w.app != nil {
		w.app.finishWordSuccess(dir, len(pages))
	}
	return dir, nil
}
