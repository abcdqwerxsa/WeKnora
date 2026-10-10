# Office Template Fill - 使用示例

## 示例 1: 生成商务标

招标方要求：
- A4 纵向，上下边距 2.5cm，左右 3.0cm
- 正文宋体小四，1.5 倍行距，首行缩进 2 字符
- 一级标题黑体三号，二级标题黑体小三
- 页脚居中显示"第 X 页 共 Y 页"
- 页眉"XX 高速公路勘察设计 - 商务标"
- 3 级目录

```bash
cat <<'EOF' | python scripts/fill_template.py
{
  "template_path": "/templates/company-bid-commercial.docx",
  "format_spec": {
    "page": {
      "size": "A4",
      "orientation": "portrait",
      "margins": {"top": 2.5, "bottom": 2.5, "left": 3.0, "right": 3.0, "unit": "cm"}
    },
    "fonts": {
      "body":      {"family": "宋体", "size_pt": 12},
      "heading_1": {"family": "黑体", "size_pt": 16, "bold": true},
      "heading_2": {"family": "黑体", "size_pt": 14, "bold": true},
      "heading_3": {"family": "黑体", "size_pt": 13, "bold": true}
    },
    "paragraph": {
      "line_spacing": 1.5,
      "first_line_indent_chars": 2,
      "alignment": "justify"
    },
    "header_footer": {
      "header": {"text": "XX 高速公路勘察设计 - 商务标", "alignment": "center"},
      "footer": {"text": "第 X 页 共 Y 页", "alignment": "center"},
      "page_number_position": "footer_center"
    },
    "toc": {"levels": 3, "include_page_number": true}
  },
  "fields": {
    "公司名称": "XX 工程有限公司",
    "项目名称": "XX 高速公路勘察设计",
    "投标日期": "2026-09-15",
    "法定代表人": "张三",
    "注册资本": "5,000 万元",
    "成立日期": "2008-03-12",
    "营业执照号": "913100005XXX",
    "项目经理": "李四",
    "项目经理资质": "注册岩土工程师 / 高级工程师"
  },
  "output_path": "/output/商务标.docx"
}
EOF
```

预期输出（stdout JSON）：
```json
{
  "ok": true,
  "output_path": "/output/商务标.docx",
  "stats": {
    "paragraphs": 142,
    "tables": 5,
    "characters": 28500,
    "pages_estimated": 36,
    "fields_filled": 9,
    "fields_unmatched": 0,
    "fill_rate": 1.0
  },
  "warnings": [],
  "errors": []
}
```

## 示例 2: 审查合同并写回修订

合同草稿存在以下问题：

```bash
cat <<'EOF' | python scripts/insert_track_changes.py
{
  "docx_path": "/input/合同草稿.docx",
  "mode": "both",
  "review_findings": [
    {
      "type": "risk",
      "severity": "high",
      "anchor": {
        "section": "第5条",
        "text_match": "违约金为合同总额的5%"
      },
      "suggestion": "建议改为合同总额的 3%（公司模板 §6.2）",
      "rationale": "依据：民法典第 585 条 — 约定的违约金过分高于造成的损失的，当事人可以请求法院适当减少。5% 比例过高，建议调至 3%。"
    },
    {
      "type": "typo",
      "severity": "medium",
      "anchor": {
        "section": "第3条",
        "text_match": "付款方式为工程验收合格后"
      },
      "suggestion": "建议补充'30 日内'明确付款时限",
      "rationale": "公司模板 §5.1 要求所有付款节点必须有时限"
    },
    {
      "type": "compliance",
      "severity": "high",
      "anchor": {
        "section": "第7条",
        "text_match": "发生争议由本公司所在地法院管辖"
      },
      "suggestion": "建议改为'项目所在地法院管辖'",
      "rationale": "公司模板 §8.3 要求合同争议优先采用项目所在地管辖"
    }
  ],
  "output_path": "/output/合同_已审查.docx"
}
EOF
```

预期输出：
```json
{
  "ok": true,
  "output_path": "/output/合同_已审查.docx",
  "stats": {
    "findings_total": 3,
    "findings_applied": 3,
    "findings_unmatched": 0
  },
  "warnings": [],
  "errors": [],
  "meta": {
    "reviewer": "WeKnora-doc-review-proofread",
    "timestamp": "2026-09-15T10:30:00Z",
    "mode": "both"
  }
}
```

## 示例 3: 多分册拼装

商务标 / 技术标 / 经济标 三册分别生成后拼装：

