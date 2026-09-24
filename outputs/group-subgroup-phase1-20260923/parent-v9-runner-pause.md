# 第九版：祖先 Runner 暂停、排队与恢复

日期：2026-09-23。只使用本机隔离 Owner、根 Runner 2、子 Runner 1 和已有深层仓库的受保护 Secret 探针。两个 Runner 进程一直运行，暂停／恢复通过普通 Owner 的原生 Web 页面完成。

## 真实操作

1. 从父群组 CI/CD → Runners → Runner 2 点击停用，页面显示保存成功及 `Availability Disabled`。
2. 从子群组 CI/CD → Runners，看到父 Runner 来源与 `Disabled`，在子组只读；进入本级 Runner 1 并停用。两项均为真实保存后的页面结果。
3. 深层 `ancestor-ci` 的 Run45 页面点击重新运行全部任务，产生 Attempt55／Job55。页面显示 `Waiting`；详情提示没有在线 Runner 可以领取。两个进程继续轮询，但只读数据库显示 Job55 的 TaskID 为 0，持续到恢复前，超过两个正常轮询周期。
4. 只在父群组恢复 Runner 2，子 Runner 1 保持停用。根 Runner 随后领取并成功完成 Job55；实际步骤日志显示 `push`、受保护引用 `phase1-secret-positive`、凭据存在为真，不打印凭据值。
5. UI 验证成功后恢复子 Runner 1，页面显示 `Availability Enabled`，恢复测试前状态。

## 数据与时序佐证

- 根 Runner 停用审计：UTC `15:49:25`；子 Runner 停用：UTC `15:50:00`。
- UTC `15:50:45`，Runner 1／2 均停用，Job55 为等待、TaskID 0。
- 根 Runner 恢复审计：UTC `15:51:12`；Task48 绑定 Runner 2，开始／结束 Unix 秒均为 `1790178674`，即 UTC `15:51:14`。
- 恢复到领取约 2 秒，小于本样本的两个正常 2 秒轮询周期；这不是千仓容量或 P95 性能结论。
- Run45 的原 Attempt50／Job50 保留；本次 Attempt55／Job55 成功。恢复子 Runner 前再次检查它仍停用，确认本次由根 Runner 领取。

## 剩余边界

此例覆盖排队期间停用与恢复、祖先任务发现、真实凭据下发和结果页。没有用它替代领取／披露临界点的固定并发屏障，也没有验证删除 Runner、执行中停用或注册令牌轮换。错误说明把停用情况概括为“没有在线 Runner”，能提示排查方向，但未精确区分所有不可用原因。
