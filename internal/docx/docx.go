// Package docx 生成一个最简的 .docx（WordprocessingML）文件，并把印章 PNG
// 以浮动图片（wp:anchor）的方式放到页面指定位置。
//
// 目前只支持“新建一个空文档 + 盖一个章”。后续要在已有 .docx 上盖章时，
// 可以复用这里的 part 常量，改成先解压、往 document.xml 里插一段 drawing、
// 再重新打包。
package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EMUPerCM 是 1 厘米对应的 EMU（English Metric Unit）数量。
const EMUPerCM = 360000

// A4 纸张尺寸（EMU）。
const (
	A4WidthEMU  = 21 * EMUPerCM
	A4HeightEMU = 297 * EMUPerCM / 10
)

// Picture 描述要嵌入文档的印章图片及其在页面上的位置。
type Picture struct {
	// PNG 是图片数据（必须是 PNG）。
	PNG []byte
	// WidthEMU/HeightEMU 是图片显示尺寸。
	WidthEMU, HeightEMU int64
	// XEMU/YEMU 是图片左上角相对页面左上角的偏移。
	XEMU, YEMU int64
	// BehindText 为 true 时图片置于文字下方。
	BehindText bool
}

const (
	contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	documentRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/seal.png"/>
</Relationships>`
)

// documentXML 生成 A4 页面 + 一段空段落，段落里以浮动方式放一张图。
// sectPr 必须作为 body 的最后一个元素。
const documentXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>
    <w:p>
      <w:pPr><w:spacing w:after="0"/></w:pPr>
      <w:r>
        <w:drawing>
          <wp:anchor distT="0" distB="0" distL="0" distR="0" simplePos="0" relativeHeight="251659264" behindDoc="%[1]d" locked="0" layoutInCell="1" allowOverlap="1">
            <wp:simplePos x="0" y="0"/>
            <wp:positionH relativeFrom="page"><wp:posOffset>%[2]d</wp:posOffset></wp:positionH>
            <wp:positionV relativeFrom="page"><wp:posOffset>%[3]d</wp:posOffset></wp:positionV>
            <wp:extent cx="%[4]d" cy="%[5]d"/>
            <wp:effectExtent l="0" t="0" r="0" b="0"/>
            <wp:wrapNone/>
            <wp:docPr id="1" name="Seal" descr="seal"/>
            <wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>
            <a:graphic>
              <a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">
                <pic:pic>
                  <pic:nvPicPr>
                    <pic:cNvPr id="1" name="seal.png"/>
                    <pic:cNvPicPr/>
                  </pic:nvPicPr>
                  <pic:blipFill>
                    <a:blip r:embed="rId1"/>
                    <a:stretch><a:fillRect/></a:stretch>
                  </pic:blipFill>
                  <pic:spPr>
                    <a:xfrm>
                      <a:off x="0" y="0"/>
                      <a:ext cx="%[4]d" cy="%[5]d"/>
                    </a:xfrm>
                    <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
                  </pic:spPr>
                </pic:pic>
              </a:graphicData>
            </a:graphic>
          </wp:anchor>
        </w:drawing>
      </w:r>
    </w:p>
    <w:sectPr>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="851" w:footer="992" w:gutter="0"/>
    </w:sectPr>
  </w:body>
</w:document>`

// Bytes 生成只含该印章图片的 .docx 内容，不落盘。
func Bytes(pic Picture) ([]byte, error) {
	if len(pic.PNG) == 0 {
		return nil, fmt.Errorf("docx: 图片数据为空")
	}
	if pic.WidthEMU <= 0 || pic.HeightEMU <= 0 {
		return nil, fmt.Errorf("docx: 图片尺寸非法 %dx%d", pic.WidthEMU, pic.HeightEMU)
	}

	behind := 0
	if pic.BehindText {
		behind = 1
	}
	doc := fmt.Sprintf(documentXML, behind, pic.XEMU, pic.YEMU, pic.WidthEMU, pic.HeightEMU)

	// [Content_Types].xml 放在最前面，兼容性最好。
	parts := []struct {
		name string
		data []byte
	}{
		{"[Content_Types].xml", []byte(contentTypesXML)},
		{"_rels/.rels", []byte(rootRelsXML)},
		{"word/document.xml", []byte(doc)},
		{"word/_rels/document.xml.rels", []byte(documentRelsXML)},
		{"word/media/seal.png", pic.PNG},
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		h := &zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: time.Now()}
		if p.name == "word/media/seal.png" {
			h.Method = zip.Store // PNG 已压缩，存储即可
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteNew 新建一个只含该印章图片的 .docx。
func WriteNew(path string, pic Picture) error {
	data, err := Bytes(pic)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// CMToEMU 把厘米换算成 EMU。
func CMToEMU(cm float64) int64 {
	return int64(cm*EMUPerCM + 0.5)
}
