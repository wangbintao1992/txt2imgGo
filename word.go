package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
	"golang.org/x/image/font"
)

const libreOfficeDownloadURL = "https://zh-cn.libreoffice.org/download/libreoffice/"

const (
	wordRenderDPI = 150.0
	maxWordPages  = 500
)

// wordConvertOptions 是 Word 转 PDF 时的水印、页码与进度设置。
type wordConvertOptions struct {
	Watermark        string
	WatermarkSize    float64
	WatermarkOpacity float64
	WatermarkAngle   float64
	Font             FontRef
	// StartPage / EndPage 是 1 起始、包含两端的页码；0 表示首页 / 末页。
	StartPage int
	EndPage   int
	Progress  func(status string, fraction float64)
}

// wordDocument 是已经用 LibreOffice 转好的 PDF，可以反复预览和渲染。
type wordDocument struct {
	Source    string
	Stem      string
	TempDir   string
	PDFPath   string
	PageCount int
	data      []byte
}

// Close 删除转换用的临时目录。
func (d *wordDocument) Close() {
	if d == nil || d.TempDir == "" {
		return
	}
	_ = os.RemoveAll(d.TempDir)
	d.TempDir = ""
	d.data = nil
}

// findSoffice 依次查找环境变量、PATH 与各平台常见安装位置。
func findSoffice() string {
	if custom := strings.TrimSpace(os.Getenv("SOFFICE_PATH")); custom != "" {
		custom = expandHome(custom)
		if fileExists(custom) {
			return custom
		}
	}
	for _, name := range []string{"soffice", "libreoffice"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	for _, path := range sofficeCandidates() {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func sofficeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		home := os.Getenv("HOME")
		return []string{
			"/Applications/LibreOffice.app/Contents/MacOS/soffice",
			filepath.Join(home, "Applications/LibreOffice.app/Contents/MacOS/soffice"),
		}
	case "windows":
		return []string{
			`C:\Program Files\LibreOffice\program\soffice.exe`,
			`C:\Program Files (x86)\LibreOffice\program\soffice.exe`,
		}
	default:
		return []string{
			"/usr/bin/soffice",
			"/usr/local/bin/soffice",
			"/usr/lib/libreoffice/program/soffice",
			"/usr/lib64/libreoffice/program/soffice",
			"/snap/bin/libreoffice",
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// prepareWordDocument 用 LibreOffice 把 Word 转成 PDF，并读出总页数。
// 结果保存在临时目录里，调用方用完要 Close。
func prepareWordDocument(sofficePath, source string, progress func(status string, fraction float64)) (*wordDocument, error) {
	if sofficePath == "" {
		return nil, fmt.Errorf("未检测到 LibreOffice")
	}
	switch strings.ToLower(filepath.Ext(source)) {
	case ".docx", ".doc":
	default:
		return nil, fmt.Errorf("只支持 .docx / .doc 文件")
	}
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		return nil, fmt.Errorf("找不到 Word 文件")
	}

	if progress == nil {
		progress = func(string, float64) {}
	}

	tmp, err := os.MkdirTemp("", "txt2img-word-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	doc := &wordDocument{
		Source:  source,
		Stem:    safeFileBase(strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))),
		TempDir: tmp,
	}
	ok := false
	defer func() {
		if !ok {
			doc.Close()
		}
	}()

	progress("正在启动 LibreOffice…", 0.15)
	input := source
	if strings.EqualFold(filepath.Ext(source), ".docx") {
		cleaned, cleanErr := sanitizeWordDOCX(source, filepath.Join(tmp, "input.docx"))
		if cleanErr != nil {
			log.Printf("[word] 清理段落框失败，按原文件转换: %v", cleanErr)
		} else {
			input = cleaned
		}
	}
	if err := runLibreOfficePDF(sofficePath, tmp, input); err != nil {
		return nil, err
	}

	pdfPath, err := findPDF(tmp)
	if err != nil {
		return nil, err
	}
	progress("正在读取 PDF…", 0.65)
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("读取转换结果失败: %w", err)
	}
	parsed, err := reader.Open(data)
	if err != nil {
		return nil, fmt.Errorf("解析 LibreOffice 生成的 PDF 失败: %w", err)
	}
	total := parsed.PageCount()
	if total <= 0 {
		return nil, fmt.Errorf("Word 没有可导出的页面")
	}
	if total > maxWordPages {
		return nil, fmt.Errorf("页数过多（%d），最多支持 %d 页", total, maxWordPages)
	}

	doc.PDFPath = pdfPath
	doc.data = data
	doc.PageCount = total
	ok = true
	return doc, nil
}

