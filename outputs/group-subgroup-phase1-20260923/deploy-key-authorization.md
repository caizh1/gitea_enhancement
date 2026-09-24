# 部署密钥旧请求授权窗口

日期：2026-09-23。父代理在复核 Runner／转移信任边界时继续检查部署密钥消费者。

## 已成立路径与反证

原生 Web／API 的创建和删除入口只在中间件判断仓库 Admin。随后模型 `WithKeyWrite` 能与转移排序，但没有重查该请求的操作者。因而存在“旧 Owner 通过入口→仓库转移提交→旧请求创建部署密钥”的路径；转移前检查不存在部署密钥不能阻止转移后插入。

固定处理器测试 `TestDeployKeyRejectsOwnerChangedAfterAuthorization` 先确认旧 Owner 的入口权限，再改变仓库 OwnerID，最后调用真实处理器。修复前期望 403、实际 201，退出 1。这是固定状态顺序，不是依赖延时的概率测试；尚未将整条真实转移和 HTTP 请求并发组合运行在 PostgreSQL。

## 修复及复验

- 新服务包装在 `WithActorWrite` 内重新读取操作者、当前仓库和原生／治理有效权限，并以 `ForMutation().IsAdmin()` 授权。
- Web／API 创建、删除调用此包装；保留内部底层能力。明确失权返回 403。
- 数据库写入和审核记录与权限检查同事务。SSH 文件同步使用已有提交后机制；嵌套删除中的同步因 `db.InTransaction(ctx)` 立即返回，不在事务内写文件。
- 子代理独立复核曾提出“嵌套删除会锁内同步”的候选问题；检查上述现有早退后撤回，不将已反证的问题列为阻塞。

实际命令：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
go test -count=1 -run '^TestDeployKeyRejectsOwnerChangedAfterAuthorization$' ./routers/api/v1/repo

PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
go test -count=1 ./services/asymkey ./routers/api/v1/repo ./routers/web/repo/setting
```

修复后第一条退出 0，1.624 秒。第二条三包全部退出 0，分别 1.768、1.677、1.762 秒。新增服务用例覆盖无关只读用户拒绝、旧 Owner 创建和删除拒绝、当前 Owner 创建和删除成功。现有审计回滚及共享密钥引用测试随包运行。子代理对此补充修复审查结论为 PASS，无成立 P0／P1。

这些证据不代表真实 SSH 客户端、全部 LDAP／自定义角色或完整部署密钥 UI 已通过。
