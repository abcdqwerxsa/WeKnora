#!/usr/bin/env python3
"""
apply_format_spec.py - 把 format_spec 应用到 docx

可作为模块被 fill_template.py 调用，也可独立从 stdin 接收 JSON 直接调整格式。

用法（独立 CLI）:
    echo '{"docx_path": "...", "format_spec": {...}, "output_path": "..."}' \\
         | python apply_format_spec.py

用法（作为模块）:
    from apply_format_spec import apply_format_spec
    apply_format_spec(doc, format_spec)
"""

import sys
import os
import json
import argparse
import traceback
from pathlib import Path

try:
    from docx import Document
    from docx.shared import Pt, Cm, Inches, Mm, RGBColor, Emu
    from docx.enum.text import WD_ALIGN_PARAGRAPH
    from docx.oxml.ns import qn
    from docx.oxml import OxmlElement
except ImportError as e:
    print(json.dumps({
        "ok": False,
        "errors": [{"code": "IMPORT_ERROR", "message": f"依赖未安装: {e}"}]
    }, ensure_ascii=False))
    sys.exit(1)


# 单位转换
UNIT_CONVERTERS = {
    "cm": Cm,
    "inch": Inches,
    "pt": Pt
}


def parse_size(value, unit):
    """根据单位把数值转为 python-docx 长度对象"""
    converter = UNIT_CONVERTERS.get(unit, Cm)
    return converter(value)


def apply_page_setup(section, page_spec):
    """应用页面设置：尺寸、方向、页边距"""
    size = page_spec.get("size", "A4")
    orientation = page_spec.get("orientation", "portrait")
    margins = page_spec.get("margins", {})

    # 页面尺寸（EMU 单位）
    # A4: 210mm × 297mm
    page_sizes = {
        "A4": (Cm(21), Cm(29.7)),
        "A3": (Cm(29.7), Cm(42)),
        "A5": (Cm(14.8), Cm(21)),
        "Letter": (Inches(8.5), Inches(11)),
        "Legal": (Inches(8.5), Inches(14))
    }
    if size in page_sizes:
        w, h = page_sizes[size]
        if orientation == "landscape":
            section.page_width, section.page_height = h, w
            section.orientation = 1  # WD_ORIENT.LANDSCAPE
        else:
            section.page_width, section.page_height = w, h
            section.orientation = 0

    unit = margins.get("unit", "cm")
    if "top" in margins:
        section.top_margin = parse_size(margins["top"], unit)
    if "bottom" in margins:
        section.bottom_margin = parse_size(margins["bottom"], unit)
    if "left" in margins:
        section.left_margin = parse_size(margins["left"], unit)
    if "right" in margins:
        section.right_margin = parse_size(margins["right"], unit)


def apply_font_to_style(style, font_spec, is_asian=False):
    """把字体设置应用到样式"""
    if not font_spec:
        return
    font = style.font
    if "family" in font_spec:
        font.name = font_spec["family"]
        # 中文字体需要在 eastAsia 上显式设置才生效
        if is_asian and style.element is not None:
            rPr = style.element.get_or_add_rPr()
            rFonts = rPr.find(qn("w:rFonts"))
            if rFonts is None:
                rFonts = OxmlElement("w:rFonts")
                rPr.append(rFonts)
            rFonts.set(qn("w:eastAsia"), font_spec["family"])
            rFonts.set(qn("w:ascii"), font_spec["family"])
            rFonts.set(qn("w:hAnsi"), font_spec["family"])
    if "size_pt" in font_spec:
        font.size = Pt(font_spec["size_pt"])
    if "bold" in font_spec:
        font.bold = font_spec["bold"]
    if "italic" in font_spec:
        font.italic = font_spec["italic"]
    if "color" in font_spec:
        color_str = font_spec["color"].lstrip("#")
        try:
            font.color.rgb = RGBColor.from_string(color_str)
        except Exception:
            pass


def apply_font_to_run(run, font_spec):
    """把字体设置应用到单个 run（含中文 eastAsia）"""
    if not font_spec:
        return
    if "family" in font_spec:
        run.font.name = font_spec["family"]
        rPr = run._element.get_or_add_rPr()
        rFonts = rPr.find(qn("w:rFonts"))
        if rFonts is None:
            rFonts = OxmlElement("w:rFonts")
            rPr.append(rFonts)
        rFonts.set(qn("w:eastAsia"), font_spec["family"])
        rFonts.set(qn("w:ascii"), font_spec["family"])
        rFonts.set(qn("w:hAnsi"), font_spec["family"])
    if "size_pt" in font_spec:
        run.font.size = Pt(font_spec["size_pt"])
    if "bold" in font_spec:
        run.font.bold = font_spec["bold"]
    if "italic" in font_spec:
        run.font.italic = font_spec["italic"]


