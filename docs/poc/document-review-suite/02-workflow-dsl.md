# 工作流 DSL JSON

每个工作流都可通过 POST `/workflows` 导入。请求体 JSON 形如：

```json
{
  "name": "wf-doc-review",
  "description": "...",
  "dsl": { /* 见下文 */ }
}
```

> **导入提示**：先用 `weknora agent list` 或 GET `/agents/:id` 拿到每个 `builtin-doc-*` 的真实 ID（如果已被 tenant 级覆盖），把 DSL 里的 `agent_id` 占位符替换成实际 ID。

---

## 1. `wf-doc-review` — 文档审查

```json
{
  "name": "wf-doc-review",
  "description": "格式审查 + 文本校对 + 一致性检查，三路并行后汇总报告",
  "dsl": {
    "version": 1,
    "components": {
      "start-1":  { "params": {},                      "downstream": ["llm-dispatcher"] },
      "llm-dispatcher": {
        "params": {
          "agent_id": "builtin-doc-review-dispatcher",
          "prompt": "{start_id@query}\n请分发到三个专业 Agent 并汇总审查报告。",
          "temperature": 0.3
        },
        "upstream": ["start-1"],
        "downstream": ["retrieval-format", "llm-proofread", "llm-consistency"]
      },
      "retrieval-format": {
        "params": {
          "kb_ids": ["KB_COMPANY_TEMPLATE"],
          "top_k": 8,
          "query": "格式规范 字体 段落 页边距"
        },
        "upstream": ["llm-dispatcher"],
        "downstream": ["llm-format"]
      },
      "llm-format": {
        "params": {
          "agent_id": "builtin-doc-review-format",
          "prompt": "请基于检索到的格式规范，对 {start_id@files[0]} 做格式审查。",
          "temperature": 0.2
        },
        "upstream": ["retrieval-format"],
        "downstream": ["agg-1"]
      },
      "llm-proofread": {
        "params": {
          "agent_id": "builtin-doc-review-proofread",
          "prompt": "请对 {start_id@files[0]} 做错别字 / 语法 / 术语校对。",
          "temperature": 0.1
        },
        "upstream": ["llm-dispatcher"],
        "downstream": ["agg-1"]
      },
      "llm-consistency": {
        "params": {
          "agent_id": "builtin-doc-review-consistency",
          "prompt": "请对 {start_id@files[0]} 做金额 / 日期 / 编号的一致性检查。",
          "temperature": 0.1
        },
        "upstream": ["llm-dispatcher"],
        "downstream": ["agg-1"]
      },
      "agg-1": {
        "params": {
          "variables": [
            { "name": "format_issues",    "ref": "{llm-format@content}" },
            { "name": "proofread_issues", "ref": "{llm-proofread@content}" },
            { "name": "consistency_issues", "ref": "{llm-consistency@content}" }
          ]
        },
        "upstream": ["llm-format", "llm-proofread", "llm-consistency"],
        "downstream": ["llm-synth"]
      },
      "llm-synth": {
        "params": {
          "prompt": "请把三路审查结果合并为一份可读报告：\n格式问题：{agg-1@format_issues}\n校对问题：{agg-1@proofread_issues}\n一致性问题：{agg-1@consistency_issues}",
          "system_prompt": "你是文档审查报告撰写人。输出 Markdown 表格 + 优先级排序的修改建议。",
          "temperature": 0.3
        },
        "upstream": ["agg-1"],
        "downstream": ["answer-1"]
      },
      "answer-1": {
        "params": { "template": "## 文档审查报告\n\n{llm-synth@content}\n\n---\n请人工复核后决定采纳。" },
        "upstream": ["llm-synth"]
      }
    }
  }
}
```

---

## 2. `wf-bid-comparison` — 招投标比对

