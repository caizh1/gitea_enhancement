# 原生屏蔽操作审计证据

## 范围与实现

原生个人及组织的屏蔽、解除屏蔽和修改备注已通过 `services/user/block.go` 的治理写事务复核当前操作者、组织 Owner 与生命周期。本轮仅在这些已有最终事务内追加成功审计：`user.blocked`、`user.unblocked`、`user.block_note_updated`。事件目录给出中英双语名称；个人事件归属本人 `user` 范围，组织事件归属 `group` 范围并保存发生时的当前祖先投影。审计对象是被屏蔽用户，详情只包含屏蔽前后状态与备注是否变化，不包含备注正文。

相同备注不产生更新事件；已屏蔽时再屏蔽和归档组织解除屏蔽被拒绝，不写成功事件。审计写入与关系清理、屏蔽记录及备注更新共用事务，审计失败则回滚业务变更。原生尚未建立治理命名空间的组织保留当前兼容路径，但不伪造祖先投影。

## 先红后绿

先加入专用 SQLite 服务测试，运行：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./services/user -run '^TestNative(BlockAudit|PersonalBlockAudit)' -count=1
```

修前运行态红例：组织与个人成功屏蔽后均找不到 `user.blocked` 事件；故障触发器拒绝审计插入时，原服务仍返回成功、屏蔽记录未回滚。这些是实际行为失败，不是编译错误。

写入审计后同命令通过。扩展原有授权回归及审计查询的定向命令：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./services/user -run '^TestNative(BlockAudit|PersonalBlockAudit|BlockRechecks)' -count=1
```

结果 `ok`，耗时约两秒。固定样本验证子群组事件能由父组 Owner 按父组范围查到，个人事件仅有个人范围；三类事件存在且详情无备注；不变备注和拒绝解除不产生成功事件；创建审计故障使屏蔽记录及已清理的关注关系一并回滚，备注审计故障使旧备注保留。SQLite 故障触发器在测试结束时删除，不污染随后已有服务测试。

最终运行 `GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./services/user ./models/governance -run '^TestNative(BlockAudit|PersonalBlockAudit|BlockRechecks)|^TestAudit' -count=1`，两个包分别 `ok`，用时 1.668 秒与 1.721 秒。定向 `gofmt` 与 `git diff --check` 无输出。对两个包的增量 lint `--new-from-rev=HEAD` 为 `0 issues`；普通包级 lint 仍报告七项不在本轮新增行的既有格式或规则诊断。本轮未占用共享 PostgreSQL、活跃浏览器或实际实例，真实 HTTP 展示与导出由父任务验收。