def apply_paragraph_format(paragraph, paragraph_spec):
    """应用段落格式"""
    pf = paragraph.paragraph_format
    if "line_spacing" in paragraph_spec:
        pf.line_spacing = paragraph_spec["line_spacing"]
    if "alignment" in paragraph_spec:
        align_map = {
            "left": WD_ALIGN_PARAGRAPH.LEFT,
            "center": WD_ALIGN_PARAGRAPH.CENTER,
            "right": WD_ALIGN_PARAGRAPH.RIGHT,
            "justify": WD_ALIGN_PARAGRAPH.JUSTIFY
        }
        pf.alignment = align_map.get(paragraph_spec["alignment"], WD_ALIGN_PARAGRAPH.JUSTIFY)

    # 首行缩进
    first_indent_chars = paragraph_spec.get("first_line_indent_chars", 0)
    if first_indent_chars > 0:
        # 中文字符首行缩进：按字符大小估算
        # 简化：直接缩进 2 个字符宽 ≈ font.size * first_indent_chars
        if paragraph.runs:
            font_size = paragraph.runs[0].font.size
            if font_size:
                pf.first_line_indent = font_size * first_indent_chars

    if "space_before_pt" in paragraph_spec:
        pf.space_before = Pt(paragraph_spec["space_before_pt"])
    if "space_after_pt" in paragraph_spec:
        pf.space_after = Pt(paragraph_spec["space_after_pt"])


def apply_fonts(doc, fonts_spec):
    """应用字体设置到 Normal / Heading 1-3 样式"""
    style_map = {
        "body": "Normal",
        "heading_1": "Heading 1",
        "heading_2": "Heading 2",
        "heading_3": "Heading 3",
        "caption": "Caption",
        "code": "No Spacing"
    }
    for key, style_name in style_map.items():
        spec = fonts_spec.get(key)
        if spec and style_name in [s.name for s in doc.styles]:
            try:
                style = doc.styles[style_name]
                apply_font_to_style(style, spec, is_asian=True)
            except Exception:
                # 样式不存在时静默跳过
                pass


def apply_paragraph_format_all(doc, paragraph_spec):
    """把段落格式应用到所有已有段落"""
    for paragraph in doc.paragraphs:
        apply_paragraph_format(paragraph, paragraph_spec)
    for table in doc.tables:
        for row in table.rows:
            for cell in row.cells:
                for paragraph in cell.paragraphs:
                    apply_paragraph_format(paragraph, paragraph_spec)


def apply_header_footer(doc, hf_spec):
    """应用页眉页脚 + 页码"""
    page_num_pos = hf_spec.get("page_number_position", "footer_center")
    sections = doc.sections
    for section in sections:
        header = section.header
        footer = section.footer

        # 页眉
        if "header" in hf_spec:
            h = hf_spec["header"]
            if "text" in h:
                if not header.paragraphs:
                    header.add_paragraph()
                p = header.paragraphs[0]
                p.text = h["text"]
                if "alignment" in h:
                    align_map = {
                        "left": WD_ALIGN_PARAGRAPH.LEFT,
                        "center": WD_ALIGN_PARAGRAPH.CENTER,
                        "right": WD_ALIGN_PARAGRAPH.RIGHT
                    }
                    p.alignment = align_map.get(h["alignment"], WD_ALIGN_PARAGRAPH.CENTER)
                if "font" in h and p.runs:
                    apply_font_to_run(p.runs[0], h["font"])

        # 页脚
        if "footer" in hf_spec:
            f = hf_spec["footer"]
            footer_text = f.get("text", "")
            if "alignment" in f:
                align_map = {
                    "left": WD_ALIGN_PARAGRAPH.LEFT,
                    "center": WD_ALIGN_PARAGRAPH.CENTER,
                    "right": WD_ALIGN_PARAGRAPH.RIGHT
                }
                if footer.paragraphs:
                    footer.paragraphs[0].alignment = align_map.get(f["alignment"], WD_ALIGN_PARAGRAPH.CENTER)

        # 页码
        if page_num_pos != "none":
            _add_page_number(footer if "footer" in page_num_pos else header,
                             page_num_pos)

        # 区分首页
        if hf_spec.get("different_first_page"):
            section.different_first_page_header_footer = True


