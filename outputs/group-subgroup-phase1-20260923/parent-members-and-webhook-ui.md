# 父代理浏览器与 Webhook 接收端证据

记录日期：2026-09-23。本阶段使用隔离地址 `127.0.0.1:13043` 的 v2 开发构建，SHA-256 为 `e7d845fbcdc322858b21e068c07449fdce6545dc2980b8b2d9d723f39d652f78`。它尚不包含后续群组基础分支保护、包写入修复、群组测试投递按钮及稳定事件 ID；以下结果不能代替这些新增代码的验收。

## 普通管理员与固定样本

- 父组 Owner `phase1-owner` 和子组 Owner `phase1-child-owner` 都不是实例管理员。隔离账号通过标准管理 CLI 初始化，不经数据库写入；授权、建组、建仓库通过真实浏览器完成。
- 父组 Owner 从“创建顶级群组”新建私有 `phase1-other`，从父组“创建子群组”新建私有 `phase1-parent/sibling`；保存后到达正确群组页面。
- 浏览器分别创建并初始化 `sibling-private`、`outside-private`、`root-policy`、`child-release`、`deep-packages`、`deep-lfs`、`sibling-readonly`，均显示 README 和初始提交。加上原有 `runner-safety`、`ancestor-ci`、`shared-workflows`，固定功能样本达到十仓库。
- 子组成员 UI 分别给 `phase1-child-owner`、`phase1-developer`、`phase1-reader` 授予 Owner、Developer、Reporter；页面回显各独立直接来源，并提示其他来源继续生效。
- 子组 Owner 登录后，首页恰好显示其子树内五个仓库。它可以进入后代 deep 的管理入口，并创建 `phase1-parent/child/deep/leaf`；当前最深路径为根加三层子群组。
- 子组 Owner 的 leaf Webhook 页显示“受限祖先来源（群组 ID 4）”，不显示父组 URL、签名密钥、历史载荷或管理链接。
- 同一子组 Owner 直接访问父组 Hook 编辑页、兄弟仓库 `sibling-private`、独立树仓库 `outside-private`，三个真实页面均为 Page Not Found。

## 祖先 Hook 与失败重投

父组 Hook 1 在 UI 创建，端点为专用 loopback 接收器。签名密钥仅存隔离环境，不进入本报告。第三层仓库 Issue 评论触发以下实际 HTTP 请求：

| 操作 | 投递编号 | 接收端结果 | UI 结果 |
| --- | --- | --- | --- |
| 首次祖先投递 | `0467eea3-e50d-4f72-9521-8a7c1e1052f7` | HTTP 200、签名正确 | 历史响应 200 |
| 接收端改为失败后，UI 人工重投 | `27e1330f-9411-4986-b4a7-32d36903a8a4` | HTTP 503、签名正确 | 刷新、展开历史后响应 503 |
| 接收端恢复后，UI 人工重投 | `3bc4ea8c-3f46-4507-a591-981aac6f9e92` | HTTP 200 | 刷新、展开历史后响应 200 |

前两次载荷摘要相同，接收端仅记录事件类型、投递编号、仓库 ID、长度、摘要、签名验证结果和响应码，不保存载荷或认证头。一次点击后立即刷新中断了请求，未计为成功；随后明确完成的重投以上表为准。

现有 v2 的重投生成新的投递 UUID，没有稳定事件 ID。这一实测限制促成了后续 `X-Gitea-Event-ID` 改动；新语义须在新构建独立验证。群组 Hook 缺少测试投递按钮也已在本轮浏览器发现，不能用仓库按钮代替群组验收。

## 边界

本记录只证明上述身份、操作和样本。共享身份、匿名访问、HTTP/SSH/LFS/OCI/其他制品、到期、最后 Owner、完整生命周期与新增代码的实际执行仍需逐项验证。浏览器自动化时间不作为人工活跃时间，未宣称达到 80% 指标。