```bash
cat <<'EOF' | python scripts/compose_volume.py
{
  "volumes": [
    {
      "name": "商务标",
      "order": 1,
      "docx_path": "/output/商务标.docx",
      "anonymous": false
    },
    {
      "name": "技术标",
      "order": 2,
      "docx_path": "/output/技术标.docx",
      "anonymous": true
    },
    {
      "name": "经济标",
      "order": 3,
      "docx_path": "/output/经济标.docx",
      "anonymous": false
    }
  ],
  "binding_order": ["商务标", "技术标", "经济标"],
  "tab_divider": true,
  "format_spec": {
    "page": {"size": "A4", "orientation": "portrait",
             "margins": {"top": 2.5, "bottom": 2.5, "left": 3.0, "right": 3.0, "unit": "cm"}}
  },
  "output_path": "/output/投标文件_完整.docx"
}
EOF
```

预期输出：
```json
{
  "ok": true,
  "volumes_merged": 3,
  "binding_order": ["商务标", "技术标", "经济标"],
  "output_path": "/output/投标文件_完整.docx",
  "warnings": [],
  "errors": []
}
```

## 示例 4: 自动生成目录

```bash
cat <<'EOF' | python scripts/render_toc.py
{
  "docx_path": "/output/技术方案.docx",
  "levels": 3,
  "include_page_number": true,
  "title": "目  录",
  "insert_position": "before_first_heading",
  "output_path": "/output/技术方案_含目录.docx"
}
EOF
```

预期输出：
```json
{
  "ok": true,
  "output_path": "/output/技术方案_含目录.docx",
  "stats": {
    "headings_found": 23,
    "levels_included": 3,
    "headings_sample": [
      ["1", "第一章 项目概述"],
      ["1", "第二章 技术方案"],
      ["2", "2.1 设计原则"],
      ["2", "2.2 技术路线"]
    ]
  }
}
```

## 示例 5: 端到端 - 一个完整投标文件生成流程

在 WeKnora 工作流 `wf-bid-generation` 里：

```
节点 1: Start（输入：项目参数 + 招标文件 docx）
  ↓
节点 2: LLM (bid-format-extractor)
  提示词: "从招标文件抽取 format_spec.json..."
  输出: format_spec 字符串
  ↓
节点 3: Code（validate_format_spec）
  用 JSON Schema 校验，缺字段则返回错误让人补
  ↓
节点 4: Retrieval (KB_DOC_TEMPLATE)
  拉公司 docx 模板文件路径
  ↓
节点 5: LLM (gen-section-content, 3 个分册各调用一次)
  每个分册生成章节内容（带 {{占位符}}）
  ↓
节点 6: Skill (office-template-fill, scripts/fill_template.py)
  循环 3 次，分别生成商务标/技术标/经济标 docx
  ↓
节点 7: Skill (office-template-fill, scripts/compose_volume.py)
  拼装多分册 → 完整投标文件
  ↓
节点 8: Skill (office-template-fill, scripts/render_toc.py)
  自动生成主目录
  ↓
节点 9: Approval HITL（项目负责人签字）
  ↓
节点 10: Answer（输出最终 docx 路径）
```

## 故障排查

### 问题 1: "模板文件不存在"

检查沙箱内模板路径是否正确。沙箱内的 `/templates/` 实际指向 Docker 镜像里的目录，**不是宿主机的 `/templates/`**。建议使用绝对路径或在镜像里预设模板。

### 问题 2: 占位符未匹配

`fill_rate < 1.0` 或 warnings 包含 `FIELDS_UNMATCHED`。

常见原因：
- `fields` 字典 key 与模板里 `{{key}}` 不完全相同（注意空格、大小写）
- 模板里用了三层花括号 `{{{key}}}`（本 skill 只支持两层）

调试方法：
```bash
# 先用 inspect_template.py（待实现）看模板里的占位符列表
python scripts/fill_template.py --debug-placeholders --template_path /templates/xxx.docx
```

### 问题 3: 中文显示为方框

字体未安装。常见于中文字体如"宋体"、"黑体"在 Linux 沙箱里没装。

解决方案：
- 在沙箱镜像里 `apt install fonts-noto-cjk fonts-wqy-microhei`
- 或在 format_spec 里改用"Noto Sans CJK SC"、"WenQuanYi Micro Hei"等 Linux 自带字体

### 问题 4: 目录页码不对

静态目录的页码是占位符（"........."），需用户在 Word 里按 F9 更新 TOC 域。

### 问题 5: 修订痕迹看不到

`insert_track_changes.py` 使用的是"插入修订建议段落"的方式（不是真正的 track changes XML），Word/Office 365 默认会显示修订段落，但 LibreOffice 可能需要勾选"显示修订"选项。

如需真正的 track changes XML 格式（原作者姓名 + 时间戳），需要升级到 LibreOffice headless 或 Aspose.Words（商业授权）。