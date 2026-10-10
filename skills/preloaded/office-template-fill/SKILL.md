---
name: office-template-fill
description: 根据结构化 format_spec（从招标文件抽取）填充公司 docx 模板，生成符合招标方格式要求的投标文件或合同文档。当用户需要按模板生成 .docx、应用字体/页边距/页眉页脚/目录/装订顺序等排版规范、生成多分册（商务标/技术标/经济标）投标文件、把审查意见写回 docx 修订痕迹或批注、或将结构化字段映射到 Word 模板占位符时使用此技能。
---

# Office Template Filler

按招标文件规定的格式（字体、字号、行距、页边距、页眉页脚、目录、装订顺序、多分册、签字盖章），
把 LLM 生成的结构化内容填充到公司 docx 模板，输出符合招标方要求的 .docx。

## 核心能力

1. **模板填充**：把 `{占位符}` 替换为真实字段（公司名、项目名称、资质编号等）
2. **排版规范应用**：按 `format_spec` 一次性应用字体、段落、行距、页边距、页眉页脚、页码
3. **多分册拼装**：按 `volumes[]` 顺序合并商务标 / 技术标 / 经济标，生成装订顺序
4. **目录自动生成**：根据标题样式自动生成 1-3 级目录 + 页码
5. **修订痕迹 / 批注写入**：把审查意见以 Word 原生修订（track changes）或批注（comments）形式写回原文件
6. **JSON Schema 校验**：校验 `format_spec` 必填字段，避免下游出错

## 与项目其它能力的边界

- **本 Skill 不负责抽取 `format_spec`** —— 抽取由 `builtin-bid-format-extractor` Agent（LLM 节点）完成，
  它从招标文件里读出 `format_spec.json` 后传给本 Skill。
- **本 Skill 不负责生成投标内容** —— 内容由 `builtin-gen-plan` Agent 生成，
  本 Skill 只负责把内容**按模板和格式**渲染为 docx。
- **本 Skill 不负责审查分析** —— 审查由 `builtin-doc-review-*` Agent 完成，
  审查意见以结构化 JSON 传给本 Skill 的 `insert_track_changes.py` 写回。

## 可用脚本

> **沙箱约定**：所有脚本在 Docker / E2B / Cube 沙箱中执行，输入数据通过 stdin 或 `input` 参数传入 JSON 字符串，**不要使用 `--file` 传内存数据**。

### 1. fill_template.py — 主入口（模板填充 + 排版应用）

把 `format_spec` + `fields` + 模板路径 → 输出 docx。

**输入 JSON**：
```json
{
  "template_path": "/skills/.../templates/company-bid.docx",
  "format_spec": { ... },  // 见 references/format_spec.schema.json
  "fields": {
    "公司名称": "XX 工程有限公司",
    "项目名称": "XX 高速公路勘察设计",
    "投标日期": "2026-09-15",
    "法定代表人": "张三",
    "...": "..."
  },
  "output_path": "/sandbox/output/投标文件_商务标.docx"
}
```

**用法**：
```json
// ✅ 正确：通过 input 传入数据
{
  "skill_name": "office-template-fill",
  "script_path": "scripts/fill_template.py",
  "input": "{\"template_path\": \"...\", \"format_spec\": {...}, \"fields\": {...}, \"output_path\": \"...\"}"
}
```

**执行流程**（脚本内部）：
1. 校验 `format_spec` 必填字段（用 references/format_spec.schema.json）
2. 调用 `apply_format_spec.py` 应用排版规范
3. 按 `fields` 替换模板里的 `{占位符}`（支持 Jinja 风格 `{{ field }}`）
4. 写入 `output_path` 并返回结构化结果 JSON（含生成路径、章节数、字数）

### 2. apply_format_spec.py — 排版规范应用

把 `format_spec` 渲染为 python-docx 调用序列，应用到现有 docx。

**输入 JSON**：
```json
{
  "docx_path": "/sandbox/output/xxx.docx",
  "format_spec": { ... }
}
```

