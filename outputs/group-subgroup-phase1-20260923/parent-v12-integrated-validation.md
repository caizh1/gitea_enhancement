# 第十二版：诊断、审计元数据与成员来源复验

日期：2026-09-24。以下仅为本机隔离开发构建的结果，不是一期全部通过或候选发布。目标 Linux amd64、8 vCPU、16 GiB 容量环境暂未提供，人工效率 80% 也没有合格对照记录。

## 构建与部署

在已有隔离副本 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5` 同步 14 个相关文件，重新生成 `modules/templates` 与 `modules/options` 的 bindata 后构建。使用 Go 1.26.4，命令为 `go generate -tags='bindata sqlite sqlite_unlock_notify' ./modules/templates ./modules/options` 与 `go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .`，均退出 0。未改变源码仓库的 Linux 二进制。

产物：`gitea-phase1-ancestors-v12`，macOS arm64，SHA-256 `e80d141963ce43ff15e9d3e0695eede84062f8a8a803380b025fb4b7069a098f`。原始构建日志与源码摘要清单位于 `/Users/archer/.cache/gitea-phase1-v5-validation/v12-build.log`、`v12-source-manifest.json`。子代理冻结后父任务逐项复核，清单中没有同步后变化。

确认 13043 端口唯一进程属于一次性实例、没有等待／执行中任务后，正常停止 v11，备份 SQLite 与配置，再原子更新 `gitea-current` 并启动 v12。没有强杀或触碰生产数据。重启后重新登录，不声称会话持久化。

## 父任务审查与真实界面

### 可信工作流诊断

父任务核对来源、提交、配置修订及最新 Attempt 的绑定，新增分支都返回原有合并阻断错误；必选任务成功和可信 Runner 证明仍为放行条件。旧 `scopedRunPatternResult` 本就要求整个 Run 和 Attempt 成功，因此诊断顺序调整没有新增整体成功门槛或放宽失败状态。

真实 PR 5 的必选 Run 48 已失败。v11 页面错误描述为未在来源信任的 Runner 完成；v12 同一 PR 显示实际运行失败并提示检查失败任务、修复后重新运行，合并仍被阻止。此次只复验失败状态文案；等待、取消及伪造成功的其他分支由定向测试覆盖，不当作新的真实 Runner 场景。

### 审计员入口与撤权

宽回归发现的旧断言没有被跳过：原本期望 404 的 Issue 写入现在正确返回 403；两个治理入口返回 303 到现用页面。更新测试检查最终页面、只读提示及无编辑控件，保留真实写入拒绝与另有 Developer 授权的正例。

父任务经正式管理 API 把一次性账号 3 暂设为审计员，账号仍非实例管理员。真实浏览器从旧成员入口跳转到仓库 12 当前协作者页，显示“全站审计员只读查看成员来源；编辑成员须另有项目授权”，没有添加／邀请／修改／移除按钮。实际 API 读仓库 200、创建 Issue 403；撤销审计员后，API 读和写均 404，同一浏览器会话刷新也为原生 404。没有实际创建 Issue，最后只读数据库确认账号 `is_admin=0`、`is_auditor=0`。

### 邀请预览

Owner 从仓库邀请页面创建 Reporter 测试邀请 5，本机 SMTP 收信后收件人打开邮件，v12 明确显示“当前邀请目标”及完整仓库路径。随后 Owner 从 UI 撤销，收件人再次打开同一链接得到原生 404。此邀请没有接受，不产生残留授权。访问申请的旧路径快照文案保持独立。

转移后接受邀请、Developer 真正推送、直接与继承来源组合、管理者逐来源撤权及短期成员到期的补充实测在 v11 完成，详见[多来源记录](parent-v11-multisource-invitation-validation.md)，不能将其错误标成 v12 首次运行。

## 审计导出保持接口兼容

父审查退回了直接删除已在 Swagger 公开的 `max_sequence` 的初稿。最终方案保留字段，在模型使用非持久响应值，内部 `MaxID` 继续保存完整快照上界且不直接序列化；响应查询复用相同审计筛选与快照限制。POST 在原创建授权事务内计算，GET 用同一稳定读取边界包含现有权限检查和响应查询。分页、快照存储和下载消费者未变，没有新增迁移。

兼容结论限定为字段名、JSON 类型及现有仓库消费者；字段含义收窄，依赖原全局含义的外部客户端没有实际验证。现有事件 ID 仍为全局自增，两条可见事件之间的序号间隔可能透露粗粒度站点活动，记为残留 P2。范围内内容授权没有放宽，未发现借此读取其他群组事件内容的路径。

在运行中 v12 实例通过正式 API 申请两份群组 15 导出：

| 筛选 | API 创建／查询 | 对外 `max_sequence` | 内部快照上界（仅只读数据库佐证） | 完成后下载 |
| --- | --- | --- | --- | --- |
| `member.expired` | 202／200 | 375 | 386 | 1 条，恰为到期事件 375 |
| 无匹配测试事件 | 202／200 | 0 | 387 | 0 条有效 JSON 数组 |

两任务完成后 GET 仍保留对应范围值，无关用户查询均 404。浏览器审计页显示两任务可下载及正确的 1／0 行数，并实际点击第一份下载链接；API 客户端另行核对下载字节及 SHA-256，不把 API 下载当成浏览器最终落盘证据。浏览器下载文件的实际保存位置仍未核对。

原始运行结果：`/Users/archer/.cache/gitea-phase1-v5-validation/v12-audit-export-runtime.json`。首次下载已经成功，但父任务辅助脚本误用 `type` 字段而报 `KeyError`；核对现有 JSON 契约 `event_type` 后更正断言，两份下载均通过，没有修改业务代码来迎合错误脚本。

## 真正 PostgreSQL 整合回归

在独占的 PG 17.11 测试库 `gitea_phase1_test` 和隔离 MinIO 运行：

```sh
go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json -run '^(TestGovernanceAuditExportScopedSequence|TestGovernanceAuditHTTP|TestGovernanceAuditorPermissions|TestRepositoryInvitationLinkFromCollaborators|TestActionsScopedWorkflows|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
```

六个顶层测试全部通过；含子项共 26 个 pass 事件、0 fail，包耗时 92.865 秒。数据库断言实际输出 `postgres / gitea_phase1_test`。测试进程墙钟 102.37 秒包含编译等开销，与包测试时间分开。原始日志为 `/Users/archer/.cache/gitea-phase1-v5-validation/v12-integrated-pg.jsonl`，SHA-256 `190c875f42f5a1cf09174a1ec3759a39040bd9098055c73673e72391e8204998`；父任务独立解析了测试事件和引擎断言。

此前只隐藏字段的初稿测试不能冒充最终兼容方案结果；最终结果以上述 v12 整合及[审计修复证据](audit-export-sequence-evidence.md)为准。子代理的 SQLite、游标、门禁与增量 lint 结果分别见[诊断证据](trusted-workflow-diagnostic-evidence.md)和[审计员复查](auditor-navigation-evidence.md)。

## 判定与剩余范围

本批代码审查 **PASS**，没有反证后仍成立的 P0／P1；接口兼容问题已在交付前调整。未验证的完整角色／协议／生命周期及并发矩阵、目标容量和人工效率仍阻止一期完成。此次未执行 MySQL／MariaDB／MSSQL 回归，未扩建 Runner 协议或新增功能平台。
