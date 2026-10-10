# Feature: 推荐问题点击填入输入框（不直接发送）

> **状态**：方案已设计、未实施。沙箱里因 HMR + 项目 TS 配置导致无法稳定测试，留待本地实现。

## 用户故事

**当前行为**：在新对话界面，点击推荐问题卡片 → 文字直接被发送，**不可修改**。

**期望行为**：点击推荐问题 → 文字**填入输入框** → 用户可编辑 → 按 Enter 才发送。

## 实现方案

### 文件1：`frontend/src/views/chat/index.vue`（约 378 行）

**找到函数**：

```javascript
const handleSuggestedQuestionClick = (question) => {
    if (inputFieldRef.value?.triggerSend) {
        inputFieldRef.value.triggerSend(question);
    } else {
        sendMsg(question);
    }
};
```

**替换为**：

```javascript
const handleSuggestedQuestionClick = (question) => {
    const field = inputFieldRef.value;
    if (field && typeof field.query !== 'undefined') {
        // 通过 Vue 响应式 ref 直接填入——TDesign t-textarea v-model 会自动更新
        field.query = question;
        // 自动聚焦内部原生 textarea
        const el = document.querySelector('.answers-input textarea');
        if (el && el.focus) el.focus();
    } else if (field && field.triggerSend) {
        // 兼容回退：旧版本 InputField 没暴露 query
        field.triggerSend(question);
    } else {
        sendMsg(question);
    }
};
```

### 文件2：`frontend/src/components/Input-field.vue`（约 2513 行）

**找到**：

```typescript
defineExpose({
  triggerSend(text: string) {
    if (!text.trim()) return;
    query.value = text;
    nextTick(() => createSession(text));
  }
});
```

**替换为**（只加一行 `query,`）：

```typescript
defineExpose({
  query,
  triggerSend(text: string) {
    if (!text.trim()) return;
    query.value = text;
    nextTick(() => createSession(text));
  }
});
```

> `query` 是文件顶部 `<script setup>` 里的 `let query = ref("")`，Vue 的 `defineExpose({ query })` 会把它以 proxy ref 形式暴露出去，**外部访问时自动 unwrap**，所以 `field.query = question` 是正确写法。

## 实现要点

### 为什么用 ref 直接赋值（不用 DOM 操作）

| 方案 | 问题 |
|---|---|
| ❌ DOM `textarea.value = ...` + dispatchEvent | TDesign `t-textarea` 不一定响应原生 input 事件（实测不生效） |
| ✅ 直接改 Vue ref `field.query = ...` | 走 Vue 响应式系统，`<t-textarea v-model="query">` 必然更新 |

### 为什么不用 TS 断言

项目里的某些写法与 Vite/Babel parser 严格模式冲突（曾遇到 `as HTMLTextAreaElement \| null` 触发编译失败）。

**方案里只用最朴素的 JS**：`typeof field.query !== 'undefined'`（不写泛型、不写 `as`）。

### 兼容性考虑

| 场景 | 行为 |
|---|---|
| 新版 InputField 暴露了 `query` | 走 `field.query = question` 路径 |
| 旧版 InputField 没暴露 `query` | 走 `field.triggerSend(question)` 兼容路径（直接发送） |
| 完全没暴露 ref | 走 `sendMsg(question)` fallback |

### 实现后的回归测试

1. ✅ 点击推荐问题 → 文字进输入框、**不发送**
2. ✅ 在输入框修改文字 → 按 Enter → 发送修改后的文字
3. ✅ 在输入框按 Shift+Enter → 换行（不发送）
4. ✅ 点击推荐问题后立刻按 Enter → 发送原文字
5. ✅ 推荐问题卡片刷新按钮仍正常
6. ✅ 对话已存在的会话里点击 follow-up 建议仍正常（这是 FollowUpSuggestions 组件，不受影响）

## 沙箱环境的失败教训

我尝试了 3 个版本（每个 ~10 行）：

1. **暴露 `setQuery` 方法**：Vite 报 `Missing semicolon`
2. **DOM `dispatchEvent('input')`**：TDesign 不响应，没生效
3. **暴露 `query` ref + 用 `field.query = question`**：HMR 没生效，`field.query` 还是 undefined → fallback 到 `triggerSend` → 直接发送

**根因**：
- 项目 `import { useStream } from '../../api/chat/streame'`（无扩展名）→ Vite/Babel parser 在某些场景下失败
- 项目 TS 配置严格，对某些写法报错
- 沙箱里 HMR 状态不可预测

**本地环境应该没问题**：干净 checkout + 完整 `pnpm install` + `pnpm dev` 全量启动。

## 工作量评估

- **改动量**：~12 行代码（两个文件）
- **测试时间**：10 5 分钟
- **风险等级**：低（已设计兼容性 fallback）
- **建议**：作为单独的 PR 处理（避免污染其他功能）

## 相关代码位置（参考）

```
frontend/src/views/chat/index.vue
├── 模板部分（28-68 行）        推荐问题卡片 UI
├── 状态定义（322-328 行）      suggestedQuestions / suggestedQuestionsLoading
├── 拉取逻辑（330-376 行）      fetchSuggestedQuestions + cancel
├── 点击处理（378-384 行）      handleSuggestedQuestionClick ← 改动点
└── Follow-up 建议（392+ 行）   loadFollowUpSuggestions（独立的，不受影响）

frontend/src/components/Input-field.vue
├── <script setup> 顶部         let query = ref("")
└── defineExpose（2513 行）      triggerSend / query ← 改动点

frontend/src/api/agent/index.ts:395
└── getSuggestedQuestions(agentId, params)  后端 API 调用
```

后端路由（无需改动）：
- `GET /api/v1/agents/:id/suggested-questions`（`internal/router/routes_agent.go:49`）