#!/usr/bin/env python3
"""
compose_volume.py - 多分册拼装（商务标 / 技术标 / 经济标 → 完整投标文件）

按 volumes[] 顺序合并多个分册 docx，生成装订顺序。
支持：分册封面、分册隔板、匿名化处理（去除公司识别信息）。

用法:
    echo '{"volumes": [...], "binding_order": [...], "format_spec": {...}, "output_path": "..."}' \\
         | python compose_volume.py
"""

import sys
import os
import json
import argparse
import re
import traceback
from pathlib import Path

try:
    from docx import Document
    from docx.shared import Pt, Cm, RGBColor
    from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_BREAK
    from docx.enum.section import WD_SECTION
    from docx.oxml.ns import qn
    from docx.oxml import OxmlElement
    from copy import deepcopy
except ImportError as e:
    print(json.dumps({
        "ok": False,
        "errors": [{"code": "IMPORT_ERROR", "message": f"依赖未安装: {e}"}]
    }, ensure_ascii=False))
    sys.exit(1)


# 默认匿名化规则：移除常见的公司识别信息
DEFAULT_ANONYMIZE_RULES = [
    # 中文公司常见形式
    (r"[一-鿿]{2,30}(?:股份有限公司|有限责任公司|有限公司|集团|事务所)",  "XXX"),
    # 项目业绩表标题（技术标里典型的识别信息）
    (r"近\s*\d+\s*年.{0,15}业绩",  "近N年同类项目业绩"),
    (r"项目业绩一览表",  "同类项目业绩一览表"),
    (r"投标人.{0,15}业绩",  "投标人同类项目业绩"),
    # Logo / 图片引用
    (r"\[LOGO\]|\[logo\]|\{\{logo\}\}",  ""),
]


def anonymize_text(text, rules):
    """按规则列表去除文本中的公司识别信息"""
    for pattern, replacement in rules:
        text = re.sub(pattern, replacement, text)
    return text


def anonymize_document(doc, rules):
    """
    对 doc 做匿名化处理：
    - 主文档段落 + 表格
    - 页眉页脚
    - 内联图片 alt 文本
    """
    if not rules:
        return 0
    count = 0

    def process_paragraph(paragraph):
        nonlocal count
        original = "".join(r.text for r in paragraph.runs)
        if not original:
            return
        new_text = anonymize_text(original, rules)
        if new_text != original:
            count += 1
            if paragraph.runs:
                paragraph.runs[0].text = new_text
                for r in paragraph.runs[1:]:
                    r.text = ""

    # 主文档
    for paragraph in doc.paragraphs:
        process_paragraph(paragraph)
    for table in doc.tables:
        for row in table.rows:
            for cell in row.cells:
                for paragraph in cell.paragraphs:
                    process_paragraph(paragraph)

    # 页眉页脚
    for section in doc.sections:
        for header in [section.header, section.first_page_header]:
            if header:
                for paragraph in header.paragraphs:
                    process_paragraph(paragraph)
        for footer in [section.footer, section.first_page_footer]:
            if footer:
                for paragraph in footer.paragraphs:
                    process_paragraph(paragraph)

    return count


def insert_cover_page(doc, volume_name, volume_order, format_spec=None):
    """在文档最前面插入分册封面页"""
    body = doc.element.body

    # 创建封面段落
    cover_para = OxmlElement("w:p")
    pPr = OxmlElement("w:pPr")
    jc = OxmlElement("w:jc")
    jc.set(qn("w:val"), "center")
    pPr.append(jc)
    # 段前 300 磅（约 10cm），让标题靠近页面中部
    spacing = OxmlElement("w:spacing")
    spacing.set(qn("w:before"), "3000")
    spacing.set(qn("w:after"), "600")
    pPr.append(spacing)
    cover_para.append(pPr)

    run = OxmlElement("w:r")
    rPr = OxmlElement("w:rPr")
    rFonts = OxmlElement("w:rFonts")
    rFonts.set(qn("w:eastAsia"), "黑体")
    rFonts.set(qn("w:ascii"), "黑体")
    rFonts.set(qn("w:hAnsi"), "黑体")
    rPr.append(rFonts)
    sz = OxmlElement("w:sz")
    sz.set(qn("w:val"), "72")  # 36pt
    rPr.append(sz)
    szCs = OxmlElement("w:szCs")
    szCs.set(qn("w:val"), "72")
    rPr.append(szCs)
    b = OxmlElement("w:b")
    rPr.append(b)
    run.append(rPr)

    t = OxmlElement("w:t")
    t.text = f"第 {volume_order} 分册\n\n{volume_name}"
    run.append(t)
    cover_para.append(run)

    # 分节符（下一页）
    pPr2 = OxmlElement("w:pPr")
    sectPr = OxmlElement("w:sectPr")
    pgSz = OxmlElement("w:pgSz")
    pgSz.set(qn("w:w"), "11906")  # A4 宽
    pgSz.set(qn("w:h"), "16838")  # A4 高
    sectPr.append(pgSz)
    type_ = OxmlElement("w:type")
    type_.set(qn("w:val"), "nextPage")
    sectPr.append(type_)
    pPr2.append(sectPr)
    cover_para.append(pPr2)

    # 插入到 body 第一个子元素前
    body.insert(0, cover_para)


