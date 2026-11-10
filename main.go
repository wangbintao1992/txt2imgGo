package main

import (
	"fmt"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	windowTitle      = "TXT 转图片"
	leftPaneWidth    = 410
	leftContentWidth = 392
	previewMaxW      = 620
	previewMaxH      = 877
	systemFontName   = "系统默认"
)

type app struct {
	win *mygo.Window

	sourcePath string
	text       string
	outputDir  string

	splitParagraphs bool
	charsPerLine    float64
	outputWidth     float64
	outputHeight    float64

	fontName    string
	fonts       []fontOption
	fontLabels  []string
	fontRefs    map[string]FontRef
	systemFont  FontRef
	fontsLoaded bool

	watermark        string
	watermarkSize    float64
	watermarkOpacity float64
	watermarkAngle   float64

	page       int
	pages      int
	preview    *ui.Bitmap
	previewErr string

	status    string
	statusErr bool

	generating   bool
	exportingPDF bool
	progress     float64

	renderSeq atomic.Uint64
}

func newApp() *app {
	ref, _ := detectSystemFont()
	a := &app{
		splitParagraphs:  true,
		charsPerLine:     30,
		outputWidth:      pageWidth,
		outputHeight:     pageHeight,
		fontName:         systemFontName,
		systemFont:       ref,
		watermarkSize:    46,
		watermarkOpacity: 14,
		watermarkAngle:   -30,
		status:           "拖入或选择 TXT 文件开始",
	}
	a.setFontOptions([]fontOption{{Label: systemFontName, Ref: ref}})
	return a
}

func (a *app) setFontOptions(options []fontOption) {
	if len(options) == 0 {
		options = []fontOption{{Label: systemFontName}}
	}
	a.fonts = options
	a.fontLabels = make([]string, len(options))
	a.fontRefs = make(map[string]FontRef, len(options))
	found := false
	for i, opt := range options {
		a.fontLabels[i] = opt.Label
		a.fontRefs[opt.Label] = opt.Ref
		if opt.Label == a.fontName {
			found = true
		}
	}
	if !found {
		a.fontName = options[0].Label
	}
	if a.systemFont.Path == "" && len(options) > 0 {
		a.systemFont = options[0].Ref
	}
}

func (a *app) currentFont() (FontRef, bool) {
	if ref, ok := a.fontRefs[a.fontName]; ok && ref.Path != "" {
		return ref, true
	}
	if ref, ok := a.fontRefs[systemFontName]; ok && ref.Path != "" {
		return ref, true
	}
	if a.systemFont.Path != "" {
		return a.systemFont, true
	}
	for _, opt := range a.fonts {
		if opt.Ref.Path != "" {
			return opt.Ref, true
		}
	}
	return FontRef{}, false
}

func (a *app) startFontScan() {
	if a.win == nil {
		return
	}
	a.fontsLoaded = false
	go func() {
		options := scanFontOptions(a.systemFont, systemFontName)
		a.win.Update(func() {
			a.fontsLoaded = true
			if len(options) == 0 {
				return
			}
			if a.systemFont.Path == "" {
				a.systemFont = options[0].Ref
				options = append([]fontOption{{Label: systemFontName, Ref: options[0].Ref}}, options...)
			} else if options[0].Label != systemFontName {
				options = append([]fontOption{{Label: systemFontName, Ref: a.systemFont}}, options...)
			}
			selected := a.fontName
			a.setFontOptions(options)
			a.fontName = selected
			if _, ok := a.fontRefs[a.fontName]; !ok {
				a.fontName = systemFontName
			}
			a.schedulePreview(0)
		})
	}()
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	statusColor := t.TextMuted
	if a.statusErr {
		statusColor = t.Danger
	} else if a.generating {
		statusColor = t.Accent
	}

	ui.Column(c).Fill().Padding(20).Gap(14).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, windowTitle).FontSize(26).Bold()
				ui.Text(c, "纯 Go 文本排版 · 拖入文件 · 实时预览 · 斜向水印").FontSize(12).TextColor(t.TextMuted)
			})
			ui.Box(c).Grow(1)
			ui.Text(c, a.status).FontSize(12).TextColor(statusColor).TextAlign(ui.End).MaxWidth(500)
		})

		ui.Row(c).Fill().Gap(18).Children(func() {
			ui.Column(c).Width(leftPaneWidth).FillHeight().Gap(10).Children(func() {
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).Width(leftContentWidth).Gap(12).Children(func() {
						a.dropZone(c)
						a.settingsPanel(c)
						a.outputPanel(c)
					})
				})
				a.actionButtons(c)
			})
			a.previewPanel(c)
		})
	})
}