**应用的规范项**：
- `page.size` / `page.orientation` / `page.margins` → 页面设置
- `fonts.body / heading_* / caption` → 默认字体（Normal 样式）
- `paragraph.line_spacing` / `first_line_indent_chars` → 段落格式
- `header_footer.header / footer / page_number_position` → 页眉页脚
- `toc.levels` / `toc.include_page_number` → 目录生成（调用 render_toc.py）
- `signature.seal_per_page` → 是否每页预留盖章位

### 3. compose_volume.py — 多分册拼装

按 `volumes[]` 顺序拼接多个分册，生成装订顺序。

**输入 JSON**：
```json
{
  "volumes": [
    {"name": "商务标", "docx_path": "/sandbox/.../volume-1.docx", "anonymous": false},
    {"name": "技术标", "docx_path": "/sandbox/.../volume-2.docx", "anonymous": true},
    {"name": "经济标", "docx_path": "/sandbox/.../volume-3.docx", "anonymous": false}
  ],
  "binding_order": ["商务标", "技术标", "经济标"],
  "tab_divider": true,
  "format_spec": { ... },
  "output_path": "/sandbox/output/投标文件_完整.docx"
}
```

**拼装细节**：
- 分册之间插入分节符 + 封面页（包含分册名）
- `anonymous=true` 的分册做"匿名化处理"：移除公司名、Logo、项目业绩表等可识别信息
- `tab_divider=true` 时插入分册隔板页（带彩色页眉）

### 5. insert_track_changes.py — 修订痕迹 / 批注写入

把审查意见以 Word 原生修订（track changes）或批注（comments）形式写回原 docx。

**输入 JSON**：
```json
{
  "docx_path": "/sandbox/.../合同草稿.docx",
  "review_findings": [
    {
      "type": "typo" | "format" | "compliance" | "consistency" | "risk",
      "severity": "high" | "medium" | "low",
      "anchor": {
        "section": "第3.2条",
        "page": 5,
        "text_match": "付款方式为..."   // 锚定原文片段
      },
      "suggestion": "建议改为：...",
      "rationale": "依据：民法典第 XXX 条 / 公司模板 §Y.Z"
    }
  ],
  "mode": "track_changes" | "comments" | "both",
  "output_path": "/sandbox/output/合同_已审查.docx"
}
```

**锚定策略**：先按 `section` 在标题里匹配，再按 `text_match` 在段落里做最长子串匹配，
避免误标。所有修订都带审查人标记 `Reviewer: WeKnora-doc-review-proofread`。

### 4. render_toc.py — 自动目录生成

扫描 docx 标题样式，生成 1-N 级目录 + 页码。

**输入 JSON**：
```json
{
  "docx_path": "/sandbox/.../xxx.docx",
  "levels": 3,
  "include_page_number": true,
  "title": "目  录",
  "insert_position": "before_first_heading"
}
```

**实现**：使用 `python-docx` 解析标题，再插入目录域（TOC field code），
Word 打开时会提示"是否更新目录"，确保页码正确。

## 处理流程

### 流程 A：生成投标文件（最常见）

```
1. Agent（LLM）从招标文件抽取 format_spec.json
2. Agent（LLM）生成各章节内容（带 {{field}} 占位符）
3. fill_template.py 用 format_spec + 章节内容 + 公司模板 → 单分册 docx
4. compose_volume.py 拼装多分册 → 完整投标文件
5. 人审（project owner 在工作流里点通过 / 驳回）
6. 输出最终 docx
```

### 流程 B：审查合同并写回修订

```
1. Agent（LLM）审查合同原文 → 输出 review_findings 数组
2. insert_track_changes.py 把 findings 写为 track changes / comments
3. 输出 .docx 给律师 / 业务方复核
```

### 流程 C：按公司模板套用格式

```
1. Agent（LLM）从用户描述提取格式要求 → format_spec
2. apply_format_spec.py 直接应用到现有 docx
3. 输出符合规范的新版本
```

## JSON Schema 校验

