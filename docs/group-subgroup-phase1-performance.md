# 群组／子群组一期容量与性能状态

> 2026-09-24 用户范围修订：本文件保留专项目标、已有准备和真实未验证状态。容量/性能或80%真人效率专项不再阻塞本次日常可用版开发收口；不将准备、自动化耗时或估算值当作专项通过。完成判定以[现行计划](group-subgroup-phase1-plan.md)为准。

## 当前结论

**未验证，不能计入一期完成。** 本轮执行的是功能与安全回归，没有运行冻结的容量基准，也没有采集列表、首个可操作状态或轮询发现延迟的 P95。

用户已明确回复：暂未提供目标环境，目标环境性能保持未验证。本机功能开发和验收继续推进，不将环境缺口视为其他工作的停止条件。

## 本轮准备结果与外部输入拆分

数据生成、采集和候选准备已经完成，不能再统一记为“缺少主机所以尚未准备”。[可复跑入口](../contrib/phase1-acceptance/README.md)包含千成员／千仓／20层／百共享、代表性 Issue／PR／任务、50独立身份并发、10 Runner 新批次触发和两轮询周期观察。Linux arm64 小样本实际24次请求全部200；无 Runner 的负例按预期非零退出，未伪造发现时间。[准备验收](../outputs/group-subgroup-phase1-20260923/capacity-preparation-acceptance.md)、[原始摘要](../outputs/group-subgroup-phase1-20260923/capacity-preparation-results.json)。小样本 P95 只用于检查采集程序，不能计为目标容量达标。

最新 Linux amd64 候选包、配套 Runner、源码清单及 SHA 已生成，见[构建说明](group-subgroup-phase1-release-notes.md)。构建源为 HEAD `7dbdfb23570c4e164061e48707837f5e719d0985` 加源码指纹 `2fd0a1e11ea46698f8ac6bb7d348dcff9cfddc71e20bb01e30ed547ee2b4e9cb` 对应工作树。它是交叉构建准备，尚未在目标机器安装。

现有2核4GiB Linux arm64 专用实例已用 F2 业务源码正常升级 PostgreSQL schema378→379，健康/会话登录/静态资源通过，五个包文件路径和摘要不变。[原地升级及备份证据](../outputs/group-subgroup-phase1-20260923/linux-arm64-f2-upgrade-acceptance.md)。此结果不关闭 S15-a、PERF-ENV-01 或 MSSQL 门槛。

| 外部输入 | 影响编号／门槛 | 已准备内容 | 环境就绪后的入口 |
| --- | --- | --- | --- |
| 用户提供隔离 Linux amd64、8 vCPU、16 GiB、SSD、单应用节点和 PostgreSQL 的连接与用途 | PERF-ENV-01、S15-a，目标安装／容量／候选最终验收 | 候选包、建数、采集、10 Runner 观察与安装说明 | 按包内 README 正常 migrate、admin user create、启动；随后执行 contrib README 的 seed／measure／prepare-runners／trigger-runners／observe-runners，另采页面和长操作指标 |
| 支持 MSSQL2019 的原生 x86-64 Linux 容器主机 | DB-ENV-01，数据库完整兼容门槛 | 四库已验证；MSSQL 的 SQL／迁移路径保留，未使用不支持的 QEMU 结果冒充 | 在该主机运行仓库 tests/mssql.ini.tmpl 及 .github/workflows/pull-db-tests.yml 的既有集成入口及本轮 db_path_verify_test.go，正常空库与迁移样本分别记录 |
| 真实参与者执行冻结十仓任务集 | EFF-MEASURE-01 | [任务、口径及记录模板](group-subgroup-phase1-efficiency.md) | 同一参与者记录逐仓与群组流程的活跃时间、重复动作、核对和异常处理，两个指标分别计算80% |

A02-a 的协议输入已由用户明确为 Generic，实际验收已关闭；它不再是外部阻塞，也不归类为缺少 Linux 主机。

## 已实际使用的环境

- 应用与浏览器：macOS arm64 开发构建，本机 loopback 13043，单应用进程；UI 数据库为全新 SQLite。
- 数据库并发回归：独立 Colima `gitea-phase1`，Linux arm64，2 CPU、4 GiB，PostgreSQL 17.11；独立 MinIO 仅供产物测试。
- 真实 Runner：仓库锁定的 runner v1.0.8 模块源码构建，`--version` 显示 `dev`；两个 Runner，分别注册在父群组和子群组，各自容量 1、轮询 2 秒、关闭缓存，执行本机测试工作流。
- UI 样本现有两个独立顶级群组、兄弟群组、三层子群组（根为第 1 层，最深为第 4 层）及十个仓库；这些资源由普通 Owner／子级 Owner 在真实浏览器创建。此功能样本不等于千仓容量样本，尚未采集目标性能指标。

