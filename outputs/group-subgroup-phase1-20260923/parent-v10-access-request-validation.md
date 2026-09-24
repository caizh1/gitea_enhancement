# 第十版访问申请：父任务真实 UI 与 Git 验收

日期：2026-09-23 至 2026-09-24。仅使用本机隔离实例、合成身份和新建测试资源，没有生产数据或真实邀请。

## 修复与独立复核

第九版中，非成员打开可发现群组的首页及成员页，没有可用的访问申请入口；已存在的申请服务可以通过已知地址使用。第十版把“访问申请与状态”放入群组公共导航头，登录且有当前群组发现权的用户可达；原有设置页和成员页重复入口移除，匿名及仅有限路径导航不增加入口。

另一个已复现的问题是申请人列表宣称显示申请时路径，但群组移动及仓库转移却修改待处理申请的 `ScopePath`。资源转为私有后改名，会向已经无权的申请人披露新路径。现在保留申请时路径；管理者审批仍按当前 `ScopeID`、权限及修订，邀请路径仍单独随资源更新。父任务比对第九版副本和全部写入点，检查了申请人撤回与当前 Owner 审批消费者，没有另建申请状态机。

第十版 macOS arm64 开发二进制 SHA-256：`87f4b7d154356182460c601ffd8c014e2df90e011af64c80252599f26edbb8bd`。通过固定入口 `gitea-current` 启动，切换前确认无活动任务并备份 SQLite；正常迁移版本保持 377。仓库根目录原有 Linux 二进制 SHA-256 仍为 `10f50310cfe1cf1d878c6a78fae4601d79f264e6dfe478edbc16b34fb0c75d5a`。

## 实际浏览器操作

普通 Owner `phase1-owner` 不具备实例管理员身份；申请人是 `phase1-outsider`。用 UI 新建独立内部群组 15，初名 `phase1-request-sample`，并创建私有仓库 12 `request-private`。初始化提交为 `35c9015118ae2c7dfe668467e7034147ba089fba`，内容全部为验收样本。此群组没有祖先 CI、Runner 或 Hook。

1. 原始申请从已有页面创建后，通过个人菜单“我的访问申请”撤回，页面显示成功，无新增权限。
2. 再次申请后，Owner 把群组改为私有，并通过预览、确认把路径改为 `phase1-request-private`。申请人个人页面仍只显示原申请路径，旧地址得到原生 HTML 404，没有重定向披露新路径；没有当前资源读取权仍可撤回自己的申请。
3. Owner 将可见性恢复为内部，仓库继续私有。第十版群组首页有“访问申请与状态”，非成员看不到私有仓库。申请人从该入口于 00:14:30 提交申请 3；普通 Owner 从相同入口找到待处理项，选择 Reporter 并批准，刷新后待处理列表为空。
4. 申请人重新进入群组，能看到私有仓库；成员页显示 Reporter、直接来源和当前群组路径。真实 HTTP Git 新旧路径读取成功，实际 clone 成功；Reporter 推送新分支被服务端拒绝。
5. 申请人展开退出说明并确认退出直接授权，页面提示其他来源按自身范围生效。本样本没有其他来源；下一次新路径、旧别名 Git 请求均拒绝。
6. 00:15:49 重新提交申请 4，Owner 从 UI 拒绝。申请人“我的访问申请”刷新后没有待处理项，Git 仍拒绝。只读数据库佐证本样本待处理申请数及直接成员数均为 0。

群组当前名称中的 `private` 是路径文本，不代表当前可见性；测试结束时它是内部群组，仓库仍私有，申请人没有残留授权。上述操作没有手工写库。

## 真实协议结果

每次 Git 调用都显式清空 `credential.helper` 并指定合成申请人，避免系统凭据缓存误用 Owner。测试凭据由私有缓存 askpass 提供，不记录在本文。

| 状态 | 实际命令要点 | 结果 |
| --- | --- | --- |
| 批准 Reporter 后 | 对新路径及旧别名执行 `git ls-remote … HEAD` | 均退出 0，返回初始化提交 |
| 批准 Reporter 后 | `git clone` 新路径到独立缓存 | 退出 0 |
| Reporter 写入负例 | `git push origin HEAD:refs/heads/phase1-request-reporter` | 退出 1，服务端写权限拒绝，未创建分支 |
| 本人退出后 | 对新路径及旧别名执行 `git ls-remote … HEAD` | 均退出 128，仓库不可见 |
| 第二份申请被拒绝后 | `git ls-remote` 新路径 | 退出 128；申请及直接成员均为 0 |

原始客户端结果位于私有缓存 `/Users/archer/.cache/gitea-phase1-git-7n1x_l_6/` 的 `v10-request-approved-git.json`、`v10-request-clone-push.json`、`v10-request-revoked-git.json` 和 `v10-request-denied-git.json`。

## 自动化证据与边界

- 入口红例在旧模板失败；SQLite 两项入口／HTTP 定向测试最终通过，包耗时 2.187 秒。原始 JSONL 为私有缓存 `v10-access-entry-red.jsonl` 与 `v10-access-entry-final.jsonl`。
- PostgreSQL 实际执行 `go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json -run '^TestGovernance(GroupAccessRequestEntry|AccessRequestHTTP|AccessRequestAuditRollback)$' ./tests/integration`：3 个顶层测试通过，包耗时 5.73 秒。父任务重新解析原始 `/Users/archer/.cache/gitea-phase1-v5-validation/v10-pg-access.jsonl` 确认结果。使用的是 Linux arm64、2 CPU／4 GiB 的既有隔离 PG，不是目标容量环境。
- 私有后群组移动的旧逻辑红例失败、修复后五项服务定向测试通过，包耗时 2.026 秒；该次原始输出只保留在工具记录，没有声称存在独立日志文件。
- 仓库真实跨根转移回归 `TestTransferredPrivateRepositoryKeepsAccessRequestPath` 通过，包耗时 1.184 秒；旧逻辑临时 Go overlay 红例退出 1，当前绿例退出 0，见[红例](access-request-transfer-red.log)和[绿例](access-request-transfer-green.log)。该测试不是浏览器转移验收。
- 入口增量 lint 为 0；治理包另有九项既有或其他文件 lint 问题，不能将定向检查写成全仓 lint 通过。

本次通过的是内部群组申请、批准为 Reporter、拒绝、撤回、退出、私有后改名隐私和 HTTP Git 新旧地址正反例。SSH、Wiki、LFS、包协议的改名组合，申请到期、多来源及完整角色／并发矩阵仍未验证。目标 Linux amd64／8 vCPU／16 GiB 环境用户明确暂无；性能容量及人工节省 80% 均未验证，一期整体仍未完成。
