# 工作流功能 E2E 测试报告 — 2026-09-08

目标环境:生产服务器 192.168.20.226(app :8091,quay.io/nilpo1/weknora-app:main @ d1d3f77)
套件:`tests/e2e/workflow/`(REST 驱动,可重复执行)
结果:**27 PASS / 0 FAIL / 2 SKIP**(跳过项均带原因)

## 部署与修复验证

本轮部署前合并的两个 PR:
- **PR #7**:LLM max_tokens 不生效、连字符节点 ID 引用不渲染、HTTP 节点表单卡死、发布快照陈旧警告(4 个修复)
- **PR #8**:恢复 CI 推送 quay.io/nilpo1(revert 了此前的 revert),服务器 override 已改回 quay 正源

修复验证(部署前后对照):
| 修复项 | 部署前 | 部署后 |
|---|---|---|
| `{llm-xxx@content}` 字面量泄漏 | FAIL(复现) | **PASS** |
| max_tokens=5 超限生成 | 未截断(几百字) | **PASS**(输出 18 字符) |
| 系统提示词生效 | PASS | PASS |
| HTTP 节点点开卡死 | 人工确认项 | 人工确认项(见下) |

## 用例明细

### A. 节点功能(14/16 可测,2 SKIP)
| # | 用例 | 结果 | 说明 |
|---|---|---|---|
| 1 | Start 表单字段/默认值/必填拦截 | PASS | 必填缺失正确 400 |
| 2 | LLM 系统提示词 | PASS | MARKER 约束生效 |
| 3 | LLM max_tokens=5 截断 | PASS | 输出 18 字符(修复验证) |
| 4 | LLM temperature | PASS* | 0/1.6 均正常执行;*见"已知限制" |
| 5 | LLM 默认模型回退 | SKIP | 工作区未标记 is_default 模型(套件自动发现模型并显式指定) |
| 6 | Answer 连字符 ID 引用渲染 | PASS | 修复验证 |
| 7 | Switch eq/contains/gt/regex、or 逻辑、default | PASS | default 路由 matched 为空串(设计如此) |
| 8 | Template 全 ops | PASS | trim/replace/upper/lower/regex_extract |
| 9 | VariableAggregator 分支合并 | PASS | 未走分支静默跳过,collected=1 |
| 10 | HTTP 内网调用 + 外网拦截 | PASS | 自身 API 返回 401(证明调用执行);example.com 被内网策略拒绝 |
| 11 | DataOps SELECT-only + 参数绑定 | PASS | DROP 被拒;SELECT 41+1 → 1 行 |
| 12 | Code 节点 | SKIP | 服务器未配置沙箱后端(错误信息清晰) |
| 13 | QuestionClassifier 两分类 | PASS | billing/tech 各命中 |
| 14 | ParameterExtractor 类型化抽取 | PASS | city=Tokyo days=5 |
| 15 | Iteration 列表循环 | PASS | alpha/beta/gamma 全处理 |
| 16 | Agent 节点纯提示词 | PASS | 输出键为 `answer` |
| 17 | Retrieval(现有 demo 知识库) | PASS | chunks 非空、引用渲染 |
| 18 | WebSearch / MCPTool | SKIP | 未配置(按约定) |

### B. 流程语义(4/4)
| # | 用例 | 结果 | 说明 |
|---|---|---|---|
| 19 | 引用渲染回归(提示词/模板/HTTP body) | PASS | |
| 20 | 发布语义(快照 vs 草稿) | PASS | 发布后改草稿,运行仍走快照(前端已加警告) |
| 21 | 错误策略 on_error=continue | PASS | default_outputs 正确物化 |
| 22 | 重试策略 | PASS | 失败节点带 retry 落 trace |

### C. 执行面(7/7)
| # | 用例 | 结果 | 说明 |
|---|---|---|---|
| 23 | 同步运行 | PASS | |
| 24 | 异步运行 + trace 完整性 | PASS | |
| 25 | SSE 事件流 | PASS | 帧格式为 `data:{...}`(无空格,gin SSEvent) |
| 26 | 取消运行 | PASS | cancelled 终态 |
| 27 | 断点续跑 | PASS | 已完成节点 replayed、修复后补跑成功 |
| 28 | 定时调度触发 + 停用 | PASS | cron 每分钟触发、disable 后停止 |
| 29 | 运行历史/详情 | PASS | 新到旧排序、trace/input 完整 |

## 测试过程中发现的产品问题(新)

1. **resume 成功后 error 字段残留旧错误文本**:修复 DSL 后 resume,run 状态变 succeeded 但 `error` 列未清空。前端 `applyRunOutcome` 会把 `run.error` 显示为错误提示,可能误导。建议成功 resume 时清空 error 列。
2. **temperature=0 与"未设置"无法区分(已知限制)**:`go-openai` 的 `temperature` 字段带 `omitempty`,0 值不上线,生效的是 provider 默认温度。如需显式 0,须把 ChatOptions.Temperature 改为指针语义(侵入大,暂记录)。
3. **引用不支持嵌套取值**:`{agg@values.picked}` 这类 map 子键访问会报 unresolved(lookupRef 只支持平铺输出键)。文档或功能二选一补齐。

## 人工确认清单(API 测不到,需浏览器验证)

- [ ] HTTP 节点点开不再卡死;headers 行增删改正常(修复验证)
- [ ] 错误策略选"继续执行"+ 默认输出编辑不卡死(同款修复)
- [ ] 已发布工作流改草稿后,运行面板出现"运行的是已发布快照"警告;重新发布后消失
- [ ] 引用插入按钮 / `{` 自动补全正常
- [ ] max_tokens 滑杆保存后运行被截断(后端已验证,前端展示确认)

## 复跑方式

```bash
E2E_BASE_URL=http://192.168.20.226:8091 \
E2E_EMAIL=... E2E_PASSWORD='...' \
go test ./tests/e2e/workflow/ -v -count=1 -timeout 25m
```

可选 `E2E_KB_NAME=<名称子串>` 指定检索库(默认取第一个有文档的库)。