## 冻结目标与缺口

目标为 Linux amd64、8 vCPU、16 GiB、SSD、单应用节点、PostgreSQL；1,000 成员、1,000 仓库、20 层、100 共享、50 并发用户、10 Runner。Issue／PR／任务必须有代表性内容并记录数据量。

| 指标 | 目标 | 本轮证据 |
| --- | --- | --- |
| 常用列表／有效配置 | P95 ≤ 1 秒 | 未采集 |
| 首个可操作页面 | P95 ≤ 2 秒 | 未采集，不能用工具截图等待时间代替 |
| 长操作响应 | 2 秒内完成或可跟踪任务 | 未采集 |
| 合格 Runner 发现任务 | 两个正常轮询周期内 | 真实任务执行成功，但未做固定时间统计 |
| 撤权后的服务端检查 | 下一次使用当前权限 | 指定安全回归通过；不等于容量条件下全部通过 |
| 并发可靠性 | 无重复领取、死锁、越权、丢数据或持续饥饿 | 固定小样本并发通过；目标负载未验证 |

PostgreSQL 合并回归耗时 250.849 秒、最终 Scoped 回归 81.537 秒是**测试套件耗时**，不是任何业务页面或请求的性能指标。后续先准备目标环境和数据，再记录基线、定位查询／锁等待并仅做必要优化。

## 2026-09-24 外部条件只读核查

本节只记录现有资源，不代表新一轮容量测试或 Linux 安装验收。外部容量环境缺口编号为 **PERF-ENV-01**，与一期操作矩阵一致。

| 实际核查命令 | 观察结果 | 可用范围 |
| --- | --- | --- |
| `uname -sm`、`sysctl -n hw.ncpu hw.memsize` | 宿主为 Darwin arm64，18 CPU、48 GiB | 可继续本机开发与功能回归；宿主资源不能替代 Linux 目标测量 |
| `docker version --format '{{.Client.Version}}|{{.Server.Version}}|{{.Server.Os}}|{{.Server.Arch}}'` | 客户端 29.5.3，默认 Docker socket 无法连接 | 使用既有隔离环境需显式指定 context |
| `colima list -j` | `gitea-phase1` 运行中，Linux aarch64、2 CPU／4 GiB；`g9-amd64` 已存在但停止，QEMU x86_64、4 CPU／8 GiB；`default` 停止 | 现有 amd64 虚拟机规格不足且本轮没有启动；不能计为目标机器 |
| `docker --context colima-gitea-phase1 info --format '{{.OSType}}|{{.Architecture}}|{{.NCPU}}|{{.MemTotal}}'`、`docker --context colima-gitea-phase1 ps --format '{{.Names}}|{{.Image}}|{{.Status}}'` | Linux aarch64、2 CPU／约 4 GiB；仅 `gitea-phase1-pg` 与 `gitea-phase1-minio` 在运行 | 可做已授权的本机隔离 PostgreSQL／MinIO 功能验证，不是 amd64 安装或容量验收 |
| `docker --context colima-gitea-phase1 exec gitea-phase1-pg postgres --version`、`docker --context colima-gitea-phase1 exec gitea-phase1-pg pg_isready -h 127.0.0.1 -p 5432` | PostgreSQL 17.11，容器内 5432 接受连接；既有映射为宿主 15433 | 数据库服务可用；没有为本次测量写入样本 |
| `file gitea /Users/archer/.cache/gitea-phase1-ui-iegohg9v/gitea-current outputs/group-subgroup-phase1-20260923/gitea-runner`、`lsof -nP -iTCP:13043 -sTCP:LISTEN` | 仓库根旧 `gitea` 为 Linux x86-64 ELF；当前 13043 应用监听中，阶段 Gitea 与 Runner 均为 macOS arm64 Mach-O | 旧 ELF 不是当前一期候选；当前阶段产物不能在 Linux amd64 直接安装 |
| `rg --files .github/workflows`、`command -v gh` | 仓库有 PostgreSQL、E2E 和 Docker amd64 工作流定义；本机没有 `gh` 命令 | 仅确认配置存在，未核实 CI 实际 Runner、任务结果或远端 Linux 机器 |

