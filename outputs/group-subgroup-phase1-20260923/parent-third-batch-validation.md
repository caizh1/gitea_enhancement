# 父任务第三批真实验收记录

日期：2026-09-23。均为本机隔离账号、仓库、凭据和接收端；不代表一期完成。

## 构建及隔离环境

- 初始第三批构建：`gitea-phase1-ancestors-v3`，SHA-256：`64c7d40c988f3a176d9b445dc7ac2814fae01a8652675e1b6b9b6dd7a15e77a8`。这个构建包含 Webhook 事件标识、基础分支保护和最后 Owner 修复；不含随后冻结的通知、协作候选和包权限最终修复。
- SQLite 通过正常迁移从 375 前进到 377；升级前使用 SQLite 备份 API 保存数据库和配置。原始根目录 Linux 二进制未修改。
- HTTP 仅监听 `127.0.0.1:13043`，新增内置 SSH 仅监听 `127.0.0.1:13044`；LFS 启用。SSH 客户端使用隔离主机公钥文件并严格校验，未关闭主机密钥校验。
- 发现多次候选可执行文件改名使验收仓库 `reference-transaction` 指向旧程序。当前安装器正确拒绝覆盖不同内容的入口。停止隔离实例，逐文件核实仅为之前三份测试构建生成的精确模板，备份十个入口后只改可执行路径；改用固定 `gitea-current` 入口。没有修改业务数据库来补配置，也未放宽陌生 Hook 检查。此项为验收工具运行路径修正。

## PostgreSQL HTTP 回归

环境为本机 Colima Linux arm64、PostgreSQL 17.11、MinIO，并非要求的 Linux amd64 性能环境。以下命令均附带隔离测试数据库环境变量和 `-tags='bindata sqlite sqlite_unlock_notify'`，清除本机代理环境变量。

- `go test -run '^(TestOrganizationWebhookTestDelivery|TestAPIOwnerTeamRenameConflict)$' -count=1 -v ./tests/integration`：通过，2.801 秒。最初副本未复制 Git 测试夹具而失败；保留首错日志，复制已存在的测试夹具后执行，未改断言。覆盖 Owner Team 改名 409、群组 Hook 测试的真实 HTTP 到达、重投保留事件标识且更换投递标识、停用后两入口 409。
- `go test -run '^TestAPINotificationRevokedUnit$' -count=1 -v ./tests/integration`：通过，2.506 秒。覆盖撤权后的 API 列表、单条 404、计数，以及 Web 通知页面和未读数。

原始运行记录：`parent-postgres-webhook-and-owner.raw.log`（夹具首错）、`parent-postgres-webhook-and-owner-fixtures.raw.log`、`parent-postgres-notification.raw.log`。

## 浏览器、Webhook 与真实协议

普通父组 Owner `phase1-owner` 不带实例管理员身份。

