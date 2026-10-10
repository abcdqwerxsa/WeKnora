#!/usr/bin/env python3
"""
fill_template.py - 模板填充 + 排版应用主入口

按 format_spec + fields 填充公司 docx 模板，输出符合招标文件格式要求的 .docx。

用法（通过 stdin JSON，推荐）:
    echo '{"template_path": "...", "format_spec": {...}, "fields": {...}, "output_path": "..."}' \\
         | python fill_template.py

用法（通过 --file 参数，模板字段已在 JSON 文件里）:
    python fill_template.py --file /path/to/input.json

输出：stdout 返回结构化 JSON（ok / stats / warnings / errors）
"""

import sys
import os
import json
import argparse
import re
import traceback
from pathlib import Path

# 把脚本所在目录加入 sys.path，便于 import 同 skill 的其它脚本
SKILL_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SKILL_DIR))

try:
    from docx import Document
    from docx.shared import Pt, Cm, RGBColor
    from docx.enum.text import WD_ALIGN_PARAGRAPH
    from jsonschema import validate, ValidationError
except ImportError as e:
    print(json.dumps({
        "ok": False,
        "errors": [{"code": "IMPORT_ERROR", "message": f"依赖未安装: {e}"}]
    }, ensure_ascii=False))
    sys.exit(1)

try:
    # 同 skill 其它脚本
    from apply_format_spec import apply_format_spec
except ImportError as e:
    print(json.dumps({
        "ok": False,
        "errors": [{"code": "IMPORT_ERROR", "message": f"无法导入 apply_format_spec: {e}"}]
    }, ensure_ascii=False))
    sys.exit(1)


SCHEMA_PATH = SKILL_DIR.parent / "references" / "format_spec.schema.json"


def load_schema():
    if not SCHEMA_PATH.exists():
        return None
    with open(SCHEMA_PATH, "r", encoding="utf-8") as f:
        return json.load(f)


def load_input(args):
    """从 stdin 或 --file 读取 JSON 输入"""
    if args.file:
        with open(args.file, "r", encoding="utf-8") as f:
            return json.load(f)
    if not sys.stdin.isatty():
        raw = sys.stdin.read()
        if not raw.strip():
            raise ValueError("stdin 为空，且未提供 --file")
        return json.loads(raw)
    raise ValueError("请通过 stdin 传入 JSON，或使用 --file 参数")


def validate_format_spec(spec, schema):
    """校验 format_spec，缺失必填字段时返回清晰错误"""
    if schema is None:
        return [{
            "code": "SCHEMA_NOT_FOUND",
            "message": f"未找到 schema 文件 {SCHEMA_PATH}，跳过校验（不推荐）"
        }]
    try:
        validate(instance=spec, schema=schema)
        return []
    except ValidationError as e:
        # 提取具体缺失字段路径
        path = "/".join(str(p) for p in e.absolute_path) if e.absolute_path else "(root)"
        return [{
            "code": "FORMAT_SPEC_INVALID",
            "field": path,
            "message": e.message,
            "schema_path": "/".join(str(p) for p in e.absolute_path)
        }]


def replace_placeholders(doc, fields):
    """
    替换模板中的占位符。支持两种语法:
        1. {{ field_name }}     (Jinja 风格)
        2. {field_name}         (单花括号风格)
    替换范围：所有段落 + 表格单元格 + 页眉页脚。
    返回 (替换总数, 未匹配占位符列表)
    """
    total = 0
    unmatched = set()

    # 占位符正则：先匹配 {{ ... }} 再匹配 { ... }
    pattern_double = re.compile(r"\{\{\s*([A-Za-z0-9_一-龥.]+)\s*\}\}")
    pattern_single = re.compile(r"(?<!\{)\{([A-Za-z0-9_一-龥.]+)\}(?!\})")

    def replace_in_text(text):
        nonlocal total
        original = text

        def _double(m):
            nonlocal total
            key = m.group(1)
            if key in fields:
                total += 1
                return str(fields[key])
            unmatched.add(key)
            return m.group(0)

        def _single(m):
            nonlocal total
            key = m.group(1)
            if key in fields:
                total += 1
                return str(fields[key])
            unmatched.add(key)
            return m.group(0)

        text = pattern_double.sub(_double, text)
        text = pattern_single.sub(_single, text)
        return text

    def process_paragraph(paragraph):
        # 处理单段：把所有 run 文本合并，匹配后再写回第一个 run
        if not paragraph.runs:
            return
        full_text = "".join(r.text for r in paragraph.runs)
        if "{{" not in full_text and "{" not in full_text:
            return
        new_text = replace_in_text(full_text)
        if new_text != full_text:
            # 把新文本写回第一个 run，清空其它 run
            paragraph.runs[0].text = new_text
            for r in paragraph.runs[1:]:
                r.text = ""

    # 主文档段落
    for paragraph in doc.paragraphs:
        process_paragraph(paragraph)
    # 表格
    for table in doc.tables:
        for row in table.rows:
            for cell in row.cells:
                for paragraph in cell.paragraphs:
                    process_paragraph(paragraph)
    # 页眉页脚
    for section in doc.sections:
        for header in [section.header, section.first_page_header, section.even_page_header]:
            if header is None:
                continue
            for paragraph in header.paragraphs:
                process_paragraph(paragraph)
            for table in header.tables:
                for row in table.rows:
                    for cell in row.cells:
                        for paragraph in cell.paragraphs:
                            process_paragraph(paragraph)
        for footer in [section.footer, section.first_page_footer, section.even_page_footer]:
            if footer is None:
                continue
            for paragraph in footer.paragraphs:
                process_paragraph(paragraph)
            for table in footer.tables:
                for row in table.rows:
                    for cell in row.cells:
                        for paragraph in cell.paragraphs:
                            process_paragraph(paragraph)

    return total, sorted(unmatched)