现有隔离功能样本与 1,000 仓／50 并发／10 Runner 的容量样本不同。此节只读核查时没有启动、停止或改配容器／虚拟机，也没有向 CI 提交任务；后续独立 Linux arm64 首装另见下节。Linux amd64 安装、目标规格性能及其 P95 仍是 **PERF-ENV-01 未验证**。

## 环境到位后的执行入口

基础设施负责人需提供可访问的**隔离非生产** Linux amd64、8 vCPU、16 GiB、SSD、单应用节点及 PostgreSQL 环境，并说明存储和网络约束；开发负责人需提供与当前改动对应的 Linux amd64 Gitea／Runner 候选及安装方式。现有 `g9-amd64` 即便获准启动，也只能用于较低规格的兼容安装检查，不能替代前述性能环境。两项输入到位前不填容量测量值。

在提供的 Linux 主机上先运行以下现成命令确认环境，记录原始输出、时间与候选版本；本轮新增的固定夹具与采集入口见文末，目标环境未提供，尚未运行完整基准：

```sh
uname -m
nproc
free -h
lsblk -o NAME,TYPE,SIZE,ROTA,MOUNTPOINT
docker version
docker compose version
```

随后按[冻结计划](group-subgroup-phase1-plan.md)建立并登记 1,000 成员、1,000 仓、20 层、100 共享、50 并发用户和 10 Runner 的代表性数据，另记 Issue／PR／任务数量。对固定入口采集逐次请求和首个可操作状态的原始时间、成功／失败、并发和 P95；长操作记录可跟踪任务返回时间，Runner 记录任务入队与发现时间及实际轮询周期，同时检查重复领取、死锁、越权、丢数据和饥饿。只有目标环境和完整原始记录可用于一期容量判定。

## 2026-09-24 隔离 Linux arm64 首装功能实测

本节补充本机已有 `gitea-phase1` Colima 的**功能兼容证据**，不改变 **PERF-ENV-01** 的未验证结论。该虚拟机为 Linux arm64、2 CPU、4 GiB，既不是目标 Linux amd64，也不足以承载冻结的容量样本。

