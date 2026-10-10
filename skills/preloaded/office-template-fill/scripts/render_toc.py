#!/usr/bin/env python3
"""
render_toc.py - 自动生成目录（TOC field + 静态备份）

策略：
1. 扫描文档中所有 Heading 1-3 样式的段落
2. 在指定位置插入 TOC 域（Word 打开时按 F9 即可更新页码）
3. 同时生成一个静态的目录条目列表作为备份（LibreOffice/WPS/移动端可显示）

用法:
    echo '{"docx_path": "...", "levels": 3, "output_path": "..."}' \\
         | python render_toc.py
"""

import sys
import os
import json
import argparse
import traceback
from pathlib import Path

try:
    from docx import Document
    from docx.shared import Pt
    from docx.enum.text import WD_ALIGN_PARAGRAPH
    from docx.oxml.ns import qn
    from docx.oxml import OxmlElement
except ImportError as e:
    print(json.dumps({
        "ok": False,
        "errors": [{"code": "IMPORT_ERROR", "message": f"依赖未安装: {e}"}]
    }, ensure_ascii=False))
    sys.exit(1)


HEADING_STYLE_PREFIXES = ("Heading ", "标题 ")


def is_heading_style(style_name, max_level):
    """判断段落样式是否为 1-max_level 级的标题"""
    if not style_name:
        return False
    for prefix in HEADING_STYLE_PREFIXES:
        if style_name.startswith(prefix):
            num_part = style_name[len(prefix):].strip()
            try:
                level = int(num_part)
                return 1 <= level <= max_level
            except (ValueError, TypeError):
                continue
    return False


def extract_headings(doc, max_level):
    """提取所有 Heading 1-max_level 的 (level, text) 列表"""
    headings = []
    for para in doc.paragraphs:
        if is_heading_style(para.style.name, max_level):
            level = int(para.style.name.split()[-1])
            headings.append((level, para.text.strip(), para))
    return headings


def build_toc_field_xml(max_level):
    r"""
    构建 TOC field code 的 XML。
    语法： TOC \o "1-3" \h \z \u
    \o "1-N": 包含级别 1-N 的标题
    \h: 生成超链接
    \z: 隐藏网页布局
    \u: 使用大纲级别
    """
    return f' TOC \\o "1-{max_level}" \\h \\z \\u '


def insert_toc_field(doc, max_level):
    """在文档开头插入 TOC field（Word 打开时会自动展开）"""
    body = doc.element.body
    first_child = None
    for child in body:
        if not child.tag.endswith("}sectPr"):
            first_child = child
            break
    if first_child is None:
        return

    # 创建一个新的段落，包含 TOC field
    toc_para = OxmlElement("w:p")
    pPr = OxmlElement("w:pPr")
    toc_para.append(pPr)

    # field begin
    run = OxmlElement("w:r")
    fldChar_begin = OxmlElement("w:fldChar")
    fldChar_begin.set(qn("w:fldCharType"), "begin")
    fldChar_begin.set(qn("w:dirty"), "true")
    run.append(fldChar_begin)
    toc_para.append(run)

    # field instruction
    run2 = OxmlElement("w:r")
    instr = OxmlElement("w:instrText")
    instr.set(qn("xml:space"), "preserve")
    instr.text = build_toc_field_xml(max_level)
    run2.append(instr)
    toc_para.append(run2)

    # field separator
    run3 = OxmlElement("w:r")
    fldChar_sep = OxmlElement("w:fldChar")
    fldChar_sep.set(qn("w:fldCharType"), "separate")
    run3.append(fldChar_sep)
    toc_para.append(run3)

    # 占位文本（用户打开文档前看到的内容）
    run4 = OxmlElement("w:r")
    rPr = OxmlElement("w:rPr")
    i_elem = OxmlElement("w:i")
    rPr.append(i_elem)
    run4.append(rPr)
    t = OxmlElement("w:t")
    t.set(qn("xml:space"), "preserve")
    t.text = "（请按 F9 或在 Word 中右键选择更新域以填充目录）"
    run4.append(t)
    toc_para.append(run4)

    # field end
    run5 = OxmlElement("w:r")
    fldChar_end = OxmlElement("w:fldChar")
    fldChar_end.set(qn("w:fldCharType"), "end")
    run5.append(fldChar_end)
    toc_para.append(run5)

    # 分页符（目录后强制分页）
    page_break = OxmlElement("w:p")
    pb_run = OxmlElement("w:r")
    br = OxmlElement("w:br")
    br.set(qn("w:type"), "page")
    pb_run.append(br)
    page_break.append(pb_run)

    # 插入到第一个内容元素前
    first_child.addprevious(toc_para)
    first_child.addprevious(page_break)