func (a *app) dropZone(c *ui.Context) {
	t := c.Theme()
	zone := ui.Column(c).Width(leftContentWidth).Height(108).Padding(12).Gap(5).
		Radius(12).Background(t.Surface).Border(1, t.Border).
		AlignItems(ui.Center).Justify(ui.Center).Disabled(a.generating)

	if files := zone.DroppedFiles(); files != nil {
		a.handleDroppedFiles(files)
	}
	if zone.FileDragOver() {
		zone.Border(2, t.Accent)
	}

	zone.Children(func() {
		if a.sourcePath == "" {
			ui.Text(c, "拖入 TXT 文件").FontSize(17).Bold()
			ui.Text(c, "支持 UTF-8 / GB18030，或点击下方按钮选择").FontSize(11).TextColor(t.TextMuted)
		} else {
			ui.Text(c, filepath.Base(a.sourcePath)).FontSize(15).Bold().MaxLines(1)
			ui.Textf(c, "%d 字符 · 共 %d 张图片", utf8.RuneCountInString(a.text), a.pages).
				FontSize(11).TextColor(t.TextMuted)
		}
		label := "选择 TXT 文件"
		if a.sourcePath != "" {
			label = "更换文件"
		}
		if ui.Button(c, label).Width(150).Clicked() {
			a.chooseTXT()
		}
	})
}

func (a *app) settingsPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(leftContentWidth).Padding(16).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Disabled(a.generating).Children(func() {
		ui.Text(c, "排版设置").FontSize(14).Bold()

		row := ui.Row(c).Gap(12).AlignItems(ui.Center)
		beforeSplit := a.splitParagraphs
		row.Children(func() {
			ui.Text(c, "按换行符区分段落").Width(142).FontSize(13)
			ui.Box(c).Grow(1)
			ui.Switch(c, &a.splitParagraphs)
		})
		if beforeSplit != a.splitParagraphs {
			a.schedulePreview(220 * time.Millisecond)
		}

		beforeChars := a.charsPerLine
		a.settingRow(c, "每行字数", func() {
			ui.NumberInput(c, &a.charsPerLine, 1, 100, 1).Width(92)
			ui.Text(c, "字 / 行").FontSize(11).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
		})
		if beforeChars != a.charsPerLine {
			a.schedulePreview(220 * time.Millisecond)
		}

		beforeFont := a.fontName
		a.settingRow(c, "字体", func() {
			ui.Select(c, &a.fontName, a.fontLabels).Grow(1)
		})
		if beforeFont != a.fontName {
			a.schedulePreview(220 * time.Millisecond)
		}
		if !a.fontsLoaded {
			ui.Text(c, "正在读取系统字体列表…").FontSize(11).TextColor(t.TextMuted)
		}

		beforeWatermark := a.watermark
		a.settingRow(c, "水印文字", func() {
			ui.TextInput(c, &a.watermark).Placeholder("例如：内部资料").Grow(1)
		})
		if beforeWatermark != a.watermark {
			a.schedulePreview(220 * time.Millisecond)
		}

		beforeWatermarkSize := a.watermarkSize
		a.settingRow(c, "水印字号", func() {
			ui.NumberInput(c, &a.watermarkSize, 12, 160, 1).Width(92)
			ui.Text(c, "px").FontSize(11).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
		})
		if beforeWatermarkSize != a.watermarkSize {
			a.schedulePreview(220 * time.Millisecond)
		}

		beforeOpacity := a.watermarkOpacity
		a.settingRow(c, "透明度", func() {
			ui.Slider(c, &a.watermarkOpacity, 5, 100).Step(1).Grow(1)
			ui.Textf(c, "%.0f%%", a.watermarkOpacity).Width(42).FontSize(11).
				TextColor(t.TextMuted).TextAlign(ui.End)
		})
		if beforeOpacity != a.watermarkOpacity {
			a.schedulePreview(220 * time.Millisecond)
		}

		beforeAngle := a.watermarkAngle
		a.settingRow(c, "倾斜角度", func() {
			ui.Slider(c, &a.watermarkAngle, -75, 75).Step(1).Grow(1)
			ui.Textf(c, "%.0f°", a.watermarkAngle).Width(42).FontSize(11).
				TextColor(t.TextMuted).TextAlign(ui.End)
		})
		if beforeAngle != a.watermarkAngle {
			a.schedulePreview(220 * time.Millisecond)
		}
		ui.Text(c, "负值向右上倾斜，正值向右下倾斜；水印为半透明平铺").FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *app) settingRow(c *ui.Context, label string, fn func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(78).FontSize(13)
		fn()
	})
}

