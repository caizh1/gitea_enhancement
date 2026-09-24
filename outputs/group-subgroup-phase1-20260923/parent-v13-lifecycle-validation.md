# 第十三版：带仓库归档恢复及协议删除复验

日期：2026-09-24。只使用本机隔离实例、合成身份及新增测试对象。原十仓效率样本未替换。Linux amd64 容量环境未提供，人工效率仍未实测；本记录不构成一期发布批准。

## 构建与最小修复

v12 实际页面复现：父群组已归档时，普通 Owner 从后代仓库原生设置点击解除归档，服务端正确拒绝，但页面仅提示查日志。v13 只在 `handleSettingsPostUnarchive` 的现有 `ErrConflict` 分支显示可操作说明：群组归档、待删除或进行中操作会阻止恢复，应到群组设置或删除恢复页面处理。其他错误仍走原提示。Owner 入口、事务内当前身份重检与归档事务均未修改，提示不包含无权来源名称或路径。

父任务实施，GPT-6 Sol high 子代理独立只读审查通过。增量 `golangci-lint run --new-from-rev=HEAD ./routers/web/repo/setting/...` 输出 `0 issues`；单文件 `gofmt -l` 与 `git diff --check` 通过。没有为文案映射另写镜像测试。

隔离副本重新生成 options bindata 并构建，命令为 `go generate -tags='bindata sqlite sqlite_unlock_notify' ./modules/options`、`go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .`，均退出 0。产物 `gitea-phase1-ancestors-v13`，macOS arm64，SHA-256 `4d70a5a01367f3c33e289395416470fd05a7343b6a86e30c949ff80697c56ae0`。源码摘要和构建日志在 `/Users/archer/.cache/gitea-phase1-v5-validation/v13-source-manifest.json` 与 `v13-build.log`。

确认两项并行协议验收停止写入、实例无非终态作业，验证 13043 唯一进程命令后正常终止 v12，备份 SQLite／配置到隔离工作目录的 `backups/before-v13-20260924-012539`，原子更新 `gitea-current` 并启动 v13。未改源码根目录原有 Linux 二进制。

## 群组归档、独立归档与恢复

普通非实例管理员 `phase1-owner` 从群组 16 的“创建仓库”入口新增仓库 13 `phase1-archive-active` 与仓库 14 `phase1-archive-prior`，两者都初始化真实 README。先经原生设置独立归档仓库 14；随后从群组设置归档本组及后代。预览明确显示“1 个群组、2 个项目、原已归档 1 个”，并提示恢复会统一恢复后代、不保留原独立归档。

| 阶段 | 页面及真实消费者结果 |
| --- | --- |
| 父组归档前，v12 | 两仓 HTTP Git clone 成功；活动仓库推送新测试分支成功，独立归档仓库推送 403、Git 退出 128。 |
| 父组归档后，v12 | 群组显示已归档，创建入口消失；两仓 `ls-remote` 仍成功，新的 push 均 403／128。Owner API 读取为 200、`archived=true`，Issue 创建为 423 `repo is archived`；无关用户读取为 404。 |
| 单仓解除归档，v12／v13 | v12 正确拒绝但只让用户看日志；v13 同一真实操作显示来源限制及恢复处理路径，仓库仍归档。 |
| 来源群组统一恢复，v13 | 预览显示两个已归档仓库；UI 恢复并刷新后归档徽标消失，创建入口恢复。只读数据库佐证群组 16 和仓库 13／14 的归档标记均为 false。两仓随后推送新的 `phase1-archive-after` 分支成功，远端 SHA 分别为 `8fcea01ecb6b7b82a4eadeab57f1116054ef1f42`、`5741120157d0157415c30f8e671ba7b2773549f0`。 |
| 恢复后实际写入，v13 | Owner API 为仓库 13 创建 Issue 1 返回 201，浏览器详情真实显示该标题与内容；无关用户读取仍为 404。 |

本次恢复包含原先独立归档的仓库，实际结果符合已冻结的统一恢复语义。GitLab `v19.4.0-ee` 的固定源码与测试也已核实此细节，见[官方语义证据](gitlab-19-4-archive-state-evidence.md)；没有运行 GitLab 实例，也没有把待删除项目混合状态算作已验。

