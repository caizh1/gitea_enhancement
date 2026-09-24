# 审计导出序号范围与兼容性证据

## 成立路径与最终修复

- 旧 `AuditExport.MaxID` 从全局审计表读取最新事件 ID，兼作导出快照上界和 API `max_sequence`。只导出群组 3 的用户也可由这个响应字段观察群组 6 等无权范围的全局活动上界。创建与进度两个 API 都直接序列化任务。
- 保留已公开的 `max_sequence` 字段，但将内部 `MaxID` 标为 `json:"-"`。响应字段 `MaxSequence` 是当前导出筛选范围内、`id <= MaxID` 的最大可见事件 ID；空结果返回 0。查询复用原有 `auditCondition`，不改变快照条件、分段、游标、内容授权或数据库模式。
- POST 在 `CreateAuditExport` 的原有写事务中完成授权、固定全局快照上界、查询响应序号；随后产生的申请审计事件在上界外。GET 使用 `WithStableRead`，在同一稳定读事务内复用 `GetAuditExport` 当前用户与范围授权并查询响应序号；撤权不能落在授权与新增元数据查询之间。
- 两份 Swagger JSON 模板保留 `max_sequence`，说明它是授权筛选范围内的快照末序号。仓库中未找到依赖旧全局含义的客户端；字段名和 JSON 类型兼容，但数值语义收窄。需要全局序号的外部客户端不能再据此推断站点活动。
- `SegmentFrom`、`CursorID`、`Chunks`、`AncestorIDs` 仍为 `json:"-"`；未发现同一 API 另有内部游标或全局末序号外显。

## 红绿记录

- 最初红例证实旧 POST 响应含 `"max_sequence":5`（全局上界），原始 JSON：`/Users/archer/.cache/gitea-phase1-v5-validation/audit-export-sequence-red.jsonl`。中间曾试过删除字段；因 Swagger 已公开且要求保留 API，撤回该方案。兼容方案修改前的新例 `TestGovernanceAuditExportScopedSequence` 失败：POST 202 响应缺 `max_sequence`，断言位于测试文件第 33 行；命令 `GITEA_TEST_DATABASE=sqlite go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -run '^TestGovernanceAuditExportScopedSequence$' ./tests/integration`。
- 兼容方案 SQLite 最终绿例：同一 Go 工具链运行 `GITEA_TEST_DATABASE=sqlite go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json -run '^(TestGovernanceAuditExportScopedSequence|TestGovernanceAuditHTTP)$' ./tests/integration`，顶级 2 项通过（0.14 秒、0.13 秒），package 2.078 秒；原始 JSON：`/Users/archer/.cache/gitea-phase1-v5-validation/audit-export-scoped-sqlite-final.jsonl`。新例固定 POST/GET 及导出完成后三次相同序号、无权群组事件与后续事件不影响该值、空筛选返回 0、导出内容不越过授权范围或快照。
- 已有 `TestAuditExportResumesAtTimeAndRowBoundaries` 单测通过：`go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -run '^TestAuditExportResumesAtTimeAndRowBoundaries$' ./models/governance`，package 2.754 秒。
- `golangci-lint run --new-from-rev=HEAD ./models/governance/... ./routers/api/v1/governance/... ./tests/integration/...` 为 0 issues；限定文件 `git diff --check` 通过；两份 Swagger 模板经 JSON 解析有效。
- 使用项目指定 go-swagger 版本运行 `go run github.com/go-swagger/go-swagger/cmd/swagger@v0.34.1 generate spec --exclude gitea.dev/sdk --input templates/swagger/v1_input.json --output /Users/archer/.cache/gitea-phase1-v5-validation/audit-export-generated-swagger-final.json`；生成的 `definitions.AuditExport.properties.max_sequence` 与仓库 Swagger 2 模板属性完全相同。非持久模型字段有 `json` 标签和注释，下次生成不会丢失。OpenAPI 3 模板保留同名、同类型和说明；它由 Swagger 2 转换，尚未另在本包运行转换器。

## 验证边界

此前 PostgreSQL 两项通过的记录只对应已经撤回的“删除字段”中间方案，不能算当前兼容方案的 PG 结果。当前方案的真正 PostgreSQL 定向回归由父任务在 v12 整合副本与审计员测试合并执行；本包没有占用共享 PG，也未宣称运行实例或目标 Linux 已验证。

后续父任务已在 v12 完成最终方案的真正 PostgreSQL 整合与运行实例验证：六个顶层测试通过，范围导出 API 返回 375／0，两份下载分别有 1／0 条。详细命令、日志摘要和兼容边界见[v12 复验](parent-v12-integrated-validation.md)。
