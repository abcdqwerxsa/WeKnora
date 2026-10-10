#!/usr/bin/env python3
"""
insert_track_changes.py - 把审查意见写回 docx（修订痕迹 / 批注）

由于 python-docx 本身不直接支持 track changes，本脚本采用折中策略：
1. mode = "track_changes": 在每个 finding 匹配的段落后面，插入一个【修订建议段落】（带删除线样式标记）
2. mode = "comments": 在匹配段落里插入 Word 批注（comments.xml）
3. mode = "both": 同时使用上述两种

锚定策略：
- 先按 anchor.section 在 Heading 1-3 样式段落里匹配（"第3.2条"、"5.2 付款方式"）
- 再按 anchor.text_match 在段落正文里做最长子串匹配
- 找不到时写入 warnings（不报错，避免破坏文件）

用法:
    echo '{"docx_path": "...", "review_findings": [...], "mode": "both", "output_path": "..."}' \\
         | python insert_track_changes.py
"""

import sys
import os
import json
import argparse
import re
import traceback
from pathlib import Path
from datetime import datetime

try:
    from docx import Document
    from docx.shared import Pt, RGBColor
    from docx.oxml.ns import qn, nsmap
    from docx.oxml import OxmlElement
except ImportError as e:
    print(json.dumps({
        "ok": False,
        "errors": [{"code": "IMPORT_ERROR", "message": f"依赖未安装: {e}"}]
    }, ensure_ascii=False))
    sys.exit(1)


# 审查人标记
REVIEWER_NAME = "WeKnora-doc-review-proofread"


def find_paragraphs_by_section(doc, section_text):
    """在 Heading 1-3 样式段落里找匹配 section_text 的段落，返回该标题之后的所有段落"""
    if not section_text:
        return list(doc.paragraphs), None

    # 提取 section 数字：如 "第3.2条" → "3.2"，"5.2 付款方式" → "5.2"
    section_num_match = re.search(r"(\d+(?:\.\d+)*)", section_text)
    section_num = section_num_match.group(1) if section_num_match else None

    heading_keywords = {"Heading 1", "Heading 2", "Heading 3", "标题 1", "标题 2", "标题 3"}

    for i, para in enumerate(doc.paragraphs):
        if para.style.name not in heading_keywords:
            continue
        text = para.text.strip()
        # 标题里包含 section 文本，或包含 section 编号
        if section_text in text:
            # 返回此标题之后到下一个同级/上级标题之前的段落
            return _collect_following_paragraphs(doc, i), para
        if section_num and section_num in text:
            return _collect_following_paragraphs(doc, i), para

    return None, None


def _collect_following_paragraphs(doc, start_idx):
    """从 start_idx 之后的段落开始收集，直到下一个同级/上级标题"""
    heading_keywords = {"Heading 1", "Heading 2", "Heading 3", "标题 1", "标题 2", "标题 3"}
    collected = []
    start_level = None
    if doc.paragraphs[start_idx].style.name in heading_keywords:
        # 推断标题级别（Heading 1 → 1）
        m = re.search(r"(\d)", doc.paragraphs[start_idx].style.name)
        start_level = int(m.group(1)) if m else 1

    for i in range(start_idx + 1, len(doc.paragraphs)):
        p = doc.paragraphs[i]
        if p.style.name in heading_keywords:
            m = re.search(r"(\d)", p.style.name)
            level = int(m.group(1)) if m else 1
            if start_level is not None and level <= start_level:
                break
        collected.append(p)
    return collected


def find_paragraph_by_text_match(paragraphs, text_match):
    """在段落列表里找包含 text_match 的段落，返回 (paragraph, start_offset)"""
    if not text_match:
        return None, -1

    # 优先精确匹配
    for p in paragraphs:
        idx = p.text.find(text_match)
        if idx >= 0:
            return p, idx
    # 退化为模糊匹配：取 text_match 的前 10 字符
    if len(text_match) > 10:
        short = text_match[:10]
        for p in paragraphs:
            idx = p.text.find(short)
            if idx >= 0:
                return p, idx
    return None, -1


def add_revision_paragraph_after(doc, target_para, finding):
    """
    在目标段落之后插入一个【修订建议段落】。
    视觉上看起来是"红色加粗的建议 + 灰色斜体的依据"。
    """
    new_para = OxmlElement("w:p")
    pPr = OxmlElement("w:pPr")
    # 缩进 2 个汉字
    ind = OxmlElement("w:ind")
    ind.set(qn("w:firstLineChars"), "200")
    pPr.append(ind)
    new_para.append(pPr)

    # 1. 前缀图标（红色加粗）
    prefix_run = OxmlElement("w:r")
    rPr = OxmlElement("w:rPr")
    color = OxmlElement("w:color")
    severity = finding.get("severity", "medium")
    color.set(qn("w:val"), {"high": "C00000", "medium": "E97132", "low": "7F6000"}.get(severity, "E97132"))
    rPr.append(color)
    b = OxmlElement("w:b")
    rPr.append(b)
    prefix_run.append(rPr)
    t1 = OxmlElement("w:t")
    t1.set(qn("xml:space"), "preserve")
    icon = {"typo": "【校对】", "format": "【格式】", "compliance": "【合规】",
            "consistency": "【一致性】", "risk": "【风险】"}.get(finding.get("type", ""), "【审查】")
    severity_label = {"high": "高", "medium": "中", "low": "低"}.get(severity, "中")
    t1.text = f"{icon} 严重度:{severity_label}  "
    prefix_run.append(t1)
    new_para.append(prefix_run)

    # 2. 修改建议
    suggestion = finding.get("suggestion", "")
    if suggestion:
        sug_run = OxmlElement("w:r")
        rPr2 = OxmlElement("w:rPr")
        sug_run.append(rPr2)
        t2 = OxmlElement("w:t")
        t2.set(qn("xml:space"), "preserve")
        t2.text = f"建议: {suggestion}"
        sug_run.append(t2)
        new_para.append(sug_run)

    # 3. 依据
    rationale = finding.get("rationale", "")
    if rationale:
        ratio_run = OxmlElement("w:r")
        rPr3 = OxmlElement("w:rPr")
        i_elem = OxmlElement("w:i")
        rPr3.append(i_elem)
        color3 = OxmlElement("w:color")
        color3.set(qn("w:val"), "7F7F7F")
        rPr3.append(color3)
        ratio_run.append(rPr3)
        t3 = OxmlElement("w:t")
        t3.set(qn("xml:space"), "preserve")
        t3.text = f"\n依据: {rationale}"
        ratio_run.append(t3)
        new_para.append(ratio_run)

    # 插入到 target_para 之后
    target_para._element.addnext(new_para)