每个脚本启动时都会用 `references/format_spec.schema.json` 校验入参的 `format_spec`，
**必填字段缺失时会直接报错并指出哪个字段缺失**（不是悄悄用默认值），防止下游输出残缺。

关键必填字段：
- `page.size`（A4 / Letter / A3 ...）
- `page.orientation`（portrait / landscape）
- `page.margins`（top / bottom / left / right，单位 cm 或 inch）
- `fonts.body.family` / `fonts.body.size_pt`
- `paragraph.line_spacing`
- `structure.volumes`（每个分册的 name 和 order）

完整字段见 `references/format_spec.schema.json`。

## 使用示例

### 示例 1：生成商务标

```bash
# 通过 stdin 传入（推荐）
cat <<'EOF' | python scripts/fill_template.py
{
  "template_path": "/templates/company-bid-commercial.docx",
  "format_spec": {
    "page": {"size": "A4", "orientation": "portrait", "margins": {"top": 2.5, "bottom": 2.5, "left": 3.0, "right": 3.0, "unit": "cm"}},
    "fonts": {"body": {"family": "宋体", "size_pt": 12}},
    "paragraph": {"line_spacing": 1.5, "first_line_indent_chars": 2}
  },
  "fields": {
    "公司名称": "XX 工程有限公司",
    "项目名称": "XX 高速公路勘察设计",
    "投标日期": "2026-09-15"
  },
  "output_path": "/output/商务标.docx"
}
EOF
```

### 示例 2：审查并写回修订

```bash
cat <<'EOF' | python scripts/insert_track_changes.py
{
  "docx_path": "/input/合同草稿.docx",
  "mode": "both",
  "review_findings": [
    {
      "type": "risk",
      "severity": "high",
      "anchor": {"section": "第5条", "text_match": "违约金为合同总额的5%"},
      "suggestion": "建议改为合同总额的 3%，参考公司模板 §6.2",
      "rationale": "依据：民法典第 585 条，约定的违约金过分高于造成的损失的，当事人可以请求人民法院予以适当减少"
    }
  ],
  "output_path": "/output/合同_已审查.docx"
}
EOF
```

## 最佳实践

1. **format_spec 必须结构化**：绝对不要把"格式要求"作为自由文本传给本 Skill，必须先用 LLM 抽成符合 schema 的对象
2. **缺失字段要报错而非回退**：缺 `page.size` 时直接报错"格式不完整，需要人补"，比回退到默认值更安全（默认值可能违反招标方要求）
3. **模板路径用绝对路径**：沙箱里相对路径会出错
4. **大文件分批处理**：>200 页的招标文件先生成各分册 docx，再用 compose_volume.py 拼装，不要一次性塞进 python-docx
5. **匿名化分册要做"硬脱敏"**：技术标匿名不仅是文本替换，还要从页眉页脚、表格、水印里去掉公司信息

## 输出格式

每个脚本都返回 JSON 字符串到 stdout：

```json
{
  "ok": true,
  "output_path": "/sandbox/output/...docx",
  "stats": {
    "pages_estimated": 42,
    "sections": 8,
    "words": 18500,
    "fill_rate": 0.96
  },
  "warnings": [],
  "errors": []
}
```

失败时返回：
```json
{
  "ok": false,
  "errors": [
    {"code": "FORMAT_SPEC_MISSING_FIELD", "field": "page.size", "message": "必填字段缺失"}
  ]
}
```

## 注意事项

- **沙箱隔离**：脚本在 Docker / E2B / Cube 沙箱中执行，与宿主隔离
- **执行超时**：默认 120 秒（投标生成），超时由沙箱配置决定
- **字体回退**：沙箱里可能没有招标方要求的字体（如某些专用字体），脚本会用 Pillow 检测并提示安装
- **中文换行**：python-docx 的中文排版需要在每个段落 run 上显式设置 `font.name` 才能正确渲染
- **修订痕迹**：生成的 docx 在 Word 中可能提示"是否接受修订"，这是预期行为，用户接受后即可看到最终版本