```json
{
  "name": "wf-bid-comparison",
  "description": "招标文件解析 → 评分表结构化 → 逐项比对 → 风险标注",
  "dsl": {
    "version": 1,
    "components": {
      "start-1": { "params": {}, "downstream": ["llm-parser"] },
      "retrieval-bid-tpl": {
        "params": { "kb_ids": ["KB_BID_TEMPLATE"], "query": "招标文件 评分表 结构", "top_k": 8 },
        "upstream": ["llm-parser"],
        "downstream": ["llm-parser"]
      },
      "llm-parser": {
        "params": {
          "agent_id": "builtin-bid-parser",
          "prompt": "请解析招标文件 {start_id@files[0]}，输出结构化 JSON。",
          "temperature": 0.1
        },
        "upstream": ["start-1", "retrieval-bid-tpl"],
        "downstream": ["llm-scorer"]
      },
      "llm-scorer": {
        "params": {
          "agent_id": "builtin-bid-scorer",
          "prompt": "请把 {llm-parser@content} 中的评分表拆解为评分项三元组。",
          "temperature": 0.05
        },
        "upstream": ["llm-parser"],
        "downstream": ["switch-loop"]
      },
      "switch-loop": {
        "params": {
          "value": "{loop-counter@value}",
          "cases": [
            { "value": "0", "to": "llm-comparator-0" },
            { "value": "1", "to": "llm-comparator-1" }
          ],
          "default": "llm-risker"
        },
        "upstream": ["llm-scorer"],
        "downstream": []
      },
      "llm-comparator-0": {
        "params": {
          "agent_id": "builtin-bid-comparator",
          "prompt": "对评分项 id=0：在投标文件 {start_id@files[1]} 中检索应答并判定。",
          "temperature": 0.2
        },
        "upstream": ["switch-loop"],
        "downstream": ["agg-results"]
      },
      "llm-comparator-1": {
        "params": {
          "agent_id": "builtin-bid-comparator",
          "prompt": "对评分项 id=1：在投标文件 {start_id@files[1]} 中检索应答并判定。",
          "temperature": 0.2
        },
        "upstream": ["switch-loop"],
        "downstream": ["agg-results"]
      },
      "agg-results": {
        "params": {
          "variables": [
            { "name": "all_comparisons", "ref": "[{llm-comparator-0@content}, {llm-comparator-1@content}]" }
          ]
        },
        "upstream": ["llm-comparator-0", "llm-comparator-1"],
        "downstream": ["llm-risker"]
      },
      "llm-risker": {
        "params": {
          "agent_id": "builtin-bid-risker",
          "prompt": "请对 {agg-results@all_comparisons} 中的缺失 / 负偏离项标注风险等级。",
          "temperature": 0.3
        },
        "upstream": ["agg-results"],
        "downstream": ["answer-1"]
      },
      "answer-1": {
        "params": { "template": "## 招投标比对报告\n\n{llm-risker@content}\n\n---\n请人工复核。" },
        "upstream": ["llm-risker"]
      }
    }
  }
}
```

> 注：上面的 `llm-comparator-0`、`llm-comparator-1` 只是示意两个评分项。实际部署时按 `llm-scorer` 输出的 `items.length` 生成 N 个 comparator 节点，或者改用后端代码 `for item in items: POST /workflows/:id/runs` 模式驱动。

---

## 3. `wf-doc-generation` — 生成 + 校核循环

```json
{
  "name": "wf-doc-generation",
  "description": "方案生成 → 校核 → 不通过则回到生成（最多 3 轮）",
  "dsl": {
    "version": 1,
    "components": {
      "start-1": { "params": {}, "downstream": ["retrieval-plan"] },
      "retrieval-plan": {
        "params": { "kb_ids": ["KB_BID_HISTORY", "KB_DOC_TEMPLATE"], "top_k": 8 },
        "upstream": ["start-1"],
        "downstream": ["llm-plan"]
      },
      "llm-plan": {
        "params": {
          "agent_id": "builtin-gen-plan",
          "prompt": "项目参数：{start_id@query}。请基于检索到的历史标书生成技术方案初稿，未核实的内容用 {{待填:xxx}} 标记。",
          "temperature": 0.5
        },
        "upstream": ["retrieval-plan"],
        "downstream": ["llm-reviewer"]
      },
      "llm-reviewer": {
        "params": {
          "agent_id": "builtin-gen-reviewer",
          "prompt": "请对生成内容 {llm-plan@content} 做事实 / 合规 / 格式三项检查，输出 JSON { passed, issues }。",
          "temperature": 0.1
        },
        "upstream": ["llm-plan"],
        "downstream": ["switch-loop"]
      },
      "switch-loop": {
        "params": {
          "value": "{llm-reviewer@content}",
          "cases": [
            { "value": "passed=true",  "to": "answer-1" },
            { "value": "loop<3",       "to": "llm-plan" }
          ],
          "default": "answer-fail"
        },
        "upstream": ["llm-reviewer"]
      },
      "answer-1": {
        "params": { "template": "## 生成结果（待人工确认）\n\n{llm-plan@content}\n\n校核报告：{llm-reviewer@content}" },
        "upstream": ["switch-loop"]
      },
      "answer-fail": {
        "params": { "template": "## 生成失败\n\n已尝试 3 轮校核仍未通过：\n{llm-reviewer@content}\n\n请人工介入。" },
        "upstream": ["switch-loop"]
      }
    }
  }
}
```

