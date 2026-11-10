package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"io"
	"strings"
)

const pdfDPI = 150

func WritePDF(w io.Writer, doc *Document, opt RenderOptions, progress func(done, total int)) error {
	if doc.Pages() == 0 {
		return fmt.Errorf("没有可导出的内容")
	}

	p := &pdfWriter{w: w, offsets: make(map[int]int64)}
	if err := p.writeString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"); err != nil {
		return err
	}

	if err := p.object(1, "<< /Type /Catalog /Pages 2 0 R >>\n"); err != nil {
		return err
	}

	pagePointsW := float64(doc.PageWidth) * 72 / pdfDPI
	pagePointsH := float64(doc.PageHeight) * 72 / pdfDPI
	pageIDs := make([]int, doc.Pages())

	for i := 0; i < doc.Pages(); i++ {
		img, err := RenderPage(doc, i, opt)
		if err != nil {
			return err
		}
		rgb := imageToRGB(img)
		compressed, err := compressBytes(rgb)
		if err != nil {
			return err
		}

		imageID := 3 + i*3
		contentID := imageID + 1
		pageID := imageID + 2
		pageIDs[i] = pageID

		imageHeader := fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n",
			doc.PageWidth, doc.PageHeight, len(compressed))
		if err := p.objectStream(imageID, imageHeader, compressed); err != nil {
			return err
		}

		content := fmt.Sprintf("q\n%.3f 0 0 %.3f 0 0 cm\n/Im0 Do\nQ\n", pagePointsW, pagePointsH)
		contentHeader := fmt.Sprintf("<< /Length %d >>\nstream\n", len(content))
		if err := p.objectStream(contentID, contentHeader, []byte(content)); err != nil {
			return err
		}

		pageBody := fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.3f %.3f] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>\n",
			pagePointsW, pagePointsH, imageID, contentID)
		if err := p.object(pageID, pageBody); err != nil {
			return err
		}

		if progress != nil {
			progress(i+1, doc.Pages())
		}
	}

	var kids strings.Builder
	for _, id := range pageIDs {
		fmt.Fprintf(&kids, "%d 0 R ", id)
	}
	pagesBody := fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>\n", len(pageIDs), strings.TrimSpace(kids.String()))
	if err := p.object(2, pagesBody); err != nil {
		return err
	}

	maxID := 2 + doc.Pages()*3
	xrefOffset := p.n
	if err := p.writeString(fmt.Sprintf("xref\n0 %d\n", maxID+1)); err != nil {
		return err
	}
	if err := p.writeString("0000000000 65535 f \n"); err != nil {
		return err
	}
	for id := 1; id <= maxID; id++ {
		offset := p.offsets[id]
		if err := p.writeString(fmt.Sprintf("%010d 00000 n \n", offset)); err != nil {
			return err
		}
	}
	return p.writeString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", maxID+1, xrefOffset))
}

type pdfWriter struct {
	w       io.Writer
	n       int64
	offsets map[int]int64
}

func (p *pdfWriter) writeString(s string) error {
	n, err := io.WriteString(p.w, s)
	p.n += int64(n)
	return err
}

func (p *pdfWriter) writeBytes(b []byte) error {
	n, err := p.w.Write(b)
	p.n += int64(n)
	return err
}

func (p *pdfWriter) object(id int, body string) error {
	p.offsets[id] = p.n
	if err := p.writeString(fmt.Sprintf("%d 0 obj\n", id)); err != nil {
		return err
	}
	if err := p.writeString(body); err != nil {
		return err
	}
	return p.writeString("endobj\n")
}

func (p *pdfWriter) objectStream(id int, header string, data []byte) error {
	p.offsets[id] = p.n
	if err := p.writeString(fmt.Sprintf("%d 0 obj\n", id)); err != nil {
		return err
	}
	if err := p.writeString(header); err != nil {
		return err
	}
	if err := p.writeBytes(data); err != nil {
		return err
	}
	return p.writeString("\nendstream\nendobj\n")
}

func imageToRGB(img *image.RGBA) []byte {
	bounds := img.Bounds()
	rgb := make([]byte, 0, bounds.Dx()*bounds.Dy()*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := y * img.Stride
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			i := row + x*4
			rgb = append(rgb, img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	}
	return rgb
}

func compressBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
