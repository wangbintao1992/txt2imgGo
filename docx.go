package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"strings"
	"time"
)

// WriteDOCX 生成一个最小但完整的 .docx。
// split 为 true 时每个非空行是一段；否则沿用「合并段落」的规则合成一整段。
// opt 只使用字体与水印参数：Word 是文字稿，反 OCR 无法逐字变形，故不参与。
func WriteDOCX(w io.Writer, text string, split bool, title string, opt RenderOptions) error {
	paras := docxParagraphs(text, split)
	if len(paras) == 0 {
		return fmt.Errorf("没有可导出的内容")
	}

	wmPNG, err := docxWatermarkPNG(opt)
	if err != nil {
		return err
	}
	hasWM := len(wmPNG) > 0

	zw := zip.NewWriter(w)
	now := time.Now()
	add := func(name, content string) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}
		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, content)
		return err
	}

	addBytes := func(name string, content []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}
		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = f.Write(content)
		return err
	}

	if err := add("[Content_Types].xml", docxContentTypes(hasWM)); err != nil {
		return err
	}
	if err := add("_rels/.rels", docxRootRels); err != nil {
		return err
	}
	if err := add("word/_rels/document.xml.rels", docxDocumentRels(hasWM)); err != nil {
		return err
	}
	if err := add("word/styles.xml", docxStyles); err != nil {
		return err
	}
	if err := add("word/document.xml", docxDocument(paras, hasWM)); err != nil {
		return err
	}
	if err := add("docProps/core.xml", docxCoreProps(title)); err != nil {
		return err
	}
	if hasWM {
		if err := add("word/header1.xml", docxHeaderXML); err != nil {
			return err
		}
		if err := add("word/_rels/header1.xml.rels", docxHeaderRels); err != nil {
			return err
		}
		if err := addBytes("word/media/watermark.png", wmPNG); err != nil {
			return err
		}
	}
	return zw.Close()
}

// docxWatermarkPNG 复用图片版的平铺水印绘制，输出透明背景的 A4 PNG。
func docxWatermarkPNG(opt RenderOptions) ([]byte, error) {
	if strings.TrimSpace(opt.Watermark) == "" {
		return nil, nil
	}
	size := opt.WatermarkSize
	if size <= 0 {
		size = 46
	}
	if size < 12 {
		size = 12
	}
	if size > 160 {
		size = 160
	}
	opacity := opt.WatermarkOpacity
	if opacity <= 0 {
		opacity = 0.14
	}
	if opacity > 1 {
		opacity = 1
	}
	face, err := newFontFace(opt.Font, size)
	if err != nil {
		return nil, fmt.Errorf("生成 Word 水印失败: %w", err)
	}
	defer face.Close()

	img := image.NewRGBA(image.Rect(0, 0, pageWidth, pageHeight))
	drawDiagonalWatermark(img, opt.Watermark, face, opt.WatermarkAngle, opacity)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码 Word 水印失败: %w", err)
	}
	return buf.Bytes(), nil
}

func docxParagraphs(text string, split bool) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !split {
		merged := mergeParagraphs(text)
		if merged == "" {
			return nil
		}
		return []string{merged}
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

var docxXMLEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func docxDocument(paras []string, watermark bool) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>`)
	for i, p := range paras {
		// 日记头加粗、不缩进，方便当作标题看。
		bold := i == 0 && strings.HasPrefix(p, "日记·")
		b.WriteString("<w:p><w:pPr>")
		b.WriteString(`<w:spacing w:line="360" w:lineRule="auto"/>`)
		if !bold {
			b.WriteString(`<w:ind w:firstLine="480"/>`)
		}
		b.WriteString(`<w:jc w:val="both"/></w:pPr><w:r><w:rPr>`)
		if bold {
			b.WriteString("<w:b/>")
		}
		b.WriteString(`<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr><w:t xml:space="preserve">`)
		b.WriteString(docxXMLEscaper.Replace(strings.ReplaceAll(p, "\t", " ")))
		b.WriteString("</w:t></w:r></w:p>")
	}
	b.WriteString(`<w:sectPr>`)
	if watermark {
		b.WriteString(`<w:headerReference w:type="default" r:id="rId2"/>`)
	}
	b.WriteString(`<w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	b.WriteString("</w:body></w:document>")
	return b.String()
}

func docxCoreProps(title string) string {
	if strings.TrimSpace(title) == "" {
		title = "TXT 转图片导出"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return xmlHeader +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + docxXMLEscaper.Replace(title) + `</dc:title>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified>` +
		`</cp:coreProperties>`
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func docxContentTypes(watermark bool) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	b.WriteString(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>`)
	b.WriteString(`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	if watermark {
		b.WriteString(`<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>`)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

const docxRootRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

func docxDocumentRels(watermark bool) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)
	if watermark {
		b.WriteString(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

const docxStyles = xmlHeader + `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr>` +
	`<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/>` +
	`<w:sz w:val="24"/><w:szCs w:val="24"/>` +
	`</w:rPr></w:rPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	`</w:styles>`

const docxHeaderRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/watermark.png"/>` +
	`</Relationships>`

// docxHeaderXML 用页眉里的 VML 图片做整页平铺水印。图片由 docxWatermarkPNG
// 按 A4 150 DPI 生成，锚定在页面左上角并置于正文之下。
const docxHeaderXML = xmlHeader +
	`<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:v="urn:schemas-microsoft-com:vml" ` +
	`xmlns:o="urn:schemas-microsoft-com:office:office">` +
	`<w:p><w:pPr><w:spacing w:after="0" w:line="1" w:lineRule="exact"/></w:pPr><w:r><w:pict>` +
	`<v:shapetype id="_x0000_t75" coordsize="21600,21600" o:spt="75" o:preferrelative="t" ` +
	`path="m@4@5l@4@11@9@11@9@5xe" filled="f" stroked="f">` +
	`<v:stroke joinstyle="miter"/>` +
	`<v:formulas>` +
	`<v:f eqn="if lineDrawn pixelLineWidth 0"/><v:f eqn="sum @0 1 0"/><v:f eqn="sum 0 0 @1"/>` +
	`<v:f eqn="prod @2 1 2"/><v:f eqn="prod @3 21600 pixelWidth"/><v:f eqn="prod @3 21600 pixelHeight"/>` +
	`<v:f eqn="sum @0 0 1"/><v:f eqn="prod @6 1 2"/><v:f eqn="prod @7 21600 pixelWidth"/>` +
	`<v:f eqn="sum @8 21600 0"/><v:f eqn="prod @7 21600 pixelHeight"/><v:f eqn="sum @10 21600 0"/>` +
	`</v:formulas>` +
	`<v:path o:extrusionok="f" gradientshapeok="t" o:connecttype="rect"/>` +
	`<o:lock v:ext="edit" aspectratio="t"/>` +
	`</v:shapetype>` +
	`<v:shape id="WatermarkObject" o:spid="_x0000_s2049" type="#_x0000_t75" ` +
	`style="position:absolute;margin-left:0;margin-top:0;width:595.3pt;height:841.9pt;` +
	`z-index:-251658752;mso-position-horizontal-relative:page;mso-position-vertical-relative:page" ` +
	`o:allowincell="f" stroked="f">` +
	`<v:imagedata r:id="rId1" o:title="watermark"/>` +
	`</v:shape>` +
	`</w:pict></w:r></w:p></w:hdr>`
