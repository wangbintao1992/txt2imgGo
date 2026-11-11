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

	antiWarp           bool
	antiWarpStrength   float64
	antiCrowd          bool
	antiCrowdStrength  float64
	antiStroke         bool
	antiStrokeStrength float64
	antiNoise          bool
	antiNoiseStrength  float64

	pdfOpenPassword  string
	pdfOwnerPassword string
	pdfNoPrint       bool
	pdfNoCopy        bool
	pdfNoExtract     bool

	archiveFormat   string
	archivePassword string
	sevenZip        string

	page       int
	pages      int
	preview    *ui.Bitmap
	previewErr string

	status    string
	statusErr bool

	generating   bool
	exportingPDF bool
	exportingZip bool
	progress     float64

	renderSeq atomic.Uint64
}

func newApp() *app {
	ref, _ := detectSystemFont()
	a := &app{
		splitParagraphs:    true,
		charsPerLine:       30,
		outputWidth:        pageWidth,
		outputHeight:       pageHeight,
		fontName:           systemFontName,
		systemFont:         ref,
		watermarkSize:      46,
		watermarkOpacity:   14,
		watermarkAngle:     -30,
		antiWarpStrength:   55,
		antiCrowdStrength:  45,
		antiStrokeStrength: 55,
		antiNoiseStrength:  40,
		archiveFormat:      archiveFormatLabels[0],
		status:             "拖入或选择 TXT 文件开始",
	}
	a.sevenZip = find7z()
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

		ui.Row(c).Fill().MinHeight(0).Gap(18).Children(func() {
			ui.Column(c).Width(leftPaneWidth).FillHeight().MinHeight(0).Gap(10).Children(func() {
				ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
					ui.Column(c).Width(leftContentWidth).Gap(12).Children(func() {
						a.dropZone(c)
						a.settingsPanel(c)
						a.antiOCRPanel(c)
						a.outputPanel(c)
						a.pdfSecurityPanel(c)
						a.archivePanel(c)
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
		if a.sourcePath == "" {
			if ui.Button(c, "选择 TXT 文件").Width(150).Clicked() {
				a.chooseTXT()
			}
		} else {
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "更换文件").Width(110).Clicked() {
					a.chooseTXT()
				}
				if ui.Button(c, "清空").Width(80).Clicked() {
					a.clearSelectedFile()
				}
			})
		}
	})
}

func (a *app) clearSelectedFile() {
	if a.generating {
		return
	}
	a.renderSeq.Add(1)
	a.text = ""
	a.sourcePath = ""
	a.page = 0
	a.pages = 0
	a.preview = nil
	a.previewErr = ""
	a.progress = 0
	a.setStatus("已清空待转换文件", false)
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

func (a *app) antiOCRPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(leftContentWidth).Padding(16).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Disabled(a.generating).Children(func() {
		ui.Text(c, "反 OCR 增强").FontSize(14).Bold()
		ui.Text(c, "提高 OCR 识别难度，仅作弱抑制").FontSize(11).TextColor(t.TextMuted)

		before := a.antiOCROptions()
		a.antiOCRRow(c, "形变扭曲", &a.antiWarp, &a.antiWarpStrength)
		a.antiOCRRow(c, "字距挤压", &a.antiCrowd, &a.antiCrowdStrength)
		a.antiOCRRow(c, "笔画重影", &a.antiStroke, &a.antiStrokeStrength)
		a.antiOCRRow(c, "干扰纹理", &a.antiNoise, &a.antiNoiseStrength)
		if before != a.antiOCROptions() {
			a.schedulePreview(220 * time.Millisecond)
		}
	})
}

func (a *app) antiOCRRow(c *ui.Context, label string, on *bool, strength *float64) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Switch(c, on)
		ui.Text(c, label).Width(78).FontSize(13)
		ui.Slider(c, strength, 0, 100).Step(1).Grow(1).Disabled(!*on)
		ui.Textf(c, "%.0f", *strength).Width(30).FontSize(11).
			TextColor(t.TextMuted).TextAlign(ui.End)
	})
}

