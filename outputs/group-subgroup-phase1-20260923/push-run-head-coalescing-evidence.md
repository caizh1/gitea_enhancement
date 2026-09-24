# 推送事件与 Actions Run 提交绑定核对

仓库 2 的新分支 `phase1-developer-20260924014733` 只有三次成功写入：Developer API 创建、Developer API 修改、Developer HTTP Git 正常推送。持久 Run 63、64、65 各有一条独立 `push` 事件，`payload.after` 依次为 `555908d828ee08fc3f3de31ebe62e626594cd089`、`59808183f5787a72bb26dd71c75d9168075dcf30`、`62b38f3601b81efb436afee8db28b41e47a5dbe9`；`before` 也依次串接前一个提交。三条 Run 均记录触发者 user 10、相同完整分支引用和单一注册来源仓库 3，排除重复脚本执行、重复来源注册及定时调度。

三条 Run 均于 2026-09-23 17:47:37 UTC 创建，`CommitSHA` 与标题却都对应最后一个 HTTP Git 提交 `62b38f3601b81efb436afee8db28b41e47a5dbe9`。源码路径是 `services/repository/push.go` 异步处理三次 push 更新，`services/actions/notifier.go` 分别保留各自的 PushPayload，`services/actions/notifier_helper.go` 的 `notify` 在处理时调用 `gitRepo.GetRefCommitID(ref.String())` 读取当时分支 HEAD，再把该 commit 传入 Run 构造。它没有用事件 `payload.after` 固定每次 Run 的提交。因此这是三个历史事件在队列处理时汇聚到最新 HEAD 的行为，并非三个独立提交各执行一次。

安全反证：任务领取时 `services/actions/task.go` 经 `TaskCredentialValid` 重读触发者对消费仓库的 Code／Actions 读取权；完全撤权的旧触发者不能领取任务或取得凭据，任务 token 后续访问也重验。仅失去写权但仍有来源读取资格的旧触发者，历史事件仍可能以其身份运行之后的有权提交；任务 token 按任务声明及当前各层策略计算，现有代码不足以证明人类角色降级必然降低每项 token 权限。本次三条均是同一仍有 Developer 权限的 user 10，最终祖先工作流均成功，未出现当前可证明的 P0/P1 越权。`payload.after` 与 Run SHA 不一致会导致事件时间线和重复 CI 的混淆，记为 **P2**；降级旧身份与使用事件 `after` 的其他工作流语义仍需独立验证，不能由本样本宣称安全闭合。

本轮只读核对，没有修改通知器或运行状态。直接把 Run SHA 改成旧事件 `after` 可能影响默认分支 schedule 对当前 HEAD 的快照更新，须作为独立设计和回归处理。