func (a *app) outputPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(leftContentWidth).Padding(16).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Disabled(a.generating).Children(func() {
		ui.Text(c, "输出设置").FontSize(14).Bold()

		beforeWidth, beforeHeight := a.outputWidth, a.outputHeight
		a.settingRow(c, "图片宽度", func() {
			ui.NumberInput(c, &a.outputWidth, minPageWidth, maxPageWidth, 1).Width(120)
			ui.Text(c, "px").FontSize(11).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
		})
		a.settingRow(c, "图片高度", func() {
			ui.NumberInput(c, &a.outputHeight, minPageHeight, maxPageHeight, 1).Width(120)
			ui.Text(c, "px").FontSize(11).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
		})
		if beforeWidth != a.outputWidth || beforeHeight != a.outputHeight {
			a.schedulePreview(220 * time.Millisecond)
		}

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &a.outputDir).Placeholder("默认与 TXT 文件同目录").Grow(1)
			if ui.Button(c, "选择…").Width(72).Clicked() {
				a.chooseOutputDir()
			}
		})
		ui.Text(c, "宽 320–4000 · 高 320–6000 px；文字会按图片尺寸自动重排，不会截断").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "生成 PNG 图片，文件名由源文件与页码组成").FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *app) actionButtons(c *ui.Context) {
	if a.generating {
		ui.Progress(c, a.progress).Width(leftContentWidth)
	}
	imageLabel := "生成图片"
	pdfLabel := "导出 PDF"
	if a.generating {
		if a.exportingPDF {
			pdfLabel = "正在导出…"
		} else {
			imageLabel = "正在生成…"
		}
	}
	disabled := a.generating || strings.TrimSpace(a.text) == ""
	ui.Row(c).Width(leftContentWidth).Gap(8).Children(func() {
		if ui.PrimaryButton(c, imageLabel).Grow(1).Height(42).Disabled(disabled).Clicked() {
			a.generate()
		}
		if ui.Button(c, pdfLabel).Grow(1).Height(42).Disabled(disabled).Clicked() {
			a.choosePDFExport()
		}
	})
}

func (a *app) previewPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).FillHeight().Padding(16).Gap(12).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "页面预览").FontSize(14).Bold()
			ui.Box(c).Grow(1)
			ui.Textf(c, "共 %d 张图片", a.pages).FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c, "上一页").Width(68).Disabled(a.page <= 0 || a.pages == 0).Clicked() {
				a.page--
				a.schedulePreview(0)
			}
			if ui.Button(c, "下一页").Width(68).Disabled(a.pages == 0 || a.page+1 >= a.pages).Clicked() {
				a.page++
				a.schedulePreview(0)
			}
		})

		ui.Box(c).Grow(1).Padding(10).Radius(8).Border(1, t.Border).
			Background(t.Background).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			switch {
			case a.previewErr != "":
				ui.Text(c, a.previewErr).TextColor(t.Danger).FontSize(12).MaxWidth(420)
			case a.preview != nil:
				ui.Image(c, a.preview).Grow(1).Fit(ui.Contain)
			case strings.TrimSpace(a.text) == "":
				ui.Text(c, "拖入或选择 TXT 文件后，这里会显示实时预览").TextColor(t.TextMuted).FontSize(13)
			default:
				ui.Spinner(c)
			}
		})

		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "第 %d / %d 页", pageNumber(a.page), a.pages).FontSize(12).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			ui.Text(c, "预览为实际出图缩小显示").FontSize(11).TextColor(t.TextMuted)
		})
	})
}

func pageNumber(page int) int {
	if page < 0 {
		return 0
	}
	return page + 1
}

