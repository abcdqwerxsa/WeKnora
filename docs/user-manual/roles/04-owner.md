# 角色手册 · Owner（所有者）

> **对应后端角色**：`tenant_members.role = 'owner'`（level 40）
> **典型用户**：团队负责人、部门主管、空间创建者、CTO
> **核心心智**：**您是「空间负责人」**。所有 Admin 的能力 + 邀请/移除成员 + 修改空间配置 + 管理 API Key + 删除空间。

---

## 1. 您的能力地图

### ✅ 您能做的事

> **Owner 是所有 Admin 能力的超集**，加上：

| 大类 | 操作 |
|---|---|
| **成员管理** | 邀请 / 移除 / 修改角色 / 主动添加 / 退出（不能退出最后一位 Owner） |
| **邀请流程** | 创建 share-link（`/auth/invite-links`）、邀请已注册用户、撤销邀请 |
| **空间配置** | 修改空间名 / 描述 / `is_joinable` / `storage_quota` / 业务标识 |
| **租户 API Key** | 创建 / 列出 / 撤销（携带 capability + KB allow-list） |
| **删除空间** | 异步清理（`ProcessKBDelete`），不可恢复 |
| **转让所有权** | 转让给其他成员（把自己降级 + 把对方升级） |
| **审计日志** | 同 Admin，可看完整日志 |
| **跨工作区** | 创建 Organization，把 KB 共享给组织 |

### ❌ 您不能做的事

Owner 是工作区内的最高角色，所有工作区级别的限制（如成员数量上限、配额）由平台运维统一控制。

---

## 2. 邀请新成员

### 场景 A：邀请一个已注册用户

1. 进入 `/platform/settings?section=members`
2. 点击「**+ 邀请成员**」
3. 填入：
   - **邮箱 / 用户名**（必须已注册）
   - **角色**：viewer / contributor / admin / **owner**（谨慎）
   - **附言**（可选）
4. 提交 → 状态 `pending` → 收件箱里对方看到邀请
5. 对方接受后，状态 `accepted`，写入 `tenant_members`

> 后端 API：
> - `POST /api/v1/tenants/:id/invitations`

### 场景 B：创建 share-link（让对方自服务注册）

1. 进入 `/platform/settings?section=members` → 「**分享链接**」Tab
2. 点击「**+ 创建链接**」
3. 设置：
   - **角色**：viewer / contributor / admin
   - **有效期**：默认 7 天（`RBAC_INVITATION_TTL`）
   - **使用次数**：单次 / 多次
4. 复制链接：`https://.../join?code=xxx`
5. 发给候选人 → 对方点击 → 注册 → 自动加入（**绕过待审批**）

> 后端 API：
> - `POST /api/v1/tenants/:id/invite-links`

### 场景 C：撤销未接受的邀请

1. 进入「邀请」列表
2. 找到 `pending` 状态的邀请
3. 点击「**撤销**」→ 状态 `revoked`，链接失效

---

## 3. 修改成员角色

1. 进入「成员」列表
2. 找到目标成员 → 点击「⋮」→ 「**修改角色**」
3. 选择新角色 → 确认

**注意**：

- ⚠️ **最后一位 Owner 不可降级**：系统返回 `409 + ErrLastOwner`
- 如果要换 Owner，先把对方提升为 Owner，再把自己降级
- Owner 转让后，对方成为新 Owner，原 Owner 可以是 Admin / Contributor / Viewer

> 后端 API：
> - `PUT /api/v1/tenants/:id/members/:user_id`

---

## 4. 移除成员

1. 进入「成员」列表
2. 找到目标成员 → 点击「⋮」→ 「**移除**」
3. 确认 → 状态变 `removed`（软删除 `deleted_at`）

**限制**：

- 不能移除最后一位 active Owner

> 后端 API：
> - `DELETE /api/v1/tenants/:id/members/:user_id`

---

## 5. 修改空间配置

1. 进入 `/platform/settings?section=tenant`
2. 可编辑：
   - **空间名**（`TenantInfo.vue` 中 `canEditTenant = hasRole('owner')`）
   - **描述**
   - **`is_joinable`**：是否允许其他工作区成员直接 join（Organization 共享场景）
   - **`storage_quota`**：存储配额
   - **`business`**：业务标识（用于多工作区区分）
3. 保存

> 后端 API：通过 `/tenants/:id` PUT

---

## 6. 租户 API Key 管理

> 用于程序化访问（机器人 / 第三方系统 / CI）。

