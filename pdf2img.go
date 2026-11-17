package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gen2brain/go-fitz"
)

const (
	pdfRenderDPI = 150.0
	maxPDFPages  = 500
)

// pdfConvertOptions 是 PDF 转图片时的分辨率、页码与进度设置。
type pdfConvertOptions struct {
	DPI       float64
	StartPage int
	EndPage   int
	Progress  func(status string, fraction float64)
}

// pdfDocument 保存已打开的 PDF，页码从 1 开始。
type pdfDocument struct {
	Source    string
	Stem      string
	PageCount int

	doc *fitz.Document
	mu  sync.Mutex
}

// preparePDFDocument 打开 PDF 并读取页数，不渲染任何页面。
func preparePDFDocument(source string) (*pdfDocument, error) {
	if !strings.EqualFold(filepath.Ext(source), ".pdf") {
		return nil, fmt.Errorf("只支持 .pdf 文件")
	}
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		return nil, fmt.Errorf("找不到 PDF 文件")
	}

	parsed, err := fitz.New(source)
	if err != nil {
		if err == fitz.ErrNeedsPassword {
			return nil, fmt.Errorf("PDF 已加密，暂不支持有密码的文件")
		}
		return nil, fmt.Errorf("打开 PDF 失败: %w", err)
	}

	total := parsed.NumPage()
	if total <= 0 {
		parsed.Close()
		return nil, fmt.Errorf("PDF 没有可导出的页面")
	}
	if total > maxPDFPages {
		parsed.Close()
		return nil, fmt.Errorf("页数过多（%d），最多支持 %d 页", total, maxPDFPages)
	}

	stem := safeFileBase(strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)))
	return &pdfDocument{
		Source:    source,
		Stem:      stem,
		PageCount: total,
		doc:       parsed,
	}, nil
}

func (d *pdfDocument) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc != nil {
		_ = d.doc.Close()
		d.doc = nil
	}
}

// RenderPage 渲染一页（页码从 1 开始），dpi 为 0 时用 150。
func (d *pdfDocument) RenderPage(page int, dpi float64) (*image.RGBA, error) {
	if page < 1 || page > d.PageCount {
		return nil, fmt.Errorf("页码 %d 超出范围", page)
	}
	if dpi <= 0 {
		dpi = pdfRenderDPI
	}
	if dpi < 36 {
		dpi = 36
	}
	if dpi > 600 {
		dpi = 600
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return nil, fmt.Errorf("PDF 已关闭")
	}
	img, err := d.doc.ImageDPI(page-1, dpi)
	if err != nil {
		return nil, fmt.Errorf("渲染第 %d 页失败: %w", page, err)
	}
	return img, nil
}

// ExportPNGs 把选中的页码渲染成 PNG，输出到 PDF 同目录的 <文件名>_images。
func (d *pdfDocument) ExportPNGs(opt pdfConvertOptions) (dir string, files []string, err error) {
	start, end := pageRange(opt.StartPage, opt.EndPage, d.PageCount)

	dpi := opt.DPI
	if dpi <= 0 {
		dpi = pdfRenderDPI
	}
	if dpi < 36 {
		dpi = 36
	}
	if dpi > 600 {
		dpi = 600
	}

	progress := opt.Progress
	if progress == nil {
		progress = func(string, float64) {}
	}

	dir = filepath.Join(filepath.Dir(d.Source), d.Stem+"_images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("创建输出目录失败: %w", err)
	}

	total := end - start + 1
	digits := len(strconv.Itoa(d.PageCount))
	if digits < 3 {
		digits = 3
	}
	files = make([]string, 0, total)
	for page := start; page <= end; page++ {
		done := page - start
		progress(fmt.Sprintf("正在渲染第 %d 页（%d / %d）…", page, done+1, total),
			0.05+0.9*float64(done)/float64(total))

		img, renderErr := d.RenderPage(page, dpi)
		if renderErr != nil {
			return "", nil, renderErr
		}

		name := fmt.Sprintf("%s_%0*d.png", d.Stem, digits, page)
		path := filepath.Join(dir, name)
		file, createErr := os.Create(path)
		if createErr != nil {
			return "", nil, fmt.Errorf("创建第 %d 页失败: %w", page, createErr)
		}
		encodeErr := png.Encode(file, img)
		closeErr := file.Close()
		if encodeErr != nil {
			return "", nil, fmt.Errorf("编码第 %d 页失败: %w", page, encodeErr)
		}
		if closeErr != nil {
			return "", nil, fmt.Errorf("保存第 %d 页失败: %w", page, closeErr)
		}
		files = append(files, path)
	}
	progress("正在整理输出…", 0.98)
	return dir, files, nil
}

// convertPDFToPNGs 是 prepare + 全页渲染的便捷封装。
func convertPDFToPNGs(source string, opt pdfConvertOptions) (dir string, files []string, err error) {
	doc, err := preparePDFDocument(source)
	if err != nil {
		return "", nil, err
	}
	defer doc.Close()
	return doc.ExportPNGs(opt)
}