// inlineFrameRe 匹配没有实际定位信息、只声明「随文」的段落框。
// 一些导出工具会给每个段落都加上这种 framePr；LibreOffice 会把它当成
// 独立文本框，结果正文被拆成一页一段，页面上往往只剩标题。
var inlineFrameRe = regexp.MustCompile(`<w:framePr\b[^>]*w:yAlign="inline"[^>]*/>`)

func stripInlineFrameProps(data []byte) []byte {
	if !bytes.Contains(data, []byte("<w:framePr")) {
		return data
	}
	return inlineFrameRe.ReplaceAll(data, nil)
}

// sanitizeWordDOCX 复制 docx 到 dest，并去掉「随文」framePr，
// 避免 LibreOffice 把正文拆成一页一段。原文件不会被改动。
func sanitizeWordDOCX(source, dest string) (string, error) {
	r, err := zip.OpenReader(source)
	if err != nil {
		return "", fmt.Errorf("打开 Word 文件失败: %w", err)
	}
	defer r.Close()

	out, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("创建临时 Word 文件失败: %w", err)
	}
	zw := zip.NewWriter(out)
	writeErr := func() error {
		for _, f := range r.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
			if strings.HasPrefix(f.Name, "word/") && strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
				data = stripInlineFrameProps(data)
			}
			hdr := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified}
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			if _, err := w.Write(data); err != nil {
				return err
			}
		}
		return nil
	}()
	if writeErr == nil {
		writeErr = zw.Close()
	} else {
		_ = zw.Close()
	}
	if closeErr := out.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("整理 Word 文件失败: %w", writeErr)
	}
	return dest, nil
}

// pageRange 把用户输入的页码夹到 1..total，并保证 start <= end。
func pageRange(start, end, total int) (int, int) {
	if total < 1 {
		total = 1
	}
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > total {
		end = total
	}
	if start > total {
		start = total
	}
	if end < 1 {
		end = 1
	}
	if start > end {
		start, end = end, start
	}
	return start, end
}

// RenderPage 渲染一页（含当前水印设置），用于预览。
func (d *wordDocument) RenderPage(page int, opt wordConvertOptions) (*image.RGBA, error) {
	if page < 1 || page > d.PageCount {
		return nil, fmt.Errorf("页码 %d 超出范围", page)
	}
	faces := newWordFaces(opt.Font)
	defer faces.Close()
	return d.renderPage(page, opt, faces)
}

// ExportPDF 把选中的页码渲染成带水印的 PDF，保存为 Word 同目录的同名 PDF。
func (d *wordDocument) ExportPDF(opt wordConvertOptions) (path string, pages int, err error) {
	start, end := pageRange(opt.StartPage, opt.EndPage, d.PageCount)

	watermark := strings.TrimSpace(opt.Watermark)
	if watermark != "" && opt.Font.Path == "" {
		return "", 0, fmt.Errorf("未找到可用字体，无法生成水印")
	}
	faces := newWordFaces(opt.Font)
	defer faces.Close()

	progress := opt.Progress
	if progress == nil {
		progress = func(string, float64) {}
	}

	path = filepath.Join(filepath.Dir(d.Source), d.Stem+".pdf")
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return "", 0, fmt.Errorf("创建 PDF 失败: %w", err)
	}

	total := end - start + 1
	progress(fmt.Sprintf("正在导出第 %d 页（1 / %d）…", start, total), 0.05)
	writeErr := writeImagePagesPDF(file, total, wordRenderDPI, func(i int) (*image.RGBA, error) {
		page := start + i
		if i > 0 {
			progress(fmt.Sprintf("正在导出第 %d 页（%d / %d）…", page, i+1, total),
				0.05+0.9*float64(i)/float64(total))
		}
		return d.renderPage(page, opt, faces)
	}, PDFSecurity{}, nil)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(tmp)
		return "", 0, writeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", 0, fmt.Errorf("保存 PDF 失败: %w", err)
	}
	progress("正在整理输出…", 0.98)
	return path, total, nil
}

