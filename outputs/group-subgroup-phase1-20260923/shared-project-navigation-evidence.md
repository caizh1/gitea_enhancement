# I-06 共享项目导航证据

时间：2026-09-23。本记录是本机源码与定向测试证据，真实浏览器页面由父任务在 v4 副本验收。

## 根因与修复

群组主导航原有“子群组与仓库”只列本树资源；Issue/PR 页面虽可选“共享给本群组的项目”，但没有共享项目自身的列表或入口。因此用户能通过 CLI 读取已共享仓库，却无法从目标群组主页定位它。

在同一个群组主路由增加“共享项目”页签。候选沿用现有 `groupWorkRepositories` 的直接仓库共享与到期过滤；每项继续用 `GetDoerRepoPermission` 核对当前代码读取能力，再排除目标群组本树仓库。列表与计数在最终权限过滤后分页。已归档但仍可读的共享项目保留定位入口；项目始终链接原归属，未写入目标树，也不作为配置继承来源。

## 已执行

- 红例：新增测试先运行，因 `ListGroupSharedRepositories` 尚不存在而编译失败，确认测试覆盖新消费者。
- 定向测试：`/Users/archer/.cache/gitea-governance/go/bin/go test -run '^TestListGroupSharedRepositoriesFiltersBeforePagination$|^TestGroupIssuesFilterScopeBeforeCountsAndPagination$' ./services/governance/ ./routers/web/governance/`，通过；治理服务 1.725 秒，Web 治理包无测试文件。覆盖直接共享、实际代码读权、无权用户、本树去重、分页计数、撤销、到期、成员降至 Guest 和归档项目定位。
- 增量 lint：`PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./services/governance ./routers/web/governance`，`0 issues`。
- `git diff --check`，通过。

## 对抗式自审

VERDICT: PASS

P0/P1 BLOCKERS：无。逐项反证了共享关系候选绕过仓库读权、过期关系继续显示、低权限成员凭关联读取代码、同树仓库被错误归为外部共享、未授权项目进入计数或分页，以及列表链接改变仓库归属。候选和每项最终权限均在每次请求重算。

UNVERIFIED RISKS：未重新生成模板嵌入资源，未在真实浏览器与 v4 实例点击验证；大型群组共享数量下逐仓过滤成本尚未做目标规模测量。真实页面和性能需要父任务验收。

NON-BLOCKING FINDINGS：入口列直接共享给当前群组的项目；共享给其子组的项目在对应子组页面查找。现有 Issue/PR 的共享范围筛选保持原行为。
