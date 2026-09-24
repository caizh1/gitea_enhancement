# 一期隔离验收工具

## 当前工作树候选构建

先按仓库标准方式安装锁定的前端依赖，指定实际Go工具链；下面的入口执行标准 `make frontend`、重新生成三类bindata、编译指定架构，并保存HEAD、每个源码文件SHA、工作树patch、工具链、命令、日志和产物摘要。输出目录必须全新，构建期间源码变化会拒绝把结果当作冻结候选。

```sh
python3 contrib/phase1-acceptance/build_candidate.py --go "$PHASE1_GO" --output "$PHASE1_CANDIDATE" --targets linux/amd64
```

若当前 `node_modules` 是已经安装完的移路径副本，且 `node_modules/.pnpm/lock.yaml` 与仓库锁文件逐字节一致，可显式加 `--reuse-installed`；脚本会验证锁文件后让pnpm告警而非自动清理目录。本轮首错就是缓存中的工作区仍指向旧路径，首次无TTY自动安装拒绝退出；原失败保留，未删除或重装用户依赖。这个选项不跳过前端编译，也不允许锁文件不同的依赖复用。

构建和压缩包准备不代替目标Linux安装或容量验收。禁止将测试凭据、Runner注册文件、数据库或真实生产配置放入候选包。

## 本轮其他实际验收入口

各脚本只面向显式指定的隔离实例；`--help` 给出参数，必须使用私有状态/凭据文件，原始结果不得含明文凭据。矩阵编号与步骤以唯一台账为准，不能从脚本存在推定通过。

| 入口 | 对应场景 |
| --- | --- |
| `artifact_v4.py` | 固定兼容客户端的实际v4上传、下载、删除与归档拒绝 |
| `ci_protected_probe.py` | 受保护、普通分支和fork凭据边界 |
| `job_token_fixture.py`、`job_token_boundary.py`、`job_token_fork.py`、`job_token_artifact_deny.py` | 同一真实Job撤权、Git/Generic及跨仓Actions拒绝 |
| `generic_package.py`、`oci_multitag.py` | 用户选定Generic，OCI多标签及共享摘要 |
| `transfer_probe.py`、`runner_disable_active.py` | 真实Runner屏障、转移与停用/删除 |
| `pr_gate_fixture.py`、`pr_gate_barrier.py` | 审批、必选运行及自动合并交错 |
| `team_delete.py`、`label_sources.py`、`audit_projection.py` | 非空Team、同名标签与转移历史、审计范围与脱敏 |
| `db_path_verify_test.go` | 四类数据库的完整长路径、大小写冲突与解析；实际环境变量见数据库证据文档 |

## PostgreSQL 定向复跑

在独立测试源码副本与专用可清空数据库中，按仓库 `tests/pgsql.ini.tmpl`、`Makefile` 和 `tools/test-integration.sh` 配好 `TEST_PGSQL_HOST`、`TEST_PGSQL_DBNAME`、`TEST_PGSQL_USERNAME`、`TEST_PGSQL_PASSWORD`、`TEST_PGSQL_SCHEMA` 及 `TEST_MINIO_ENDPOINT`。私有环境文件自行载入，不把凭据放入命令记录；测试入口会重建夹具，禁止指向生产或其他验收运行库。先构建副本内供 Git hooks 调用的二进制，再跑集成测试，例如：

```sh
export GITEA_TEST_DATABASE=pgsql
go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json \
  -run '^(TestActionsClaimAfterFormalLifecycleMutation|TestActionsTokenPackageMaxSettingsUI|TestScopedScheduleWithoutConsumerWorkflow|TestScopedScheduleSourceVersionCancelsOldRun|TestScopedScheduleReplacementPreservesOtherPlans|TestScopedScheduleSourceFailureDoesNotBlockPush|TestLastPermanentGroupOwnerConcurrentLeave|TestExpiredTemporaryOwnerCannotReplaceLastPermanentOwner)$' \
  ./tests/integration > "$PHASE1_PRIVATE_RESULTS/ci-owner.jsonl"
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json \
  -run '^(TestAPICreateHook|TestOrganizationWebhookTestDelivery|TestWebhookSendBarrierPostgres)$' \
  ./tests/integration > "$PHASE1_PRIVATE_RESULTS/hooks.jsonl"
```

`PHASE1_PRIVATE_RESULTS` 应预先建立且仅当前用户可读。完整已执行测试正则、原始日志 SHA 和结果见公开 `current-candidate-targeted-regression.json` 及专项证据；本机私有包装器不是复跑必需依赖。`models/unittest.MainTest` 的包级测试固定 SQLite，不能仅设置 `GITEA_TEST_DATABASE=pgsql` 就称为 PostgreSQL；需要 PostgreSQL 的测试必须走上述集成入口并核实引擎。不得把 skip 当作通过。

## 容量夹具与采集

`capacity_probe.py` 仅向明确指定的隔离实例写入带唯一前缀的测试资源。默认建立一棵 20 层群组树，在其根群组授予 1,000 名独立成员，并在树中分布 1,000 个初始化仓库。额外建立一个接收共享的独立群组，给前 100 个仓库各加一条真实项目共享；另建立 100 条有正文的 Issue、50 条含真实分支及文件差异的 PR，以及 50 条由工作流文件和 push 产生的 Actions Run／Job。脚本不创建 Runner，不删除资源。状态文件逐步落盘，可使用相同参数续跑；前缀和私有状态文件应一轮一组，避免与其他验收数据混合。

