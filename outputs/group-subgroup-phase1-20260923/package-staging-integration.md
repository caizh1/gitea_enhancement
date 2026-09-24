# I-07 包暂存与清理 PostgreSQL/MinIO 对抗验证

日期：2026-09-23。只使用本机隔离 PostgreSQL `127.0.0.1:15433/gitea_phase1_test` 与 MinIO `127.0.0.1:19000`；未连接生产环境。测试口令仅由进程环境提供，本文省略。

## 验证副本与首因

将已冻结的包模型、服务、上下文、OCI 路由文件及本次新增的 `tests/integration/governance_package_cleanup_test.go` 同步到 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5`。两个关键源码文件在副本与工作区的 SHA-256 一致；副本根目录已有 `gitea` 是 macOS arm64 Mach-O，未构建或替换 UI 二进制。此次没有出现测试失败，不存在需掩盖或重试的失败首因。

## 实际执行

在副本中用 `GITEA_TEST_DATABASE=pgsql`、`TEST_PGSQL_HOST=127.0.0.1:15433`、`TEST_PGSQL_DBNAME=gitea_phase1_test`、`TEST_PGSQL_USERNAME=phase1_test`、`TEST_PGSQL_SCHEMA=public`、`TEST_MINIO_ENDPOINT=127.0.0.1:19000` 启动单个 `go test -p 1 -count=1` 进程；密码变量在命令记录中省略。

现有用例实际执行并通过：`TestPackageGeneric`、`TestPackageContainer`、`TestGovernancePackageCleanupConcurrentUpload`、`TestGovernancePackageCompensationConcurrentUpload`、`TestGovernancePackageCleanupRecovery`。退出 0，`ok gitea.dev/tests/integration 8.702s`。HTTP 轨迹中 Generic 上传/下载分别返回 201/200，OCI blob 上传、跨镜像挂载与 manifest 写入均走真实测试路由，相关子用例通过。

新增四个固定交错用例实际执行并通过：

- `TestPackageFailedStageRefreshSurvivesConcurrentCleanup`：旧数据库 blob 行已过期、对象存储不存在；新上传完成 `PrepareBlobUpload` 后阻断 MinIO `Save`，同时运行过期清理。清理在对象存储仍阻断时完成，刷新行仍存在，解除阻断后正文完整可读。
- `TestPackageConcurrentIdenticalStage`：两个同哈希上传同时进入 MinIO `Save`，最后获得同一 blob ID 且正文完整。
- `TestPackageQueuedCleanupSkipsRestagedReference`：旧 blob 删除与对象清理任务已提交，但对象删除尚未执行；同哈希重新暂存并建立文件引用后运行旧任务，旧任务不删除新引用的对象。
- `TestPackageArchiveDuringStageRejectsFinalReference`：MinIO `Save` 被阻断时归档包所属群组；归档不被正文传输占用的治理锁阻塞。解除阻断后最终写事务返回冲突，未生成可读取的包引用。

四条用例合并命令退出 0，`ok gitea.dev/tests/integration 3.750s`。`git diff --check` 对新测试文件退出 0。撤权发生在正文写入期间的最终拒绝另有 `TestPackageUploadRevokedDuringContentSaveLeavesNoReference` SQLite 服务测试，已在冻结前通过；此次 PG 固定交错覆盖归档边界。

## 并发因果链与审查结论

`PrepareBlobUpload` 外层 `db.WithTx` 包住 `GetOrInsertBlob` 的治理写锁、哈希锁及创建时间刷新；嵌套 `WithTx` 共用同一事务，锁直到最外层提交才释放。提交后 `StagePackageBlob` 才执行 MinIO `Has/Save`，因此网络正文传输不持有全局治理写锁。清理排队和执行时按哈希锁排序，并在删除对象之前查询当前是否已有同哈希数据库行；新暂存行或新引用均可阻止旧任务删正文。最后包元数据事务另行重新检查当前主体、能力及归档状态。

24 小时最小清理宽限仅用于避免正常在途暂存行被扫描为过期，同时给失败上传留回收窗口；它不替代事务锁、哈希锁或删除前当前引用复核。失败正文是内容寻址孤立对象，需由既有清理任务后续回收。

VERDICT: PASS。本轮对抗检查未发现成立的 P0/P1。

P0/P1 BLOCKERS: 无。

UNVERIFIED RISKS: 未在多应用进程、其他 S3 实现或长于 24 小时的异常传输中复现同一交错；这些不影响当前固定 PG/MinIO 结论。真实 Generic/OCI 客户端和浏览器操作仍归父任务验收。

NON-BLOCKING FINDINGS: 失败暂存正文至少保留 24 小时，磁盘/对象存储容量运营应考虑这段清理延迟。
