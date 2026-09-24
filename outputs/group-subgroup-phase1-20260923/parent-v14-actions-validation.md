# 第十四版：Actions 删除最终授权复验

日期：2026-09-24。基线仍为源码工作树 `main @ 9a3f93813847f53b4762e28863b09b9266f75943`；本批只围绕已复现的归档删除缺口补齐服务层、四个用户入口及相关测试，未提交或推送源码。

## 红例与实现

v13 专用仓库 19 归档后，Web 删除 Run 78 的 `phase1-archive-probe` 返回 200 且产物进入删除状态，REST 删除 Run 80 返回 204 且记录消失。原路由只有 Actions 单元写权，没有最终生命周期检查；这破坏已承诺的归档只读语义，构成 P1。详见[原始红例及子代理证据](release-artifact-delete-evidence.md)。

v14 用户删除在 `WithWrite(repository:id)` 内复用 `CheckRepositoryContentWrite`，重读当前身份、委托、仓库及祖先生命周期。单件产物核对 repo／run／attempt 与产物状态；整次运行删除重读当前完成状态，并在同事务枚举任务、产物、清理快照和审计后删除记录。并发重跑更新最新 attempt 时会同步当前 Run 状态，因此旧完成快照不能删除新运行。持久清理记录与删除原子提交，实际存储删除仍在事务外；内部保留期清理不经过用户守卫。

父任务与独立 GPT-6 Sol high 子代理对最终调用链分别审查，未保留成立的 P0／P1。Runner v4 已认证在途请求与归档的交错、历史 legacy attempt 回填与删除交错仍缺专项实测；不把缺少这两项证据直接判成严重漏洞。

## 构建与实际版本

隔离副本 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5` 同步七个相关文件；原 v13 文件保存在 `/Users/archer/.cache/gitea-phase1-v5-validation/v14-prior-source`，完整差异与源码摘要分别为 `v14-source.diff`、`v14-source-manifest.json`。新增测试 fixture 修正后更新摘要，业务代码未再修改。

使用 Go 1.26.4 执行 `go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .`，退出 0；本批增量 golangci-lint 为 0 issues。macOS arm64 开发二进制 `gitea-phase1-ancestors-v14` 的 SHA-256 为 `da6bb584f752034da06824f29db71363bb3992bd0351088843a703ad358dd2c6`。没有覆盖源码根目录原有 Linux 二进制。

确认无非终态作业、核对 13043 唯一进程后正常停止 v13，备份数据库和配置到隔离工作目录 `backups/before-v14-20260924-021613`，再原子更新 `gitea-current` 启动 v14。此为本机开发构建，不是一期候选发布。

## PostgreSQL 回归与首错

环境为已有隔离 PostgreSQL 17、MinIO 与本机 arm64 应用，连接 `127.0.0.1:15433`，数据库 `gitea_phase1_test`；不属于目标 Linux amd64 容量环境。最终测试实际查询并断言 `postgres / gitea_phase1_test`。

命令：`go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json -run '^(TestArchivedRepositoryRejectsUserActionsDeletion|TestActionsDeleteRunRejectsStaleCompletedSnapshot|TestActionsDeletionRechecksCurrentActor|TestExpiredV4ArtifactUserDeletion|TestActionsDeletionRejectsForeignTargets|TestAPIActionsWorkflowRun|TestActionsArtifactV4DeletePublicApi|TestActionsDeleteRun|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration`。使用 `GITEA_TEST_DATABASE=pgsql` 及上述隔离数据库／MinIO 参数，关闭代理变量。

首次扩大运行发现旧 `DeleteRunGeneral` 正例缺少 Actions 单元，当前权限检查返回 403；仅在该样本增加 Actions RepoUnit 后复测，生产检查未放宽。子代理此前的带斜杠正则也未实际选中 API Run 子测试，已更正历史结论。首败原始文件保留为 `v14-actions-delete-pg-first.jsonl`。

最终 **9 个顶层测试、含子项 36 个通过，0 失败、0 跳过，包耗时 10.052 秒**。包括四个归档入口、陈旧完成快照、当前身份、异仓／异 attempt、过期 v4 产物、原生正常删除与重跑 API、真实 Git fixture 的整次运行删除。最终原始文件 `/Users/archer/.cache/gitea-phase1-v5-validation/v14-actions-delete-pg.jsonl`，SHA-256 `184c8688f24b4ee3ff62be4bf6f0fd930a0265f2d0d8d974964d7a95e9c90360`。

## 同对象真实协议复验

父任务运行 `python3 outputs/group-subgroup-phase1-20260923/parent-v14-actions-delete-check.py archived`，合成密码在 PTY 无回显输入。只验证原专用仓库 19 的既有 Run 78；HTTP 先确认仓库 ID 和归档状态，不靠手工改库制造结果。

- 普通非实例管理员 Owner 登录独立 Web 会话。
- 对保留产物 `phase1-retain` 的 Web DELETE 返回 423。
- 对同一个 Run 78 的 Web POST 删除和 REST DELETE 均返回 423。
- 每次拒绝后都重新下载同一 ZIP，内容仍是原 38 字节测试文件，摘要一致；拒绝没有损坏原记录或文件。

原始记录为 [parent-v14-actions-delete-archived.json](parent-v14-actions-delete-archived.json)，脚本退出 0。公开按 ID 的产物删除只支持 v4，本样本是实际 v3，因此没有拿 v3 的 REST 404 冒充 v4 拒绝证据；v4 路径的 423 有上面的真实 PostgreSQL HTTP 集成证据。

浏览器在 v13 已观察到归档页面的产物删除按钮并打开确认框，但没有完成确认；该测试确认框阻塞了后续点击，工具不能关闭。v14 重启后原内存会话失效、页面转为匿名 404，需要重新登录；当前浏览器交互受阻，故新按钮状态及恢复后的最终 UI 删除仍未验证。已请求用户取消测试确认框，其他验证继续推进，不将工具受阻认定为产品缺陷。

一期仍未完成：容量／性能、80% 人工效率及剩余真实角色、协议、生命周期和恢复矩阵仍缺合格证据。