func (a *app) antiOCROptions() AntiOCR {
	var opts AntiOCR
	if a.antiWarp {
		opts.Warp = a.antiWarpStrength
	}
	if a.antiCrowd {
		opts.Crowd = a.antiCrowdStrength
	}
	if a.antiStroke {
		opts.Stroke = a.antiStrokeStrength
	}
	if a.antiNoise {
		opts.Noise = a.antiNoiseStrength
	}
	return opts
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

func (a *app) pdfSecurityPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(leftContentWidth).Padding(16).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Disabled(a.generating).Children(func() {
		ui.Text(c, "PDF 权限加密").FontSize(14).Bold()

		a.settingRow(c, "打开密码", func() {
			ui.TextInput(c, &a.pdfOpenPassword).Password().Placeholder("留空则打开无需密码").Grow(1)
		})
		a.settingRow(c, "权限密码", func() {
			ui.TextInput(c, &a.pdfOwnerPassword).Password().Placeholder("用于修改权限").Grow(1)
		})

		a.pdfToggleRow(c, "禁止打印", &a.pdfNoPrint)
		a.pdfToggleRow(c, "禁止复制文字", &a.pdfNoCopy)
		a.pdfToggleRow(c, "禁止提取图片", &a.pdfNoExtract)

		ui.Text(c, "设置任一密码后启用加密，仅对 PDF 导出有效").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "PDF 标准中复制文字与提取图片共用同一权限位，禁止其一即同时限制").FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *app) pdfToggleRow(c *ui.Context, label string, on *bool) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Switch(c, on)
		ui.Text(c, label).FontSize(13)
		ui.Box(c).Grow(1)
	})
}

func (a *app) pdfSecurity() PDFSecurity {
	return PDFSecurity{
		OpenPassword:  a.pdfOpenPassword,
		OwnerPassword: a.pdfOwnerPassword,
		AllowPrint:    !a.pdfNoPrint,
		AllowCopy:     !a.pdfNoCopy,
		AllowExtract:  !a.pdfNoExtract,
	}
}

func (a *app) archivePanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(leftContentWidth).Padding(16).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Disabled(a.generating).Children(func() {
		ui.Text(c, "导出压缩包").FontSize(14).Bold()

		a.settingRow(c, "压缩格式", func() {
			ui.Select(c, &a.archiveFormat, archiveFormatLabels).Grow(1)
		})
		a.settingRow(c, "压缩密码", func() {
			ui.TextInput(c, &a.archivePassword).Password().Placeholder("留空则不加密").Grow(1)
		})
		archiveLabel := "导出压缩包"
		if a.exportingZip {
			archiveLabel = "正在导出…"
		}
		if ui.Button(c, archiveLabel).Width(leftContentWidth - 32).Height(38).
			Disabled(strings.TrimSpace(a.text) == "").Clicked() {
			a.chooseArchiveExport()
		}

		ui.Text(c, "包含全部 PNG 图片；ZIP 使用内置 AES-256 加密").FontSize(11).TextColor(t.TextMuted)
		if a.currentArchiveFormat() == format7z {
			if a.sevenZip == "" {
				ui.Text(c, "未检测到 7-Zip 命令（7zz / 7z / 7za），请先安装或改用 ZIP").FontSize(11).TextColor(t.Danger)
			} else {
				ui.Textf(c, "7z 使用 %s；设置密码时会同时加密文件头", filepath.Base(a.sevenZip)).
					FontSize(11).TextColor(t.TextMuted)
			}
		}
	})
}

func (a *app) actionButtons(c *ui.Context) {
	t := c.Theme()
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
	ui.Column(c).Width(leftContentWidth).Gap(8).Shrink(0).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Progress(c, a.progress).Grow(1)
			ui.Textf(c, "%.0f%%", a.progress*100).Width(40).FontSize(11).
				TextColor(t.TextMuted).TextAlign(ui.End)
		})
		ui.Row(c).Width(leftContentWidth).Gap(8).Children(func() {
			if ui.PrimaryButton(c, imageLabel).Grow(1).Height(42).Disabled(disabled).Clicked() {
				a.generate()
			}
			if ui.Button(c, pdfLabel).Grow(1).Height(42).Disabled(disabled).Clicked() {
				a.choosePDFExport()
			}
		})
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
	anti             AntiOCR
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
		anti:             a.antiOCROptions(),
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
			AntiOCR:          snap.anti,
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
		anti:             a.antiOCROptions(),
	}
	base := a.documentBaseName()

	a.renderSeq.Add(1)
	a.generating = true
	a.exportingPDF = false
	a.exportingZip = false
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
	sec := a.pdfSecurity()
	if !sec.Enabled() && sec.Restricted() {
		a.setStatus("已勾选禁止项，请先设置打开密码或权限密码", true)
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
	sec := a.pdfSecurity()
	a.renderSeq.Add(1)
	a.generating = true
	a.exportingPDF = true
	a.exportingZip = false
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
		err = WritePDF(f, doc, snap.renderOptions(), sec, func(done, total int) {
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

func (a *app) chooseArchiveExport() {
	if a.generating {
		return
	}
	if strings.TrimSpace(a.text) == "" {
		a.setStatus("请先选择 TXT 文件", true)
		return
	}
	format := a.currentArchiveFormat()
	if format == format7z && a.sevenZip == "" {
		a.setStatus("未找到 7-Zip 命令，请先安装 7-Zip 或改用 ZIP 格式", true)
		return
	}
	out := a.resolveOutputDir()
	base := a.documentBaseName()
	ext := archiveExtension(format)
	defaultPath := filepath.Join(out, base+ext)
	name := "ZIP 压缩包"
	if format == format7z {
		name = "7z 压缩包"
	}
	a.setStatus("请选择压缩包保存位置…", false)

	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:            a.win,
			Title:             "导出压缩包",
			DefaultPath:       defaultPath,
			Filters:           []mygo.FileFilter{{Name: name, Extensions: []string{strings.TrimPrefix(ext, ".")}}},
			CreateDirectories: true,
		})
		if err != nil {
			a.win.Update(func() { a.setStatus("选择压缩包保存位置失败: "+err.Error(), true) })
			return
		}
		if strings.TrimSpace(path) == "" {
			a.win.Update(func() { a.setStatus("已取消导出压缩包", false) })
			return
		}
		if !strings.EqualFold(filepath.Ext(path), ext) {
			path += ext
		}
		a.win.Update(func() { a.startArchiveExport(path) })
	}()
}