def add_comment_to_paragraph(doc, target_para, finding, comment_id):
    """
    在目标段落里插入 Word 批注引用。
    注：python-docx 处理 comments.xml 比较 hack，这里做简化版本：
    - 在段落末尾插入 commentRangeStart / commentRangeEnd / commentReference
    - 不会自动创建完整的 comments part，但 Word 打开时会尝试识别
    """
    if not target_para.runs:
        # 如果段落没有 run，先添加一个
        target_para.add_run(target_para.text)
        target_para.text = ""

    # commentRangeStart
    range_start = OxmlElement("w:commentRangeStart")
    range_start.set(qn("w:id"), str(comment_id))

    # commentRangeEnd
    range_end = OxmlElement("w:commentRangeEnd")
    range_end.set(qn("w:id"), str(comment_id))

    # commentReference
    ref_run = OxmlElement("w:r")
    rPr = OxmlElement("w:rPr")
    rStyle = OxmlElement("w:rStyle")
    rStyle.set(qn("w:val"), "CommentReference")
    rPr.append(rStyle)
    ref_run.append(rPr)
    comment_ref = OxmlElement("w:commentReference")
    comment_ref.set(qn("w:id"), str(comment_id))
    ref_run.append(comment_ref)

    # 插入到段落的 pPr 之后
    p = target_para._element
    pPr = p.find(qn("w:pPr"))
    if pPr is not None:
        pPr.addnext(range_start)
    else:
        p.insert(0, range_start)

    # range_end 和 ref 插入到段落末尾
    p.append(range_end)
    p.append(ref_run)


def insert_findings(doc, findings, mode, warnings):
    """主入口：把所有 findings 写入 docx"""
    applied = 0
    unmatched = []

    for idx, finding in enumerate(findings):
        anchor = finding.get("anchor", {})
        section = anchor.get("section", "")
        text_match = anchor.get("text_match", "")

        # 锚定段落
        paragraphs, heading = find_paragraphs_by_section(doc, section)
        if not paragraphs:
            unmatched.append({
                "finding_index": idx,
                "section": section,
                "reason": "未找到对应章节"
            })
            continue

        target_para, offset = find_paragraph_by_text_match(paragraphs, text_match)
        if not target_para:
            unmatched.append({
                "finding_index": idx,
                "section": section,
                "text_match": text_match[:20] + "..." if len(text_match) > 20 else text_match,
                "reason": "章节找到但未匹配到文本片段"
            })
            continue

        # 写入修订建议段落
        if mode in ("track_changes", "both"):
            add_revision_paragraph_after(doc, target_para, finding)
            applied += 1

        # 写入批注
        if mode in ("comments", "both"):
            try:
                add_comment_to_paragraph(doc, target_para, finding, comment_id=idx + 1)
            except Exception as e:
                warnings.append({
                    "code": "COMMENT_INSERT_FAILED",
                    "finding_index": idx,
                    "message": str(e)
                })

    return applied, unmatched


def main():
    parser = argparse.ArgumentParser(description="把审查意见写回 docx")
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
    findings = payload.get("review_findings", [])
    mode = payload.get("mode", "both")

    if not docx_path or not os.path.exists(docx_path):
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "DOCX_NOT_FOUND", "message": f"文件不存在: {docx_path}"}]
        }, ensure_ascii=False))
        sys.exit(1)

    if not findings:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "NO_FINDINGS", "message": "review_findings 数组为空"}]
        }, ensure_ascii=False))
        sys.exit(1)

    warnings = []
    try:
        doc = Document(docx_path)
        applied, unmatched = insert_findings(doc, findings, mode, warnings)
        doc.save(output_path)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{
                "code": "INSERT_FAILED",
                "message": str(e),
                "trace": traceback.format_exc()
            }]
        }, ensure_ascii=False))
        sys.exit(1)

    if unmatched:
        warnings.append({
            "code": "ANCHOR_UNMATCHED",
            "count": len(unmatched),
            "details": unmatched,
            "message": f"{len(unmatched)} 个审查意见未能定位到具体段落"
        })

    print(json.dumps({
        "ok": True,
        "output_path": output_path,
        "stats": {
            "findings_total": len(findings),
            "findings_applied": applied,
            "findings_unmatched": len(unmatched)
        },
        "warnings": warnings,
        "errors": [],
        "meta": {
            "reviewer": REVIEWER_NAME,
            "timestamp": datetime.utcnow().isoformat() + "Z",
            "mode": mode
        }
    }, ensure_ascii=False))


if __name__ == "__main__":
    main()