func (d *wordDocument) renderPage(page int, opt wordConvertOptions, faces *wordFaces) (*image.RGBA, error) {
	doc, err := reader.Open(d.data)
	if err != nil {
		return nil, fmt.Errorf("解析 PDF 失败: %w", err)
	}
	rasterPage, err := renderPDFPage(doc, page)
	if err != nil {
		return nil, err
	}
	img := nrgbaToRGBA(rasterPage)

	watermark := strings.TrimSpace(opt.Watermark)
	if watermark == "" {
		return img, nil
	}
	face, err := faces.get(img.Bounds().Dx(), opt)
	if err != nil {
		return nil, fmt.Errorf("生成水印失败: %w", err)
	}
	drawDiagonalWatermark(img, watermark, face, opt.WatermarkAngle, opt.WatermarkOpacity)
	return img, nil
}

// wordFaces 按页面像素宽度缓存水印字体，避免每页重复加载。
type wordFaces struct {
	font  FontRef
	faces map[int]font.Face
}

func newWordFaces(ref FontRef) *wordFaces {
	return &wordFaces{font: ref, faces: make(map[int]font.Face)}
}

func (f *wordFaces) get(width int, opt wordConvertOptions) (font.Face, error) {
	if face, ok := f.faces[width]; ok {
		return face, nil
	}
	if f.font.Path == "" {
		return nil, fmt.Errorf("未找到可用字体")
	}
	size := opt.WatermarkSize
	if size <= 0 {
		size = 46
	}
	size *= float64(width) / float64(pageWidth)
	if size < 12 {
		size = 12
	}
	face, err := newFontFace(f.font, size)
	if err != nil {
		return nil, err
	}
	f.faces[width] = face
	return face, nil
}

func (f *wordFaces) Close() {
	if f == nil {
		return
	}
	for _, face := range f.faces {
		face.Close()
	}
}

// convertWordToPDF 是 prepare + 导出 PDF 的便捷封装。
func convertWordToPDF(sofficePath, source string, opt wordConvertOptions) (path string, pages int, err error) {
	doc, err := prepareWordDocument(sofficePath, source, opt.Progress)
	if err != nil {
		return "", 0, err
	}
	defer doc.Close()
	return doc.ExportPDF(opt)
}

func runLibreOfficePDF(sofficePath, workDir, source string) error {
	profileDir := filepath.Join(workDir, "profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fmt.Errorf("创建 LibreOffice 配置目录失败: %w", err)
	}
	profileURL := (&url.URL{Scheme: "file", Path: profileDir}).String()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, sofficePath,
		"--headless",
		"--invisible",
		"--nologo",
		"--nodefault",
		"--norestore",
		"--nolockcheck",
		"--nofirststartwizard",
		"-env:UserInstallation="+profileURL,
		"--convert-to", "pdf",
		"--outdir", workDir,
		source,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("LibreOffice 转换超时（3 分钟）")
	}
	if runErr != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = runErr.Error()
		}
		if runes := []rune(message); len(runes) > 300 {
			message = string(runes[len(runes)-300:])
		}
		log.Printf("[word] LibreOffice 失败: %v: %s", runErr, message)
		return fmt.Errorf("LibreOffice 转换失败: %s", message)
	}
	return nil
}

func findPDF(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("读取临时目录失败: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("LibreOffice 没有生成 PDF")
}

func renderPDFPage(doc *reader.Document, page int) (img *raster.Image, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("渲染第 %d 页失败: %v", page, recovered)
		}
	}()
	rendered, err := render.Page(doc, page, render.Options{
		DPI:         wordRenderDPI,
		MaxDuration: 90 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("渲染第 %d 页失败: %w", page, err)
	}
	return rendered, nil
}

// nrgbaToRGBA 把直通 alpha 的像素缓冲复制成 draw 可用的 RGBA 图。
func nrgbaToRGBA(src *raster.Image) *image.RGBA {
	nrgba := src.ToNRGBA()
	dst := image.NewRGBA(image.Rect(0, 0, nrgba.Bounds().Dx(), nrgba.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), nrgba, nrgba.Bounds().Min, draw.Src)
	return dst
}