def build_static_toc(doc, headings, include_page_number, title):
    """生成静态目录（不依赖 Word 域），适合 LibreOffice/WPS/移动端"""
    if not headings:
        return

    body = doc.element.body
    # 找到第一个 Heading 之前的位置
    insert_before = None
    for child in body:
        if child.tag.endswith("}p"):
            for r in child.iter(qn("w:pStyle")):
                style_val = r.get(qn("w:val")) or ""
                if style_val.startswith("Heading") or style_val.startswith("标题"):
                    insert_before = child
                    break
            if insert_before is not None:
                break

    if insert_before is None:
        return

    new_paragraphs = []

    # 标题
    title_para = OxmlElement("w:p")
    title_pPr = OxmlElement("w:pPr")
    jc = OxmlElement("w:jc")
    jc.set(qn("w:val"), "center")
    title_pPr.append(jc)
    sp = OxmlElement("w:spacing")
    sp.set(qn("w:before"), "240")
    sp.set(qn("w:after"), "240")
    title_pPr.append(sp)
    title_para.append(title_pPr)
    title_run = OxmlElement("w:r")
    title_rPr = OxmlElement("w:rPr")
    rFonts = OxmlElement("w:rFonts")
    rFonts.set(qn("w:eastAsia"), "黑体")
    rFonts.set(qn("w:ascii"), "黑体")
    rFonts.set(qn("w:hAnsi"), "黑体")
    title_rPr.append(rFonts)
    sz = OxmlElement("w:sz")
    sz.set(qn("w:val"), "36")  # 18pt
    title_rPr.append(sz)
    b = OxmlElement("w:b")
    title_rPr.append(b)
    title_run.append(title_rPr)
    title_t = OxmlElement("w:t")
    title_t.text = title
    title_run.append(title_t)
    title_para.append(title_run)
    new_paragraphs.append(title_para)

    # 目录条目
    for level, text, _para in headings:
        item_para = OxmlElement("w:p")
        item_pPr = OxmlElement("w:pPr")
        # 缩进按级别
        ind = OxmlElement("w:ind")
        ind.set(qn("w:firstLineChars"), str(200 * level))
        ind.set(qn("w:firstLine"), str(480 * (level - 1)))
        item_pPr.append(ind)
        sp = OxmlElement("w:spacing")
        sp.set(qn("w:line"), "360")
        sp.set(qn("w:lineRule"), "auto")
        item_pPr.append(sp)
        item_para.append(item_pPr)

        run = OxmlElement("w:r")
        rPr = OxmlElement("w:rPr")
        sz2 = OxmlElement("w:sz")
        sz2.set(qn("w:val"), "24" if level == 1 else "22")
        rPr.append(sz2)
        if level == 1:
            b2 = OxmlElement("w:b")
            rPr.append(b2)
        run.append(rPr)
        t = OxmlElement("w:t")
        t.set(qn("xml:space"), "preserve")
        marker = "  " * (level - 1)
        page_text = f" ......... " if include_page_number else ""
        t.text = f"{marker}{text}{page_text} "
        run.append(t)
        item_para.append(run)
        new_paragraphs.append(item_para)

    # 分页
    page_break = OxmlElement("w:p")
    pb_run = OxmlElement("w:r")
    br = OxmlElement("w:br")
    br.set(qn("w:type"), "page")
    pb_run.append(br)
    page_break.append(pb_run)
    new_paragraphs.append(page_break)

    # 插入
    for p in new_paragraphs:
        insert_before.addprevious(p)


def render_toc(docx_path, levels, include_page_number, title, insert_position, output_path):
    """主入口：生成目录并保存"""
    doc = Document(docx_path)
    headings = extract_headings(doc, levels)

    if insert_position == "before_first_heading":
        if include_page_number:
            # 同时插入 TOC field 和静态目录
            insert_toc_field(doc, levels)
        build_static_toc(doc, headings, include_page_number, title)
    elif insert_position == "after_cover":
        # 找到第一个标题，在它前面插入
        build_static_toc(doc, headings, include_page_number, title)
    elif insert_position == "manual":
        # 不主动插入，仅在末尾追加目录备用页
        pass

    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    doc.save(output_path)

    return {
        "headings_found": len(headings),
        "levels_included": levels,
        "headings_sample": [(lvl, text) for lvl, text, _ in headings[:10]]
    }


def main():
    parser = argparse.ArgumentParser(description="自动生成目录")
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
    levels = int(payload.get("levels", 3))
    include_page_number = bool(payload.get("include_page_number", True))
    title = payload.get("title", "目  录")  # 目  录
    insert_position = payload.get("insert_position", "before_first_heading")

    if not docx_path or not os.path.exists(docx_path):
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "DOCX_NOT_FOUND", "message": f"文件不存在: {docx_path}"}]
        }, ensure_ascii=False))
        sys.exit(1)

    if levels < 1 or levels > 9:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "INVALID_LEVELS", "message": "levels 必须在 1-9 之间"}]
        }, ensure_ascii=False))
        sys.exit(1)

    try:
        result = render_toc(docx_path, levels, include_page_number, title, insert_position, output_path)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{
                "code": "RENDER_FAILED",
                "message": str(e),
                "trace": traceback.format_exc()
            }]
        }, ensure_ascii=False))
        sys.exit(1)

    print(json.dumps({
        "ok": True,
        "output_path": output_path,
        "stats": result,
        "warnings": [],
        "errors": []
    }, ensure_ascii=False))


if __name__ == "__main__":
    main()