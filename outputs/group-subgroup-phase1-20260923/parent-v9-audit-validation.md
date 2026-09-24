# 第九版：审计页面、权限拒绝与导出验证

日期：2026-09-23。只操作本机隔离实例与测试身份；第九版为 macOS arm64 开发构建，尚非一期候选发布。

## 改动和独立复核

`routers/web/governance/audit.go` 的公共 Web 错误处理将治理“未找到／无权”改用已有 `ctx.NotFound(nil)`。邀请入口不再保留重复特殊处理。父任务追查 `services/context/context_response.go` 与原生 404 模板：保持 HTTP 404，HTML 请求使用无敏感错误详情的页面，其余客户端仍返回简短文本；没有修改授权计算或 API 错误协议。

红例为第八版中 Developer 访问审计页，服务端拒绝但本次 IAB 停留在原页面；审计集成测试的 HTML 类型断言在旧实现失败。修复后 SQLite 定向与下列 PostgreSQL 回归通过。此修复是错误呈现修复，不将旧版本描述为已经放行审计权限。

## 构建与回归

- 第九版 SHA-256：`e8d62e8f3f9557ec0f5b9e32a0607e1aa31f056cb025fdab645045075ad8ca34`。
- 隔离副本执行 `go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .`，退出 0。沿用第八版已经重新生成的前端及 bindata，本轮未修改模板和前端。
- 根目录原有 Linux `gitea` 的 SHA-256 仍为 `10f50310cfe1cf1d878c6a78fae4601d79f264e6dfe478edbc16b34fb0c75d5a`。
- PostgreSQL 命令：`go test -json -count=1 -p 1 -tags='bindata sqlite sqlite_unlock_notify' -run '^TestGovernance(AuditHTTP|Invitation(HTTP|WebForm|RevokedWebRendersHTML404))$' ./tests/integration`。使用既有隔离 PG／MinIO 环境，代理变量移除，凭据只在进程环境中使用。
- 上述 4 个顶层测试全部通过，包耗时 4.708 秒。原始记录在 `/Users/archer/.cache/gitea-phase1-v5-validation/v9-pg.jsonl`，父任务再次解析确认无失败事件。
- 第八版的独立 PG 记录 `v8-pg.jsonl` 为 5 个顶层测试通过、4.934 秒；不能合并成第九版全矩阵回归结论。

切换前确认无活跃 Actions 作业，验证目标二进制、配置和唯一监听进程；正常停止隔离应用后备份 SQLite 与配置，再原子切换固定 `gitea-current` 入口并启动。删除本次诊断用的邀请路由日志配置，未清理队列或重置数据。

## 真实浏览器结果

1. Developer 访问群组 6 审计页，实际显示原生 `404 Not Found` 和不存在／无权的通用说明，无群组审计数据。
2. 普通 Owner 从 Dashboard 的“群组”进入父群组，再点击“审计”。页面列出深层仓库 Run49、PR5 和 Git 引用事务等事件；没有使用实例管理员身份。
3. 在事件类型输入 `actions.secret_created` 并点击查询，得到两条 Secret 创建事件。展开受保护 Secret 的“事件证据”，详情只有 `description_configured`、名称、`protected` 与 `value_configured`，没有 Secret 值。刷新后筛选与记录保留。
4. 页面点击“导出 CSV”，任务 `ce1dde89-c86c-4d6e-9c70-c232bdbdce46` 先显示等待生成；刷新后显示“可下载 · csv · 2 条”，提供“下载证据”链接，并实际点击该链接。
5. 当前窄页面截图中，查询按钮、导出按钮和证据内容可见，长路径及 JSON 换行。此次没有测全部尺寸和所有错误状态。

## 客户端与数据库佐证

使用同一导出 ID 的 API 下载地址：Owner 实际得到 HTTP 200、`text/csv`、879 字节；解析为两条 `actions.secret_created` 事件，筛选生效。Developer 请求同一地址为 HTTP 404。

下载文件保存在私有缓存 `v9-audit-export.csv`，权限 0600，SHA-256 为 `7697ffa892733a1e71ce053d9f37baa79fda730375c7f69c40bc361b1e41b0d9`。浏览器的下载点击与 API 内容校验分别记录；未声称已经核对浏览器最终文件落盘位置。

只读查询审计表全部 `details`，以及检查导出 CSV，均未找到本次受保护 Secret 的测试明文；这只证明该固定样本没有泄漏，不是对所有敏感字段的绝对保证。

## 判定边界

本次审计页面正反例、过滤和导出子项通过；历史移动范围、全部过滤／分页、导出期间撤权及重启恢复仍需真实场景验收。独立审查还记录非阻塞 P2：导出 API 元数据的全局 `max_sequence` 可间接反映实例活动量，导出行仍经过范围过滤。

目标 Linux amd64／8 vCPU／16 GiB 环境用户明确暂未提供，容量性能未验证；人工活跃时间和重复操作减少 80% 未实测。一期整体未完成。