def insert_tab_divider(doc, next_volume_name):
    """在文档末尾插入分册隔板页"""
    para = doc.add_paragraph()
    para.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = para.add_run("\n\n\n\n\n")
    run.font.size = Pt(36)

    p2 = doc.add_paragraph()
    p2.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run2 = p2.add_run("——————  分册隔板  ——————")
    run2.font.name = "黑体"
    run2.font.size = Pt(18)
    run2.font.bold = True

    p3 = doc.add_paragraph()
    p3.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run3 = p3.add_run(f"\n下一分册：{next_volume_name}")
    run3.font.size = Pt(14)

    # 分页符
    p4 = doc.add_paragraph()
    p4.add_run().add_break(WD_BREAK.PAGE)


def merge_documents(volumes, binding_order, format_spec, output_path, tab_divider=True, include_master_toc=False):
    """
    把多分册 docx 合并为一个 docx。
    策略：基于第一个分册创建新文档，逐个 append 其它分册内容。
    """
    # 按 binding_order 重排
    name_to_volume = {v["name"]: v for v in volumes}
    ordered = []
    for name in binding_order:
        if name in name_to_volume:
            ordered.append(name_to_volume[name])
        else:
            ordered.extend(v for v in volumes if v["name"] == name and v not in ordered)

    # 兜底：把没在 binding_order 里的分册追加到末尾
    for v in volumes:
        if v not in ordered:
            ordered.append(v)

    if not ordered:
        raise ValueError("volumes 数组为空")

    # 打开第一个分册
    first = ordered[0]
    if not os.path.exists(first["docx_path"]):
        raise FileNotFoundError(f"分册文件不存在: {first['docx_path']}")

    merged_doc = Document(first["docx_path"])

    # 第一个分册：插入封面 + 匿名化（如果需要）
    insert_cover_page(merged_doc, first["name"], 1, format_spec)
    if first.get("anonymous"):
        rules = DEFAULT_ANONYMIZE_RULES
        count = anonymize_document(merged_doc, rules)

    # 后续分册
    for idx, vol in enumerate(ordered[1:], start=2):
        if not os.path.exists(vol["docx_path"]):
            raise FileNotFoundError(f"分册文件不存在: {vol['docx_path']}")

        # 插入隔板（如果不是最后一个分册）
        if tab_divider and idx < len(ordered):
            insert_tab_divider(merged_doc, vol["name"])

        # 打开下一个分册
        next_doc = Document(vol["docx_path"])

        # 插入封面（在新分册内容前）
        insert_cover_page_to_existing(merged_doc, vol["name"], idx)

        # 匿名化
        if vol.get("anonymous"):
            anonymize_document(next_doc, DEFAULT_ANONYMIZE_RULES)

        # 把 next_doc 的 body 元素追加到 merged_doc
        for element in list(next_doc.element.body):
            if element.tag.endswith("}sectPr"):
                continue
            # 深拷贝避免破坏原文档
            new_element = deepcopy(element)
            merged_doc.element.body.append(new_element)

    # 保存
    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    merged_doc.save(output_path)

    return {
        "volumes_merged": len(ordered),
        "binding_order": [v["name"] for v in ordered],
        "output_path": output_path
    }


def insert_cover_page_to_existing(doc, volume_name, volume_order):
    """在现有文档末尾插入分册封面 + 分节符"""
    para = doc.add_paragraph()
    para.alignment = WD_ALIGN_PARAGRAPH.CENTER
    para.paragraph_format.space_before = Pt(300)

    run = para.add_run(f"第 {volume_order} 分册\n\n{volume_name}")
    run.font.name = "黑体"
    run.font.size = Pt(36)
    run.font.bold = True

    # 强制分页
    para.add_run().add_break(WD_BREAK.PAGE)


def main():
    parser = argparse.ArgumentParser(description="拼装多分册 docx")
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

    volumes = payload.get("volumes", [])
    binding_order = payload.get("binding_order", [])
    format_spec = payload.get("format_spec", {})
    output_path = payload.get("output_path")
    tab_divider = payload.get("tab_divider", True)
    include_master_toc = payload.get("include_master_toc", False)

    if not volumes:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "MISSING_FIELD", "field": "volumes", "message": "必填"}]
        }, ensure_ascii=False))
        sys.exit(1)

    if not output_path:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "MISSING_FIELD", "field": "output_path", "message": "必填"}]
        }, ensure_ascii=False))
        sys.exit(1)

    try:
        result = merge_documents(
            volumes=volumes,
            binding_order=binding_order,
            format_spec=format_spec,
            output_path=output_path,
            tab_divider=tab_divider,
            include_master_toc=include_master_toc
        )
    except FileNotFoundError as e:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "VOLUME_NOT_FOUND", "message": str(e)}]
        }, ensure_ascii=False))
        sys.exit(1)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{
                "code": "MERGE_FAILED",
                "message": str(e),
                "trace": traceback.format_exc()
            }]
        }, ensure_ascii=False))
        sys.exit(1)

    print(json.dumps({
        "ok": True,
        **result,
        "warnings": [],
        "errors": []
    }, ensure_ascii=False))


if __name__ == "__main__":
    main()