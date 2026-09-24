# I-07 LFS 与 Release 最终授权和清理证据

## 结论与实际边界

VERDICT: PASS（限本轮已修复的源码链与定向自动化；真实浏览器、SSH 部署密钥客户端及其他数据库驱动尚未验收）。P0/P1 BLOCKERS：无。

Release API 七个写入口在路由拒绝归档项目；服务层上传附件先保存正文，再在短治理事务中复查当前操作者、仓库、祖先归档及删除状态。创建、修改、删除 Release 和纯标签的普通数据库路径也在最终事务复查。生产环境另有 Git 引用事务路径：修复前 `planReleaseTagCreate/Delete` 只登记占用，没有复核发布者，Git 成功后直接补写并跳过普通路径检查；现在计划登记与当前权限检查同属一个治理事务。已有成功注册的引用按原恢复语义完成，不在 Git 已提交后重新撤权。纯 Git tag 沿用 Code 写权，完整 Release 沿用 Releases 写权；伪造标签删除 ID、以及标签转 Release 后的陈旧请求，在计划事务重读 `IsTag`、仓库和标签名后拒绝。

LFS 上传入口先检查当前仓库授权，正文传输和对象存储验证不占治理全局写锁；关联 metadata 前，在治理短事务复核当前 Code 写权、主体状态、祖先生命周期和部署密钥。SSH 签发的部署密钥 LFS JWT 现在绑定 `DeployKeyID`；每次读/写及最终关联复核当前部署密钥所属仓库、读写模式、公钥类型和仓库所有者。普通用户与 Actions 任务继续走各自原有权限链；旧 Owner 的 JWT 在仓库转移后不能沿用。

同 OID 的慢上传仍可能占内容锁，最终关联等待该锁最多 200 毫秒，超时返回冲突并回滚治理事务。不能直接反转为内容锁→治理锁：派生仓库的同一事务先注册仓库取得治理锁，后复制 LFS 取得内容锁，会形成反向死锁。失败的每次暂存都独立登记 24 小时后清理任务，即使对象是复用既有暂存；任务携带当次内容版本。后台取内容锁后，旧版本任务不增加版本也不删除；仅当前版本且全局 LFS metadata 引用为零时才增加版本并删除对象。后台对象存储删除不持治理全局锁；完成审计另在短治理事务执行。重复执行和相同内容后续暂存不会误删对象。24 小时是孤立对象清理宽限，并非替代内容版本与全局引用保护。

## 复现与验证

- 隔离副本使用本机 PostgreSQL `127.0.0.1:15433/gitea_phase1_test` 与 MinIO `127.0.0.1:19000`；测试口令只经进程环境传递，不记入文档。先前屏障红例：A 创建对象并在最终关联取治理锁，B 同 OID 读取正文时被屏障阻塞；旧实现 A 的即时清理抢先增加内容版本，导致 A、B 都返回 409、无 metadata。修复后 A 返回 409、B 返回 200；在 B 正文仍阻塞时，独立治理写入能在一秒内完成。测试还以 PostgreSQL `FOR UPDATE NOWAIT` 确认测量前 A 确实持有治理写锁。
- PG/MinIO 单进程定向命令：`GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 go test -p 1 -count=1 -run '^(TestLFSBothRejectedStagesEventuallyCleanOrphan|TestLFSSlowSameOIDUploadDoesNotHoldGovernanceWriteLock|TestLFSDelayedCleanupDoesNotDeleteRestagedContent|TestLFSDelayedCleanupDeletesUnreferencedContentOnce|TestLFSConcurrentSameObjectStagingIsNotCleanedByRejectedUpload|TestLFSSettingsDeletePreservesOtherRepositoryReference|TestArchivedGroupDeveloperCannotMutateReleaseAPI)$' ./tests/integration`：七项通过，`ok 5.335s`。实际命令还设置了测试库口令环境变量，证据故意省略其值。
- 两次同内容上传均在最终归档检查失败的固定例中，第一次创建对象、第二次复用对象；两条任务各绑定版本。旧任务跳过，最新任务清掉无引用对象。另有已关联对象保护、后续重新暂存保护、孤立对象一次清理与重复运行保护、Web LFS 删除跨仓共享对象保护。
- 本地单元与 SQLite：`go test ./models/governance ./services/lfs ./services/release ./services/attachment` 全部通过；六项对应 SQLite 集成测试通过。`go test -run '^$' ./cmd ./routers/api/v1/repo ./routers/web/repo/setting ./services/repository` 编译通过。部署密钥测试覆盖用户 token、组织 Owner token、读写 operation、错仓库、换 Owner、降只读、删密钥和改公钥类型；Release 单测覆盖计划时归档与陈旧 `IsTag`。
- `golangci-lint run --tests=false ./services/lfs/... ./services/attachment/...` 为 0 issues；扩大到 governance/release/repository 的 lint 仍报十三项此前文件的格式、modernize、revive 等存量问题，未在本包扩改。目标文件 `git diff --check` 通过。`services/repository` 全包单测有与本改动无关的失败：现有根目录 `gitea` 是 Linux ELF，macOS Git hook 执行得到 `Exec format error`；本轮未覆盖该二进制。

## 未验证与非阻塞项

MySQL/MSSQL 的数据库等待取消没有本轮实测；200 毫秒取锁上限已在目标 PostgreSQL 驱动验证。真实 SSH 部署密钥端到端和浏览器 Release 表单需父任务实测。LFS 对象存储预签名下载链接在撤权后的剩余有效期是已有边界，本轮未改变。若清理任务插入时数据库不可用，会记录错误，仍需既有存储孤儿检查/修复；不能把本轮定向通过当作生产可用性验收。