协议原始结果在 `/Users/archer/.cache/gitea-phase1-archive-68i19l6_/before.json`、`during.json`、`after.json`。首次辅助脚本误把归档 Issue 拒绝预期写成 403，实际为现有 423；只纠正断言。第一次恢复点击后立即刷新与提交发生交错，页面短暂保留旧标题；确认数据库事务已统一完成后再次刷新，页面一致。没有重复提交恢复或据此报告部分持久化。

## 制品删除的 UI 补验

Generic／OCI 的真实发布、拒删、合法删除、其他版本保留已在 v12 完成，见[协议证据](package-delete-acceptance-evidence.md)。父任务在 v13 进一步验证 Generic 原生 UI：

- Reporter 从深层群组可见的“制品”入口找到测试包；可见版本及下载入口，没有 Settings／Delete version。协议拒删的服务端证据仍按 v12 独立记录。
- Owner 打开首轮一次性包 `phase1-delete-generic-1790184068819120000` 的 2.0.0，使用 Delete version 确认框删除。页面显示删除成功；刷新后该包不在列表，旧详情为原生 404。
- 使用页面安装命令中的现有组织标识调用真实 Generic API：已删除版本 GET 404，独立对照 `phase1-delete-generic-1790184137128077000` 的 2.0.0 GET 200、66 字节且摘要仍为 `87e123f53ed40446d0bb666db8afe442b7b699362680b05145635534760725d3`；原 `phase1-client-1790170311` 仍 GET 200、47 字节，摘要未变。

原始记录 `/Users/archer/.cache/gitea-phase1-v5-validation/v13-package-ui-delete.json`。父任务首次辅助下载误将层级全路径代入原生包 API 的 owner 段，已存在对照包也返回 404；按当前页面安装命令更正后才进行删除结论。包 Web 地址使用层级路径，原生协议命令当前使用组织标识，不能宣称两种地址可通用替换。此处额外删除的是首轮测试包 2.0.0，不覆盖子代理先前保留该版本的历史事实，也未删除正式对照包或旧验收包。

## 真正 PostgreSQL 与分支保护补验

在已有隔离 PG 17.11／MinIO 使用相同测试环境，执行：

```sh
go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json -run '^(TestGovernanceGroupArchiveRealGit|TestGovernanceNativeArchiveAudit|TestArchivedGroupDeveloperCannotMutateReleaseAPI|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
```

四个顶层测试全部通过、0 fail，包耗时 12.866 秒，实际引擎断言 `postgres / gitea_phase1_test`。原始日志 `/Users/archer/.cache/gitea-phase1-v5-validation/v13-archive-pg.jsonl`，SHA-256 `a35ff20585726d5fb7fa92ba799c423618acf87072e74406614a1ec8fc08c876`。

另一个 GPT-6 Sol high 子代理在 v12 的仓库 6 补验 API 文件创建／修改、禁推分支拒绝、普通分支强推允许、受保护分支强推拒绝和无关账号拒绝，保留原 main 不变。受保护 Secret 的旧提交失败经父任务核对 `ProtectedRefTrusted` 后确认是提交在领取前不再为 HEAD；切换 v13 后保持 HEAD 不变的新 API 提交，真实祖先 Run 60 和凭据探针 Run 61 均成功。完整步骤、SHA 与时间见[分支保护证据](api-branch-protection-evidence.md)，不把中途失败隐藏或误报为 API 身份差异。

父任务另从仓库 Actions 列表进入 Run 61／Job 67，真实页面显示 Success；展开“只验证是否下发，不回显内容”步骤，日志显示 `事件=push`、完整受保护引用、`凭据存在=true` 和“受保护正例通过”。列表中 Run 60 同样成功，历史 Run 53／55 的失败仍保留。`ref_protected` 显示不可用，与已有 Runner 兼容限制一致。

## 判定与剩余项

本批改动与所列消费者复核 **VERDICT: PASS**，没有成立的 P0／P1。原生单仓解档反馈 P2 已修复；接口和授权行为保持。

仍未验证：归档中的运行任务、待删除项目与其他协议混合状态，OCI 原生 UI 删除、多 tag 同摘要和物理 blob 回收，Release／artifact 删除，以及完整角色、并发、重启恢复矩阵。受保护 Secret 当前只认可特定可信事件的当前受保护分支 HEAD，旧排队提交可能得不到凭据；这是一项须明确保留的实现限制，不能泛称与 GitLab 全部凭据语义等价。目标容量与真实人工效率仍是一期未完成的独立门槛。