def _add_page_number(footer_or_header, position):
    """在页脚/页眉插入 PAGE / NUMPAGES 域"""
    if not footer_or_header.paragraphs:
        footer_or_header.add_paragraph()
    p = footer_or_header.paragraphs[0]

    if "right" in position:
        p.alignment = WD_ALIGN_PARAGRAPH.RIGHT
    elif "center" in position:
        p.alignment = WD_ALIGN_PARAGRAPH.CENTER
    else:
        p.alignment = WD_ALIGN_PARAGRAPH.LEFT

    run = p.add_run()
    # 清空已有内容
    p.text = ""

    # 插入 PAGE 域
    fldChar_begin = OxmlElement("w:fldChar")
    fldChar_begin.set(qn("w:fldCharType"), "begin")
    instrText = OxmlElement("w:instrText")
    instrText.set(qn("xml:space"), "preserve")
    instrText.text = " PAGE "
    fldChar_end = OxmlElement("w:fldChar")
    fldChar_end.set(qn("w:fldCharType"), "end")

    run._r.append(fldChar_begin)
    run._r.append(instrText)
    run._r.append(fldChar_end)

    run = p.add_run(" / ")
    # 插入 NUMPAGES 域
    fldChar_begin2 = OxmlElement("w:fldChar")
    fldChar_begin2.set(qn("w:fldCharType"), "begin")
    instrText2 = OxmlElement("w:instrText")
    instrText2.set(qn("xml:space"), "preserve")
    instrText2.text = " NUMPAGES "
    fldChar_end2 = OxmlElement("w:fldChar")
    fldChar_end2.set(qn("w:fldCharType"), "end")

    run2 = p.add_run()
    run2._r.append(fldChar_begin2)
    run2._r.append(instrText2)
    run2._r.append(fldChar_end2)


def apply_format_spec(doc, format_spec):
    """
    把 format_spec 应用到已打开的 Document 对象。
    这是模块级导出函数，可被 fill_template.py 调用。
    """
    applied = []

    # 1. 页面设置（应用到所有 sections）
    if "page" in format_spec:
        for section in doc.sections:
            apply_page_setup(section, format_spec["page"])
        applied.append("page")

    # 2. 字体
    if "fonts" in format_spec:
        apply_fonts(doc, format_spec["fonts"])
        applied.append("fonts")

    # 3. 段落格式
    if "paragraph" in format_spec:
        apply_paragraph_format_all(doc, format_spec["paragraph"])
        applied.append("paragraph")

    # 4. 页眉页脚
    if "header_footer" in format_spec:
        apply_header_footer(doc, format_spec["header_footer"])
        applied.append("header_footer")

    return applied


def main():
    parser = argparse.ArgumentParser(description="把 format_spec 应用到 docx")
    parser.add_argument("--file", help="输入 JSON 文件路径")
    args = parser.parse_args()

    try:
        if args.file:
            with open(args.file, "r", encoding="utf-8") as f:
                payload = json.load(f)
        elif not sys.stdin.isatty():
            raw = sys.stdin.read()
            payload = json.loads(raw) if raw.strip() else {}
        else:
            raise ValueError("请通过 stdin 传入 JSON 或使用 --file")
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "INPUT_INVALID", "message": str(e)}]
        }, ensure_ascii=False))
        sys.exit(1)

    docx_path = payload.get("docx_path")
    output_path = payload.get("output_path", docx_path)
    format_spec = payload.get("format_spec", {})

    if not docx_path or not os.path.exists(docx_path):
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "DOCX_NOT_FOUND", "message": f"文件不存在: {docx_path}"}]
        }, ensure_ascii=False))
        sys.exit(1)

    try:
        doc = Document(docx_path)
        applied = apply_format_spec(doc, format_spec)
        doc.save(output_path)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "APPLY_FAILED", "message": str(e),
                        "trace": traceback.format_exc()}]
        }, ensure_ascii=False))
        sys.exit(1)

    print(json.dumps({
        "ok": True,
        "output_path": output_path,
        "applied": applied,
        "warnings": [],
        "errors": []
    }, ensure_ascii=False))


if __name__ == "__main__":
    main()