---

## 4. `wf-kb-ingest` — 知识库入库

```json
{
  "name": "wf-kb-ingest",
  "description": "非结构化文档解析 → 元数据抽取 → 写入知识库 + 异步关联",
  "dsl": {
    "version": 1,
    "components": {
      "start-1": { "params": {}, "downstream": ["llm-ingest"] },
      "llm-ingest": {
        "params": {
          "agent_id": "builtin-kb-ingest",
          "prompt": "请解析 {start_id@files[0]}，提取元数据：项目名称、专业类别、问题类型、阶段、文档类型；并按段落切块。",
          "temperature": 0.1
        },
        "upstream": ["start-1"],
        "downstream": ["dataops-write-meta"]
      },
      "dataops-write-meta": {
        "params": {
          "sql": "INSERT INTO document_metadata (doc_id, project, category, phase) VALUES (?, ?, ?, ?)",
          "variables": [
            { "name": "doc_id",   "ref": "{start_id@files[0].id}" },
            { "name": "project",  "ref": "{llm-ingest@content.project}" },
            { "name": "category", "ref": "{llm-ingest@content.category}" },
            { "name": "phase",    "ref": "{llm-ingest@content.phase}" }
          ]
        },
        "upstream": ["llm-ingest"],
        "downstream": ["answer-1"]
      },
      "answer-1": {
        "params": { "template": "## 入库成功\n\n{llm-ingest@content}\n\n异步关联将由 builtin-kb-associator 后台处理。" },
        "upstream": ["dataops-write-meta"]
      }
    }
  }
}
```

---

## 5. `wf-kb-advisory` — 主动提醒

```json
{
  "name": "wf-kb-advisory",
  "description": "新项目启动 → 检索关联历史问题 → 注意事项清单",
  "dsl": {
    "version": 1,
    "components": {
      "start-1": { "params": {}, "downstream": ["retrieval-advisory"] },
      "retrieval-advisory": {
        "params": { "kb_ids": ["KB_COMPANY_HISTORY"], "top_k": 15 },
        "upstream": ["start-1"],
        "downstream": ["llm-advisor"]
      },
      "llm-advisor": {
        "params": {
          "agent_id": "builtin-kb-advisor",
          "prompt": "新项目：{start_id@query}。请基于检索结果生成『历史问题 & 注意事项清单』。",
          "temperature": 0.3
        },
        "upstream": ["retrieval-advisory"],
        "downstream": ["answer-1"]
      },
      "answer-1": {
        "params": { "template": "## 新项目启动注意事项\n\n{llm-advisor@content}\n\n---\n请项目负责人逐项确认。" },
        "upstream": ["llm-advisor"]
      }
    }
  }
}
```

---

## 6. 导入命令模板

```bash
# 1. 创建工作流
curl -X POST $LUOSA_HOST/api/v1/workflows \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d @wf-doc-review.json

# 2. 触发一次运行
curl -X POST $LUOSA_HOST/api/v1/workflows/$WF_ID/runs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"input": {"query": "...", "files": ["file-id-1"]}}'

# 3. 监听 SSE 流（可选）
curl -N $LUOSA_HOST/api/v1/workflows/$WF_ID/runs/$RUN_ID/events \
  -H "Authorization: Bearer $TOKEN"
```