1. 进入 `/platform/settings?section=integrations` → 「**API Keys**」Tab
2. 「**+ 创建 API Key**」
3. 配置：
   - **名称**：易记的描述
   - **能力列表**（`APIKeyCapability`，勾选）：
     - `retrieve`：检索 KB
     - `chat`：聊天
     - `ingest`：上传文档
     - `manage_kbs`：管理 KB
     - `manage_agents`：管理 Agent
     - `run_workflows`：跑工作流
     - `manage_models`：管模型
     - `manage_members`：管成员
   - **KB allow-list**：限制该 Key 只能访问哪些 KB（强安全）
   - **过期时间**：可设置
4. 生成后**立即复制 Key**（明文只显示一次）

> API Key 与 JWT 用户是**正交**的两个鉴权维度。Key 默认拒绝未在 `apiKeyRouteAuthorizer` 注册的路由。

---

## 7. 跨工作区共享（Organization）

> 把 KB 共享给跨部门的团队。

1. 进入 `/platform/organizations`
2. 「**+ 新建组织**」→ 填名称 → 保存
3. 进入组织详情 → 「**成员**」 → 添加其他工作区的成员（按邮箱）
4. 设置成员的 `OrgMemberRole`：
   - `admin`：完全控制共享 KB
   - `editor`：编辑共享 KB
   - `viewer`：只读
5. 在 KB 详情 → 「**共享**」Tab → 启用共享给哪些组织

> 跨工作区 = **第二层 RBAC**，与租户角色独立。

---

## 8. 转让所有权

> 把 Owner 身份转给其他成员。

1. **第一步**：先把目标成员提升为 Owner（详见 § 3）
2. **第二步**：把自己的角色降级（这时对方是 Owner，您是 Admin）
3. **第三步**（可选）：自己退出空间

或者使用转让专用的「**转让所有权**」功能（如果有 UI），一步完成。

---

## 9. 删除空间

> ⚠️ **不可恢复**。会异步清理所有 KB、Agent、Workflow、文档、Wiki。

1. 进入 `/platform/settings?section=tenant`
2. 滚动到底部 → 「**删除空间**」
3. 输入空间名确认
4. 提交 → 异步任务 `ProcessKBDelete` 启动
5. 几分钟后空间从列表消失

**约束**：

- 不能删除还有成员的活跃空间（要先移除所有成员）
- 不能删除您**不在**的工作区

---

## 10. 关键页面详解

### 10.1 成员管理（`/platform/settings?section=members`）

| 元素 | 说明 |
|---|---|
| 「+ 邀请成员」 | 邀请已注册用户 |
| 「+ 创建链接」 | 创建 share-link |
| 「邀请」Tab | 查看 / 撤销邀请 |
| 「成员」列表 | 角色筛选、搜索、修改角色、移除 |
| 角色权限矩阵 | 内嵌展示 4 级角色能做什么（来自 `TenantMembers.vue` 的 `roleMatrix`） |

### 10.2 空间信息（`/platform/settings?section=tenant`）

| 字段 | 谁能编辑 |
|---|---|
| 空间名 | Owner |
| 描述 | Owner |
| `is_joinable` | Owner |
| `storage_quota` | Owner |
| `business` | Owner |
| 创建时间 / 配额使用 | 只读 |

---

## 11. 常见问题（FAQ）

### Q1：为什么我点不动「删除空间」按钮？
A：检查是否有未移除的成员。先移除所有成员（除了您自己），再删。

### Q2：share-link 有有效期吗？
A：有，默认 7 天（`RBAC_INVITATION_TTL`）。也可设置一次性 / 多次使用。

### Q3：通过 share-link 加入的用户需要审批吗？
A：**不需要**。这是绕过待审批的快速通道。

### Q4：API Key 泄露了怎么办？
A：立即在设置页**撤销**（revoke），重新创建。撤销是不可逆的。

### Q5：我能把 Owner 转给不在本工作区的用户吗？
A：不能。Owner 必须是当前工作区的成员。先邀请对方加入 → 提升为 Owner。

### Q6：Owner 能退出自己的空间吗？
A：如果您是**最后一位 Owner**，不能退出。先把 Owner 转给别人。

---

## 12. 工作流检查清单（季度）

- [ ] 成员名单是最新的吗？有没有已离职的遗留？
- [ ] Owner / Admin 是否过多？（建议每空间 ≤ 3 个 Owner）
- [ ] 审计日志是否有异常拒绝？
- [ ] share-link 是否还有过期的？
- [ ] API Key 是否还有未使用的？
- [ ] 存储配额使用率？（避免超限）

---

## 13. 进一步阅读

- [modules/06-system-settings.md § 3 租户 API Key](../modules/06-system-settings.md) — API Key 完整说明
- [modules/06-system-settings.md § 4 Organization](../modules/06-system-settings.md) — 跨工作区
- [`docs/RBAC说明.md`](../../RBAC说明.md) — RBAC 设计原文
- [README.md § 角色矩阵](../README.md#角色矩阵速查)
- [README.md § 关键不变式](../README.md#关键不变式手册通则)