| 操作 | 实际证据 | 结论和边界 |
| --- | --- | --- |
| 子组 Owner 授予仓库 Maintainer | 在深层仓库协作者 UI 输入 `phase1-repo-admin`、选择 Maintainer，预览与确认后回显直接来源 | 管理入口正例，完整仓库管理员能力仍待验收 |
| 父组 Hook 测试按钮 | UI 测试生成投递 `3e3d61cf-5237-45a8-a4b7-6b832232ed04`；接收端真实 200、签名正确；合成仓库 ID 为 0 | 不读取下级仓库来生成测试载荷 |
| UI 重投 | 新投递 `0158b008-4495-4924-957c-a13c752fb269`；两次事件 ID 都为 `4650676c-cf93-4540-b2d0-d6f4db0a1785`，载荷摘要相同，均真实 200 | 投递尝试与事件幂等标识分离 |
| 两 Hook 同一地址 | UI 新建子组 Hook 2，与父组 Hook 1 均指向本机接收端；在深层仓库 Issue 1 评论后产生两个不同投递，签名均正确，载荷摘要相同 | 已验证按 Hook ID 去重，不按 URL 去重 |
| 顶级群组保护规则 | UI 设置 `phase1-protected/*`，推送无人、合并 Developer；保存后展开回显保留 | 当前规则支持星号匹配斜杠 |
| HTTP 与 SSH 拒绝保护分支 | 两个真实 `git push` 分别尝试 `phase1-protected/ui-denied`、`phase1-protected/ssh-denied`，均在 pre-receive 被拒绝 | 普通父组 Owner 也不能绕过此已配置推送规则 |
| SSH 正例 | `git ls-remote` 正确读取深层私有仓库；SSH 推送新提交 `66acb7adc7e803d1e81d4c3cfbe90b9bd449dd0e` 至普通分支成功 | 未据此声称 SSH 撤权矩阵已通过 |
| 新强制要求拒绝旧证据 | UI 保存父组 required 开关成功；PR 2 仅有旧配置/子组 Runner 的成功记录时，点击最终合并被服务端拒绝，PR 仍开放 | 页面错误地仍称可合并，此显示缺口已交子代理修复，不能计 UI 完成 |
| 父组可信 Runner 执行 | UI 注册父组 Runner 2；新提交生成 Run 19（push）和 Run 20（PR 同步），均由 Runner 2 执行成功；浏览器查看 Run 20／Job 23 成功及变量/凭据校验步骤 | 仓库无本级 YAML；父级统一工作流、变量和测试 Secret 真实生效 |
| 可信执行后合并 | UI 再次最终合并 PR 2 成功，刷新显示 Merged；合并提交 `48bdb35a02c7e50f9638142872ae8756befc4aed`；其后 push Run 21 成功 | 证明合法研发流程可以完成，不代表审批规则、fork 和全部门禁场景已过 |

接收端仅记录事件、投递 ID、仓库 ID、长度、摘要、签名结果和状态，不保存载荷、密钥或认证头。删除、移动中的真实投递交错仍待完成。

## 新增真实 Git、LFS 与共享成员检查

- UI 添加隔离 SSH 公钥后，HTTP／SSH 的正常分支写入与受保护分支拒绝分别实测，未通过关闭主机校验规避错误。
- Owner 通过 `git lfs install --local`、`git lfs track '*.lfs'`、提交及 HTTP push 上传 `sample.lfs`，文件 780000 字节，SHA-256 为 `bd152d359a51382b4e850d1ac6e5befc927d9ffb7ac98421feff4f316f23d0e1`。
- 祖先 Developer 单独克隆后启用仓库本地 LFS filter，`git lfs pull` 成功，实际下载大小及摘要一致。首次只得到 pointer 是客户端未安装 filter，定位后配置客户端，未修改服务器权限。
- Owner 从子组成员 UI 移除该 Developer 的唯一成员来源；下一次 HTTP `git ls-remote` 退出 128，LFS batch 请求返回 401。随后通过相同 UI 恢复角色。已经下载的文件仍在本地，不声称可追回。
- UI 将深层仓库 `ancestor-ci` 共享给独立根 `phase1-other`，上限 Reporter；共享成员通过真实 HTTP Git 读取成功、push 被拒。浏览器可查看共享仓库，访问邻近未共享的 `deep-lfs` 为 404。
- 发现独立根主页遗漏已共享仓库入口，已用真实 UI 固定缺口并交子代理补“共享项目”页签，源码测试通过；v3 尚无该页签，不能计为浏览器通过。

## 新增 PostgreSQL 可信门禁界面回归

`go test -tags='bindata sqlite sqlite_unlock_notify' -run '^TestActionsScopedWorkflows$' -count=1 -v ./tests/integration` 完整执行通过，118.597 秒。包括 HTML 就绪状态、同名普通成功状态、缺失真实 Run、当前配置／来源修订、禁用 Actions、无原生保护规则等正反例。原始记录为 `parent-postgres-readiness-ui.raw.log`。这是 HTTP 集成证据，最新诊断界面仍需浏览器验收。

## 未验证边界

第三批 UI 尚未装入最终通知、候选、包权限及门禁显示修复。目标性能环境暂未提供，保持未验证；千仓容量和人工活跃时间减少 80% 均没有合格证据。不得把本文件的正例写成全矩阵完成。