先确认目标环境确实是隔离的 Linux amd64、8 vCPU、16 GiB、SSD、单应用节点和 PostgreSQL，并记录 `uname -m`、`nproc`、`free -h`、`lsblk -o NAME,TYPE,SIZE,ROTA,MOUNTPOINT`、PostgreSQL 版本、Gitea／Runner 二进制 SHA-256、部署配置与启动时间。目标环境未提供时，不能用本机或交叉编译结果填写容量结论。`PHASE1_ADMIN_USER`、`PHASE1_ADMIN_PASSWORD`、`PHASE1_MEMBER_SEED` 由专用账号的私有环境传入；后者用于确定性生成 1,000 个不同用户的口令，必须跨续跑保持不变。不要把这三个值写入仓库、终端历史或原始采集文件。以下命令中的路径和地址需改为该隔离主机的真实值：

```sh
umask 077
export PERF_WORK=/var/tmp/phase1-capacity
mkdir -p "$PERF_WORK"
python3 contrib/phase1-acceptance/capacity_probe.py self-test
python3 contrib/phase1-acceptance/capacity_probe.py seed \
  --base-url http://127.0.0.1:3000 --state "$PERF_WORK/state.json" --prefix p1cap01
python3 contrib/phase1-acceptance/capacity_probe.py measure \
  --base-url http://127.0.0.1:3000 --state "$PERF_WORK/state.json" --prefix p1cap01 \
  --concurrency 50 --repeats 5 --candidate-binary /opt/gitea/gitea \
  --output "$PERF_WORK/requests-01.jsonl"
```

`measure` 在启动屏障后由 50 个**不同成员身份**并发发起用户仓库列表、深层群组仓库列表、深层仓库资料、实际含内容的 Issue 列表和 PR 列表请求；第六类审批有效配置请求使用同一个管理账号，摘要明确区分。逐请求记录时间、HTTP 状态、耗时和字节数，并输出同名 `.summary.json`。每类入口分别检查零失败及成功请求 P95 不超过 1,000 毫秒；先校验固定数据、可见仓库全集和列表中实际存在的代表条目。默认 Issue／PR 分布于不同仓库，每仓一个条目，这一数据分布必须随结果披露；不能据此宣称大单仓 Issue 列表性能已通过。自定义小样本的所有命令须重复同一组规模参数，否则拒绝复用状态文件。

启动屏障只同步请求发起，不声称服务器内部交错。首次可操作页面的 2 秒指标需在目标浏览器另记导航、资源和交互就绪时间；长操作 2 秒内返回或可跟踪任务、授权变化的下一次服务端检查也须另附原始证据，不能从本脚本的 API P95 推断。

10 Runner 是**单独的**实测。先执行 `prepare-runners` 准备批次工作流，并等待其提交触发的其他工作流结束。随后记录十个 Runner 的注册身份、标签 `ubuntu-latest`、每个容量 1、空闲状态和实际轮询周期；确认没有其他任务抢占。保存就绪证据后，`trigger-runners` 仅使用正式手动触发接口并行创建十个新 Run，不再提交文件，避免污染空闲条件。每轮使用新的批次编号，不能用样本生成阶段的旧任务计算发现延迟。脚本只核对就绪文件时间与哈希，操作者仍须提供真实的运行状态证据。

`observe-runners` 读取本批次真实 Run ID，轮询 Job API，记录服务端创建／开始时间、Runner ID、状态和观察时间；要求全部任务在两个配置轮询周期内开始、至少十个不同 Runner 领取、观察期间同一 Job 未出现多个 Runner ID。原始记录不能单独证明没有瞬时重复领取、数据库死锁或长期饥饿，需结合 Runner／服务端日志及长时段监控。示例：

```sh
python3 contrib/phase1-acceptance/capacity_probe.py prepare-runners \
  --base-url http://127.0.0.1:3000 --state "$PERF_WORK/state.json" --prefix p1cap01 \
  --batch-size 10
# 等待任务排空，在 runner-readiness.md 中记录十个 Runner 的真实就绪状态。
python3 contrib/phase1-acceptance/capacity_probe.py trigger-runners \
  --base-url http://127.0.0.1:3000 --state "$PERF_WORK/state.json" --prefix p1cap01 \
  --batch-id run01 --batch-size 10 --expected-runners 10 \
  --runner-readiness "$PERF_WORK/runner-readiness.md"
python3 contrib/phase1-acceptance/capacity_probe.py observe-runners \
  --base-url http://127.0.0.1:3000 --state "$PERF_WORK/state.json" --prefix p1cap01 \
  --batch-id run01 --batch-size 10 \
  --expected-runners 10 --poll-seconds 2 \
  --runner-readiness "$PERF_WORK/runner-readiness.md" \
  --output "$PERF_WORK/runner-observations-01.jsonl"
```

原始输出和独立摘要均使用新英文文件名，保留首次失败与后续修正。任何本机小样本、Linux arm64 功能检查、Linux amd64 交叉编译，均不能替代目标规格下的 50 并发与 10 Runner 验收。
