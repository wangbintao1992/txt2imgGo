package main

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
	"time"
)

// WriteDOCX 生成一个最小但完整的 .docx。
// split 为 true 时每个非空行是一段；否则沿用「合并段落」的规则合成一整段。
func WriteDOCX(w io.Writer, text string, split bool, title string) error {
	paras := docxParagraphs(text, split)
	if len(paras) == 0 {
		return fmt.Errorf("没有可导出的内容")
	}

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

	if err := add("[Content_Types].xml", docxContentTypes); err != nil {
		return err
	}
	if err := add("_rels/.rels", docxRootRels); err != nil {
		return err
	}
	if err := add("word/_rels/document.xml.rels", docxDocumentRels); err != nil {
		return err
	}
	if err := add("word/styles.xml", docxStyles); err != nil {
		return err
	}
	if err := add("word/document.xml", docxDocument(paras)); err != nil {
		return err
	}
	if err := add("docProps/core.xml", docxCoreProps(title)); err != nil {
		return err
	}
	return zw.Close()
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

func docxDocument(paras []string) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
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
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
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

const docxContentTypes = xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
	`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
	`</Types>`

const docxRootRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

const docxDocumentRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`</Relationships>`

const docxStyles = xmlHeader + `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr>` +
	`<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/>` +
	`<w:sz w:val="24"/><w:szCs w:val="24"/>` +
	`</w:rPr></w:rPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	`</w:styles>`