type previewSnapshot struct {
	text             string
	split            bool
	chars            int
	width            int
	height           int
	font             FontRef
	watermark        string
	watermarkSize    float64
	watermarkOpacity float64
	watermarkAngle   float64
	page             int
}

func (a *app) makeSnapshot() (previewSnapshot, error) {
	ref, ok := a.currentFont()
	if !ok {
		return previewSnapshot{}, fmt.Errorf("未找到可用字体，请安装字体或在字体列表中选择")
	}
	return previewSnapshot{
		text:             a.text,
		split:            a.splitParagraphs,
		chars:            int(math.Round(a.charsPerLine)),
		width:            int(math.Round(a.outputWidth)),
		height:           int(math.Round(a.outputHeight)),
		font:             ref,
		watermark:        a.watermark,
		watermarkSize:    a.watermarkSize,
		watermarkOpacity: a.watermarkOpacity / 100,
		watermarkAngle:   a.watermarkAngle,
		page:             a.page,
	}, nil
}

func (a *app) schedulePreview(delay time.Duration) {
	seq := a.renderSeq.Add(1)
	if a.win == nil {
		return
	}
	snap, err := a.makeSnapshot()
	if err != nil {
		a.previewErr = err.Error()
		return
	}
	if strings.TrimSpace(snap.text) == "" {
		a.pages = 0
		a.page = 0
		a.preview = nil
		a.previewErr = ""
		return
	}

	a.previewErr = ""
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		if a.renderSeq.Load() != seq {
			return
		}

		doc := BuildDocument(snap.text, snap.split, snap.chars, snap.width, snap.height)
		if doc.Pages() == 0 {
			a.win.Update(func() {
				if a.renderSeq.Load() != seq {
					return
				}
				a.pages = 0
				a.page = 0
				a.preview = nil
				a.previewErr = ""
			})
			return
		}

		page := snap.page
		if page >= doc.Pages() {
			page = doc.Pages() - 1
		}
		if page < 0 {
			page = 0
		}
		img, err := RenderPage(doc, page, RenderOptions{
			Font:             snap.font,
			Watermark:        snap.watermark,
			WatermarkSize:    snap.watermarkSize,
			WatermarkOpacity: snap.watermarkOpacity,
			WatermarkAngle:   snap.watermarkAngle,
		})
		if err != nil {
			a.win.Update(func() {
				if a.renderSeq.Load() != seq {
					return
				}
				a.pages = doc.Pages()
				a.page = page
				a.preview = nil
				a.previewErr = err.Error()
			})
			return
		}
		small := scalePreview(img, previewMaxW, previewMaxH)
		a.win.Update(func() {
			if a.renderSeq.Load() != seq {
				return
			}
			a.pages = doc.Pages()
			a.page = page
			a.preview = ui.NewBitmap(small)
			a.previewErr = ""
		})
	}()
}

func (a *app) handleDroppedFiles(files []string) {
	for _, path := range files {
		if strings.EqualFold(filepath.Ext(path), ".txt") {
			a.openTXT(path)
			return
		}
	}
	a.setStatus("请拖入 .txt 文本文件", true)
}

func (a *app) chooseTXT() {
	if a.win == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   "选择 TXT 文件",
			Filters: []mygo.FileFilter{{Name: "文本文件", Extensions: []string{"txt"}}},
		})
		if err != nil {
			a.win.Update(func() { a.setStatus("打开文件失败: "+err.Error(), true) })
			return
		}
		if len(paths) > 0 {
			path := paths[0]
			a.win.Update(func() { a.openTXT(path) })
		}
	}()
}

func (a *app) openTXT(path string) {
	if !strings.EqualFold(filepath.Ext(path), ".txt") {
		a.setStatus("请选择 .txt 文本文件", true)
		return
	}
	if a.win == nil {
		return
	}
	go func() {
		text, err := readTextFile(path)
		a.win.Update(func() {
			if err != nil {
				a.setStatus("读取文件失败: "+err.Error(), true)
				return
			}
			a.text = text
			a.sourcePath = path
			if strings.TrimSpace(a.outputDir) == "" {
				a.outputDir = filepath.Dir(path)
			}
			a.page = 0
			a.pages = 0
			a.preview = nil
			a.previewErr = ""
			a.setStatus(fmt.Sprintf("已载入 %s · %d 字符", filepath.Base(path), utf8.RuneCountInString(text)), false)
			a.schedulePreview(0)
		})
	}()
}