def estimate_stats(doc):
    """粗略统计：段落数、字数、估算页数"""
    paragraphs = list(doc.paragraphs)
    total_chars = 0
    for p in paragraphs:
        total_chars += len(p.text)
    # 经验值：A4 小四宋体 1.5 倍行距，每页约 700-900 字
    pages = max(1, round(total_chars / 800))
    return {
        "paragraphs": len(paragraphs),
        "tables": len(doc.tables),
        "characters": total_chars,
        "pages_estimated": pages
    }


def main():
    parser = argparse.ArgumentParser(description="按 format_spec 填充 docx 模板")
    parser.add_argument("--file", help="输入 JSON 文件路径")
    parser.add_argument("--strict", action="store_true",
                        help="严格模式：format_spec 校验失败时立即退出")
    args = parser.parse_args()

    try:
        payload = load_input(args)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "INPUT_INVALID", "message": str(e)}]
        }, ensure_ascii=False))
        sys.exit(1)

    template_path = payload.get("template_path")
    format_spec = payload.get("format_spec", {})
    fields = payload.get("fields", {})
    output_path = payload.get("output_path")

    warnings = []
    errors = []

    # 必填字段检查
    if not template_path:
        errors.append({"code": "MISSING_FIELD", "field": "template_path", "message": "必填"})
    if not output_path:
        errors.append({"code": "MISSING_FIELD", "field": "output_path", "message": "必填"})
    if not format_spec:
        errors.append({"code": "MISSING_FIELD", "field": "format_spec", "message": "必填"})

    if errors:
        print(json.dumps({"ok": False, "errors": errors}, ensure_ascii=False))
        sys.exit(1)

    # 模板存在性
    if not os.path.exists(template_path):
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "TEMPLATE_NOT_FOUND", "field": "template_path",
                        "message": f"模板文件不存在: {template_path}"}]
        }, ensure_ascii=False))
        sys.exit(1)

    # format_spec 校验
    schema = load_schema()
    schema_errors = validate_format_spec(format_spec, schema)
    if schema_errors:
        if args.strict or any(e.get("code") == "FORMAT_SPEC_INVALID" for e in schema_errors):
            print(json.dumps({"ok": False, "errors": schema_errors}, ensure_ascii=False))
            sys.exit(1)
        else:
            warnings.extend(schema_errors)

    # 打开模板
    try:
        doc = Document(template_path)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "TEMPLATE_OPEN_FAILED", "message": str(e)}]
        }, ensure_ascii=False))
        sys.exit(1)

    # 替换占位符
    fill_count, unmatched = replace_placeholders(doc, fields)
    if unmatched:
        warnings.append({
            "code": "FIELDS_UNMATCHED",
            "count": len(unmatched),
            "fields": unmatched,
            "message": f"以下 {len(unmatched)} 个占位符未匹配（fields 字典中缺失）"
        })

    # 应用排版规范
    try:
        apply_format_spec(doc, format_spec)
    except Exception as e:
        warnings.append({"code": "FORMAT_APPLY_PARTIAL", "message": f"部分排版设置失败: {e}"})

    # 保存
    try:
        os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
        doc.save(output_path)
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{"code": "OUTPUT_SAVE_FAILED", "message": str(e)}]
        }, ensure_ascii=False))
        sys.exit(1)

    stats = estimate_stats(doc)
    fill_rate = (fill_count / (fill_count + len(unmatched))) if (fill_count + len(unmatched)) > 0 else 1.0

    print(json.dumps({
        "ok": True,
        "output_path": output_path,
        "stats": {
            **stats,
            "fields_filled": fill_count,
            "fields_unmatched": len(unmatched),
            "fill_rate": round(fill_rate, 3)
        },
        "warnings": warnings,
        "errors": []
    }, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print(json.dumps({
            "ok": False,
            "errors": [{
                "code": "UNEXPECTED_ERROR",
                "message": str(e),
                "trace": traceback.format_exc()
            }]
        }, ensure_ascii=False))
        sys.exit(1)