- 从 `a488feb7a4` 加当时一期业务差异，以 Go 1.26.4 执行 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOEXPERIMENT=jsonv2 GOPROXY=off GOSUMDB=off go build -tags 'bindata sqlite sqlite_unlock_notify'`，得到静态 ELF arm64 候选，SHA-256 `7a9dd699b4919dab4d3001e0d15737d652ff6aeead5c2cf33e019427e3265e58`；容器内 `--version` 正常返回。源码差异与内嵌资源的逐项哈希见下列私有原始记录。本轮 Linux 构建与 macOS 候选 `9f77cd8313062b0cf34c9758d492db830041659719c8b9df2a18b78480ceac09` 是不同架构的产物，不能用二进制哈希证明其等同。
- 只使用已有 `postgres:17` 基础镜像，构建带 Git 和证书的独立 Linux arm64 应用镜像；另建专用网络、PostgreSQL 容器与卷，数据库名为 `gitea_phase1_linux_functional`，没有复用现有测试库、UI 实例或 Runner。数据库首次启动前为 0 表，按正常迁移启动后 schema 版本 378、公共 schema 150 表，用户数仍为 0。
- 首装 HTTP 应用容器 `gitea-phase1-linux-functional-app-http` 曾在独立回环端口 `127.0.0.1:13144` 运行。宿主发起的 `GET /api/healthz` 为 HTTP 200、`status=pass` 且数据库 ping 成功；`GET /user/login` 为 HTTP 200 且出现登录表单；该页面引用的 CSS 资源为 HTTP 200。
- 首次启动曾因新配置文件属主错误导致 `/data/app.ini: permission denied`，修正为容器内 `postgres` 属主和 0600 权限后迁移成功；初选 13044 端口又与既有 macOS 应用冲突，改为独立空闲端口 13144。首个失败容器已停止并保留；首装 HTTP 应用容器在后续最终候选替换时正常停止并保留。两个首因及修正均记录在[私有首装记录](/Users/archer/.cache/gitea-phase1-closure-20260924/linux-functional/validation.md)，不含凭据。

这次仅完成**预配置首次安装、数据库迁移、HTTP 健康与登录页**。未创建账户或实测登录，也未验证 Git push、Runner、祖先 schedule、升级、长期运行、Linux amd64 安装或任一容量 P95；不得把本轮 Linux arm64 功能通过算作 PERF-ENV-01 完成。

## 上一轮源码候选 Linux arm64 替换启动复验

父任务冻结该轮源码指纹 `a35d1cb539597c3967786cc3d0acf05dce2819d0f2986674469fa937bcfa8031`、macOS 候选 SHA-256 `e08c83b35ffbcbf3c474072dbbdb746ac5887653bbb74b234a5cd1e12b8ae135` 后，本机逐项核对该轮源码清单：所列 **6,644 个文件的实际 SHA-256 均一致**。另行交叉编译 Linux arm64 静态候选 `gitea-linux-arm64-final`，SHA-256 `dbcab5a8bedc3bd24437afd5b4382aa087bbfb93a885344998e53ae2852e0f5a`，没有覆盖首装二进制。只在已构建应用镜像上替换可执行文件，得到该轮独立镜像 ID `sha256:e12b2acf22f91a644e2690c425b139fb90f9b3a96d641f1965a335e05ea68ed1`；容器内 `--version` 正常。

停止并保留首装 HTTP 应用容器后，该轮应用容器 `gitea-phase1-linux-functional-app-final` 沿用**本任务专用** PostgreSQL 库、网络和配置，曾在 `127.0.0.1:13144` 正常运行。宿主实测 `/api/healthz`、`/user/login`、登录页 CSS 均为 HTTP 200；健康结果 `status=pass`，数据库、缓存与治理审计持久化检查均为 pass，登录表单含用户名和密码字段。数据库迁移版本仍为 378、公共表数 150、用户数 0。此处是**已有专用库上的替换启动**，首次空库迁移证据仍属于首装候选；该轮容器后续已正常停止并保留。两份结果与容器保留状态见[私有首装与替换记录](/Users/archer/.cache/gitea-phase1-closure-20260924/linux-functional/validation.md)。未清理既有容器或卷，也未触碰既有 PG 测试库、UI 和 Runner。

该轮 Linux arm64 候选未实测真实登录、Git push、Runner、祖先 schedule、Linux amd64 安装及容量 P95。**PERF-ENV-01 保持未验证**；2 CPU／4 GiB 的本机 arm64 隔离环境不能替代目标 8 vCPU／16 GiB 的 amd64 环境。

## 71d19b 不可变候选 Linux arm64 替换启动复验

父任务冻结本轮[源码清单](/Users/archer/.cache/gitea-phase1-closure-20260924/source-manifest.json)，指纹 `1b485408ab940a5693a89dfd29581f6e5e73d355344ccf11a83a0f6702f9c547`，macOS 候选 SHA-256 `71d19b0d1687ac1e71ccbd5aaec8f79e7c3e5481d8ea749d15377afd360c5ed0`。构建前逐项校验清单 6,644 个文件，哈希不一致数为 0。单独构建 Linux arm64 静态二进制 `gitea-linux-arm64-71d19b`，SHA-256 `61f8dfa74e91771f7d98ff9526c87367d750c9dbf0e73a5ddd44ed359881d2f7`；派生的独立应用镜像 ID 为 `sha256:fea2b8f7fc86dd4e56235d721e5169c73d0884e736fa13fb4f3a2b2c82c8480d`。首次首装和上一轮替换候选均保留，三轮产物不混称。

仅停止并保留本任务上一轮应用容器后，新容器 `gitea-phase1-linux-functional-app-71d19b` 沿用本任务专用 PostgreSQL 库、网络及 `127.0.0.1:13144`，正常启动并保持运行。宿主实测健康接口、登录页和页面 CSS 均为 HTTP 200；健康 `status=pass`，数据库、缓存和治理审计持久化检查均通过，登录表单有用户名、密码字段。专用 PostgreSQL 容器健康，schema 版本 378、公共表数 150、用户数 0。此处验证的是**已有专用库再次替换启动**，不声称新候选完成空库首装。完整指纹与容器状态见[私有记录](/Users/archer/.cache/gitea-phase1-closure-20260924/linux-functional/validation.md)。未清理既有容器或卷，既有 PG 测试库、macOS UI、Runner 均未触动。

本轮仍未实测真实登录、Linux Git／Runner／祖先 schedule、Linux amd64 安装或目标容量 P95；**PERF-ENV-01 保持未验证**。

## 7dbdfb2357 容量准备与隔离功能补验

本节对应提交 `7dbdfb23570c4e164061e48707837f5e719d0985`。构建时 `git status --porcelain --untracked-files=no` 无已跟踪源码差异；Go 元数据仍将含新增未跟踪验收脚本的工作树标为 `+dirty`，因此以提交、命令、文件哈希和下列边界标识候选，**不是正式发布包**。本机只有 Linux arm64、2 CPU／4 GiB 的专用 Colima 实例；Linux amd64、8 vCPU／16 GiB／SSD／单应用／PostgreSQL 目标主机仍未提供。

- 用 Go 1.26.4 从当前提交执行 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=jsonv2 GOPROXY=off GOSUMDB=off go build -mod=readonly -tags 'bindata sqlite sqlite_unlock_notify' -o <私有输出路径> .`，交叉编译得到静态 ELF x86-64 Gitea 候选，SHA-256 `a39635e33e3b26e9d21e2108d0bd904ae96aefcd9a2ee5dbe034c0e9ce5e2bf1`。二进制保存在 `/Users/archer/.cache/gitea-phase1-closure-20260924/perf-env-01/gitea-linux-amd64-7dbdfb`；未在 Linux amd64 执行、安装或迁移。
- Runner 从本机 Go 模块缓存中仓库锁定的 `gitea.com/gitea/runner@v1.0.8` 源码执行 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go build -mod=readonly -o <私有输出路径> .`，得到静态 ELF x86-64 候选，SHA-256 `c0eec0f69e6a3c4bbd12639aa166557d12f4dea2b25120cd2a82ef1a506248fa`。首次离线构建因缺 `github.com/prometheus/procfs@v0.17.0`、`github.com/moby/sys/userns@v0.1.0` 失败；只补齐这两个锁定模块到本机 Go 缓存，保留首次失败日志后重新离线构建成功。模块源码构建的 Runner 版本展示不能冒称官方发布包，尚未在目标主机注册运行。
- 新增可复跑的[容量夹具和采集入口](../contrib/phase1-acceptance/README.md)、[`capacity_probe.py`](../contrib/phase1-acceptance/capacity_probe.py)。默认固定单根 1,000 成员、1,000 仓、20 层；另有接收共享的独立群组和 100 条项目共享；代表性内容固定为 100 Issue、50 个含分支与差异的 PR、50 个真实 push 工作流 Run／Job。`seed` 逐项保存无口令状态并可用相同参数续跑；`measure` 用 50 个不同身份并发、逐次原始记录与各入口 P95／失败数；`observe-runners` 单独记录 10 Runner、两轮询周期和 Job 分配变化。后两者的结果不能互代，页面首个可操作状态仍须浏览器单独测量。
- 在**本任务原有专用** Linux arm64／PostgreSQL 17.11 实例上，使用 71d19b 候选先执行真实 `gitea admin user create --config /data/app.ini`，创建专用管理员；再用 HTTP 表单完成登录，带会话 Cookie 访问 `/user/settings` 为 200 且页面含账号。首次探针误访不存在的 `/user/settings/profile` 得到 404；服务端日志显示表单 POST 已返回 303、重定向首页 200，确认是探针路径错误而非登录失败，首因保留。随后通过专用私有仓库完成 HTTP Git clone、commit、push 和 `ls-remote`，本地及远端 `main` 均为 `6e38fc6ec253e3d535a33e8438328bedadb1b619`。此为 Linux arm64 功能补验，不是 amd64 安装或容量测试。
- 脚本纯函数与 Python 语法自测通过；同一专用实例真实生成 2 成员、2 仓、2 层、1 共享、1 Issue、1 PR，先核对首名成员实际可见两仓，再由两个不同身份各两轮采集共 12 次 API 请求，均返回 200。最后一轮由脚本对实际 Linux arm64 二进制重新计算 SHA-256，仓库列表／群组配置／项目资料各 4 次的本机 P95 分别为 56.411／17.955／20.223 毫秒，仅用于确认采集入口可工作。另建 20 层、每层一仓的边界小样本，最深群组为第 20 层，根成员实际可见全部 20 仓；一名成员每入口一次请求不构成有效 P95。另建两层各两仓、各两条共享／Issue／PR／Run／Job 的嵌套内容样本，证明兼容名下深层仓库可创建这些资源。首次单仓任务样本因脚本误把运行列表的 `path` 当作 `workflow_id` 而报告未观察到 Run，实际接口已有 Run；改用真实 `path` 字段后续跑成功，记录 Run ID 2，再次同参数执行前后 Run 总数均为 2，未重复触发。嵌套样本又暴露 Actions Run 异步入队，脚本立即查询时尚未出现；按触发提交 SHA 限时等待真实 Run 和 Job 后续跑成功。两次首次失败和修正都保留，不能将小样本填入目标 P95 或 10 Runner 栏。
- 对两条真实排队 Job 的 Runner 采集入口做了 1.2 秒**负向自测**：专用实例没有注册 Runner，结果为发现 0／2、Runner ID 空、P95 空、退出 1，原始轮询记录和摘要均保留。这个失败是如实反映未具备 Runner 条件，不是两轮询周期验收。完整 10 Runner 的注册／资格／空闲证据、50 并发目标规格实测、页面首可操作状态及长操作仍待目标环境执行。

完整私有证据位于 `/Users/archer/.cache/gitea-phase1-closure-20260924/perf-env-01/`（交叉编译和首次失败日志、候选二进制、两组隔离小样本状态、逐请求 JSONL 与摘要）；Linux CLI／表单／Git 证据位于同级 `linux-functional/` 的 `admin-create.log`、`login-first-failure.md`、`login-check.json`、`git-probe.json`。状态和凭据均为专用私有文件，仓库文档与日志不写入明文密码。

目标主机到位后须先创建**独立非生产** PostgreSQL 库和配置，核对候选哈希及系统规格，再按现有 CLI 语法执行 `gitea-linux-amd64-7dbdfb --version`、`gitea-linux-amd64-7dbdfb migrate --config <专用配置>`、`gitea-linux-amd64-7dbdfb admin user create --config <专用配置> ...`、`gitea-linux-amd64-7dbdfb web --config <专用配置>`；随后实测 `/api/healthz`、表单登录与 HTTP Git。Runner 候选须在目标主机检查 `--version`、注册十个具备相应标签与容量的实例，并保存实际轮询配置。完整 50 并发与 10 Runner 命令见上述使用说明，原始结果、二进制摘要、环境资源、Runner 就绪时间、失败／重试和浏览器首可操作时间必须同轮记录。**PERF-ENV-01 仍未验证**；跨架构构建、arm64 功能及 12 次请求都不满足目标规格的容量关闭条件。

## 路径索引数据库兼容与容量入口后续证据

上述 `7dbdfb` Linux amd64 交叉编译包生成于路径索引修复**之前**，不能用于最终安装验收。MySQL 8.4.5 旧完整路径索引在真实初始化时首先报 1071（键超过 3072 字节）；保留原始失败后，以完整路径 SHA-256 短唯一索引修复，未截断路径。随后隔离 MySQL 8.4.5／MariaDB 11.4.13 全新库、PostgreSQL 17.11／SQLite 独立旧 v378 库均完成当前源码的 CLI v379 迁移、Web ORM 初始化及健康／登录页 HTTP 200；四库分别做了长路径实际解析和写入，MySQL 与 MariaDB 的治理／Runner 各九项定向集成通过。旧 PostgreSQL 首轮还暴露 2BP01 约束删除失败，现仅在哈希索引建立后安全处理旧单列约束，完整失败与成功记录见[数据库兼容实测](../outputs/group-subgroup-phase1-20260923/db-compatibility-acceptance.md)及[机器可读结果](../outputs/group-subgroup-phase1-20260923/db-compatibility-results.json)。MSSQL 2019 仍缺原生 x86-64 Linux 容器主机，不把现有 QEMU 模拟环境或其他数据库结果计为其通过。这是本机隔离兼容验证，仍需新的不可变候选；未将 Mac 运行或 Linux arm64 容器数据库冒充目标 amd64 安装。

容量入口经根任务复核并补齐实际深层列表／有效配置采集、仓库代表性选择和 Runner 就绪后新批次触发，最新小样本与真实无 Runner 负例见[容量准备验收](../outputs/group-subgroup-phase1-20260923/capacity-preparation-acceptance.md)和[原始结果摘要](../outputs/group-subgroup-phase1-20260923/capacity-preparation-results.json)。这些结果只证明采集入口可执行；没有 8 vCPU／16 GiB Linux amd64 主机上的 1,000 成员、1,000 仓、50 并发和 10 Runner 实测，**PERF-ENV-01 继续未验证**。