func (a *app) chooseOutputDir() {
	if a.win == nil {
		return
	}
	defaultPath := a.outputDir
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            a.win,
			Title:             "选择图片输出目录",
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil {
			a.win.Update(func() { a.setStatus("选择目录失败: "+err.Error(), true) })
			return
		}
		if len(paths) > 0 {
			a.win.Update(func() {
				a.outputDir = paths[0]
				a.setStatus("输出目录已选择", false)
			})
		}
	}()
}

func (a *app) generate() {
	if a.generating {
		return
	}
	if strings.TrimSpace(a.text) == "" {
		a.setStatus("请先选择 TXT 文件", true)
		return
	}
	ref, ok := a.currentFont()
	if !ok {
		a.setStatus("未找到可用字体", true)
		return
	}

	out := a.resolveOutputDir()
	if out == "" {
		a.setStatus("请选择输出目录", true)
		return
	}

	snap := previewSnapshot{
		text:             a.text,
		split:            a.splitParagraphs,
		chars:            int(math.Round(a.charsPerLine)),
		width:            int(math.Round(a.outputWidth)),
		height:           int(math.Round(a.outputHeight)),
		font:             ref,
		watermark:        a.watermark,
		watermarkSize:    a.watermarkSize,
		watermarkOpacity: a.watermarkOpacity / 100,
		watermarkAngle:   a.watermarkAngle,
	}
	base := a.documentBaseName()

	a.renderSeq.Add(1)
	a.generating = true
	a.exportingPDF = false
	a.progress = 0
	a.statusErr = false
	a.setStatus("正在准备生成…", false)
	if a.win == nil {
		a.generating = false
		return
	}

	go func() {
		doc := BuildDocument(snap.text, snap.split, snap.chars, snap.width, snap.height)
		if doc.Pages() == 0 {
			a.finishGenerateError("没有可生成的内容")
			return
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			a.finishGenerateError("创建输出目录失败: " + err.Error())
			return
		}

		digits := len(strconv.Itoa(doc.Pages()))
		if digits < 3 {
			digits = 3
		}
		for i := 0; i < doc.Pages(); i++ {
			img, err := RenderPage(doc, i, snap.renderOptions())
			if err != nil {
				a.finishGenerateError(fmt.Sprintf("生成第 %d 页失败: %v", i+1, err))
				return
			}
			name := fmt.Sprintf("%s_%0*d.png", base, digits, i+1)
			target := filepath.Join(out, name)
			f, err := os.Create(target)
			if err != nil {
				a.finishGenerateError("创建图片失败: " + err.Error())
				return
			}
			encodeErr := png.Encode(f, img)
			closeErr := f.Close()
			if encodeErr != nil {
				a.finishGenerateError("写入图片失败: " + encodeErr.Error())
				return
			}
			if closeErr != nil {
				a.finishGenerateError("保存图片失败: " + closeErr.Error())
				return
			}
			done := i + 1
			a.win.Update(func() {
				a.progress = float64(done) / float64(doc.Pages())
				a.status = fmt.Sprintf("正在生成 %d / %d", done, doc.Pages())
			})
		}

		a.win.Update(func() {
			a.generating = false
			a.exportingPDF = false
			a.progress = 1
			a.outputDir = out
			a.setStatus(fmt.Sprintf("已生成 %d 张图片 · %s", doc.Pages(), out), false)
		})
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent:  a.win,
			Type:    mygo.MessageInfo,
			Title:   "转换成功",
			Message: fmt.Sprintf("已生成 %d 张图片", doc.Pages()),
			Detail:  out,
			Buttons: []string{"好"},
		})
	}()
}

func (a *app) choosePDFExport() {
	if a.generating {
		return
	}
	if strings.TrimSpace(a.text) == "" {
		a.setStatus("请先选择 TXT 文件", true)
		return
	}
	out := a.resolveOutputDir()
	base := a.documentBaseName()
	defaultPath := filepath.Join(out, base+".pdf")
	a.setStatus("请选择 PDF 保存位置…", false)

	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:            a.win,
			Title:             "导出 PDF",
			DefaultPath:       defaultPath,
			Filters:           []mygo.FileFilter{{Name: "PDF 文档", Extensions: []string{"pdf"}}},
			CreateDirectories: true,
		})
		if err != nil {
			a.win.Update(func() { a.setStatus("选择 PDF 保存位置失败: "+err.Error(), true) })
			return
		}
		if strings.TrimSpace(path) == "" {
			a.win.Update(func() { a.setStatus("已取消导出 PDF", false) })
			return
		}
		if !strings.EqualFold(filepath.Ext(path), ".pdf") {
			path += ".pdf"
		}
		a.win.Update(func() { a.startPDFExport(path) })
	}()
}

