# 群组直接成员自退闭环

## 结论与边界

**VERDICT: PASS。** 已补 Reporter 等普通成员从群组成员页自愿退出自己在当前群组的直接治理成员授权。退出只删除 `governance_membership` 当前群组／当前账号的直接记录，不触碰父组继承、共享或原生团队来源；页面在确认前说明这一点。若退出后仍可读该群组，返回成员页；若失去读取权，安全返回“我的群组”。

原生组织的“退出成员”只处理 `org_user`、`team_user`，不能代替治理直接成员退出。隔离 UI 数据中，group 6／user 3 有一条永久 Reporter 治理成员记录，原生组织与团队成员记录均为零；自退缺口在当前数据上真实可达。

## 红绿证据

- 红：新增 Web 集成测试先要求 Reporter 自己的成员行出现“退出本群组直接授权”；实现前失败，页面只显示“在授权来源处管理”。
- 绿：`GITEA_TEST_DATABASE=sqlite /Users/archer/.cache/gitea-governance/go/bin/go test -count=1 -run '^TestGroupMemberLeavesOwnDirectSource$' ./tests/integration` 通过。覆盖本人按钮和 POST、他人伪造拒绝、成功后跳安全列表、失权后群组页 404、重复提交拒绝。
- 绿：`/Users/archer/.cache/gitea-governance/go/bin/go test -count=1 -run '^TestLeaveGroup' ./services/governance` 通过。覆盖归档时允许撤销自己直接来源、父组来源保留、停用账号与代办拒绝、最后永久 Owner 回滚、审计失败整事务回滚且修订号不推进。

服务端在治理短事务内重新读取操作者与直接关系，删除后校验当前祖先链仍有有效永久 Owner，再推进群组修订号并保存 `member.removed` 审计。相同群组的写入沿已有 `WithWrite` 串行，最终归属判断不依赖页面时的快照。未新增 API 或新的权限系统。

定向 golangci-lint 检查 `services/governance`、`routers/web/governance`、`tests/integration` 时有 12 条其他文件诊断；本次新增或修改的文件无诊断。未占用共享 PG，也未运行真实 v6 浏览器验收；这一步由主任务完成。

## 相邻 I-02 功能审查

申请加入、审批／拒绝、撤回申请、撤销邀请已有 UI 与服务调用链。群组入口为“成员”页的“访问申请”“成员邀请”，以及群组设置页的相同入口；个人菜单可到“我的访问申请”。申请创建检查可见性、当前成员状态及开关；审批在治理事务内按稳定申请 ID 重鉴权并创建直接来源；撤销邀请按来源范围与 ID 重鉴权。私有群组对非成员不可见，也不开放自助申请，需由有权者邀请。上述相邻 UI 的真实鼠标验收仍由主任务负责。