func (a *app) startArchiveExport(path string) {
	if a.generating {
		return
	}
	snap, err := a.makeSnapshot()
	if err != nil {
		a.setStatus(err.Error(), true)
		return
	}
	format := a.currentArchiveFormat()
	password := a.archivePassword
	bin := a.sevenZip
	if format == format7z && bin == "" {
		a.setStatus("未找到 7-Zip 命令，请先安装 7-Zip 或改用 ZIP 格式", true)
		return
	}
	base := a.documentBaseName()

	a.renderSeq.Add(1)
	a.generating = true
	a.exportingZip = true
	a.exportingPDF = false
	a.progress = 0
	a.statusErr = false
	a.setStatus("正在准备导出压缩包…", false)
	if a.win == nil {
		a.generating = false
		a.exportingZip = false
		return
	}

	go func() {
		doc := BuildDocument(snap.text, snap.split, snap.chars, snap.width, snap.height)
		if doc.Pages() == 0 {
			a.finishGenerateError("没有可导出的内容")
			return
		}

		tmp, err := os.MkdirTemp("", "txt2img-archive-*")
		if err != nil {
			a.finishGenerateError("创建临时目录失败: " + err.Error())
			return
		}
		defer os.RemoveAll(tmp)

		digits := len(strconv.Itoa(doc.Pages()))
		if digits < 3 {
			digits = 3
		}
		files := make([]string, 0, doc.Pages())
		for i := 0; i < doc.Pages(); i++ {
			img, err := RenderPage(doc, i, snap.renderOptions())
			if err != nil {
				a.finishGenerateError(fmt.Sprintf("生成第 %d 页失败: %v", i+1, err))
				return
			}
			name := filepath.Join(tmp, fmt.Sprintf("%s_%0*d.png", base, digits, i+1))
			f, err := os.Create(name)
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
			files = append(files, name)
			done := i + 1
			a.win.Update(func() {
				a.progress = 0.7 * float64(done) / float64(doc.Pages())
				a.status = fmt.Sprintf("正在渲染压缩包内容 %d / %d", done, doc.Pages())
			})
		}

		dir := filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.finishGenerateError("创建压缩包目录失败: " + err.Error())
			return
		}

		switch format {
		case format7z:
			a.win.Update(func() { a.status = "正在压缩为 7z…" })
			err = write7zArchive(bin, path, files, password, func(done, total int) {
				a.win.Update(func() {
					a.progress = 1
					a.status = "正在写入 7z…"
				})
			})
		default:
			a.win.Update(func() { a.status = "正在压缩为 ZIP…" })
			err = writeZIPArchive(path, files, password, func(done, total int) {
				a.win.Update(func() {
					a.progress = 0.7 + 0.3*float64(done)/float64(total)
					a.status = fmt.Sprintf("正在压缩 %d / %d", done, total)
				})
			})
		}
		if err != nil {
			_ = os.Remove(path)
			a.finishGenerateError("导出压缩包失败: " + err.Error())
			return
		}

		a.win.Update(func() {
			a.generating = false
			a.exportingZip = false
			a.progress = 1
			a.outputDir = dir
			a.setStatus(fmt.Sprintf("已导出 %d 张图片压缩包 · %s", doc.Pages(), path), false)
		})
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent:  a.win,
			Type:    mygo.MessageInfo,
			Title:   "压缩包导出成功",
			Message: fmt.Sprintf("已打包 %d 张图片", doc.Pages()),
			Detail:  path,
			Buttons: []string{"好"},
		})
	}()
}

func (a *app) finishGenerateError(message string) {
	if a.win == nil {
		a.generating = false
		a.exportingPDF = false
		a.exportingZip = false
		return
	}
	a.win.Update(func() {
		a.generating = false
		a.exportingPDF = false
		a.exportingZip = false
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
		AntiOCR:          s.anti,
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