func (a *app) startPDFExport(path string) {
	if a.generating {
		return
	}
	snap, err := a.makeSnapshot()
	if err != nil {
		a.setStatus(err.Error(), true)
		return
	}
	a.renderSeq.Add(1)
	a.generating = true
	a.exportingPDF = true
	a.progress = 0
	a.statusErr = false
	a.setStatus("正在准备导出 PDF…", false)
	if a.win == nil {
		a.generating = false
		a.exportingPDF = false
		return
	}

	go func() {
		doc := BuildDocument(snap.text, snap.split, snap.chars, snap.width, snap.height)
		if doc.Pages() == 0 {
			a.finishGenerateError("没有可导出的内容")
			return
		}
		dir := filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.finishGenerateError("创建 PDF 目录失败: " + err.Error())
			return
		}
		f, err := os.Create(path)
		if err != nil {
			a.finishGenerateError("创建 PDF 失败: " + err.Error())
			return
		}
		err = WritePDF(f, doc, snap.renderOptions(), func(done, total int) {
			a.win.Update(func() {
				a.progress = float64(done) / float64(total)
				a.status = fmt.Sprintf("正在导出 PDF %d / %d", done, total)
			})
		})
		closeErr := f.Close()
		if err != nil {
			_ = os.Remove(path)
			a.finishGenerateError("导出 PDF 失败: " + err.Error())
			return
		}
		if closeErr != nil {
			_ = os.Remove(path)
			a.finishGenerateError("保存 PDF 失败: " + closeErr.Error())
			return
		}

		a.win.Update(func() {
			a.generating = false
			a.exportingPDF = false
			a.progress = 1
			a.outputDir = dir
			a.setStatus(fmt.Sprintf("已导出 %d 页 PDF · %s", doc.Pages(), path), false)
		})
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent:  a.win,
			Type:    mygo.MessageInfo,
			Title:   "PDF 导出成功",
			Message: fmt.Sprintf("已导出 %d 页 PDF", doc.Pages()),
			Detail:  path,
			Buttons: []string{"好"},
		})
	}()
}

func (a *app) finishGenerateError(message string) {
	if a.win == nil {
		a.generating = false
		a.exportingPDF = false
		return
	}
	a.win.Update(func() {
		a.generating = false
		a.exportingPDF = false
		a.progress = 0
		a.setStatus(message, true)
	})
}

func (a *app) resolveOutputDir() string {
	out := strings.TrimSpace(a.outputDir)
	if out == "" {
		if a.sourcePath != "" {
			out = filepath.Dir(a.sourcePath)
		} else if wd, err := os.Getwd(); err == nil {
			out = wd
		}
	}
	return expandHome(out)
}

func (a *app) documentBaseName() string {
	base := "txt2img"
	if a.sourcePath != "" {
		base = safeFileBase(strings.TrimSuffix(filepath.Base(a.sourcePath), filepath.Ext(a.sourcePath)))
	}
	return base
}

func (s previewSnapshot) renderOptions() RenderOptions {
	return RenderOptions{
		Font:             s.font,
		Watermark:        s.watermark,
		WatermarkSize:    s.watermarkSize,
		WatermarkOpacity: s.watermarkOpacity,
		WatermarkAngle:   s.watermarkAngle,
	}
}

func (a *app) setStatus(message string, isError bool) {
	a.status = message
	a.statusErr = isError
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func safeFileBase(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), " ._")
	runes := []rune(out)
	if len(runes) > 60 {
		out = string(runes[:60])
	}
	if out == "" {
		return "txt2img"
	}
	return out
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     windowTitle,
			Width:     1240,
			Height:    860,
			MinWidth:  980,
			MinHeight: 720,
			StateKey:  "main-v2",
			Content:   ui.View(a.view),
		})
		a.startFontScan()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
