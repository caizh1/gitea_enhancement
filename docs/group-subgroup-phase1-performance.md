# 群组／子群组一期容量与性能状态

## 当前结论

**未验证，不能计入一期完成。** 本轮执行的是功能与安全回归，没有运行冻结的容量基准，也没有采集列表、首个可操作状态或轮询发现延迟的 P95。

用户已明确回复：暂未提供目标环境，目标环境性能保持未验证。本机功能开发和验收继续推进，不将环境缺口视为其他工作的停止条件。

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

在提供的 Linux 主机上先运行以下现成命令确认环境，记录原始输出、时间与候选版本；具体安装和负载命令须按实际交付物确定，当前仓库没有可复用的一期千仓基准脚本，不虚构运行入口：

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
