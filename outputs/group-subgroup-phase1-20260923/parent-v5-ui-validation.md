# 第五版真实 UI 与协议验收

日期：2026-09-23。构建为 `gitea-phase1-ancestors-v5`，SHA256 `92854b4ec77bb5b3c3504f48aea60fffe85f0a7e3ba95b653791b9b0310e7407`。本机隔离 SQLite 实例，普通 Owner 账号没有实例管理员身份；不涉及生产数据。

## LFS 部署密钥：真实红绿及撤销

第四版使用一次性部署密钥 SSH clone 成功，但 LFS 下载返回 Unauthorized。第五版同一客户端执行 `git lfs install --local`、`git -c credential.helper= lfs pull` 后成功；前一步只配置隔离客户端的过滤器，不修改全局 Git 设置。实际检出 780000 字节，SHA256 `bd152d359a51382b4e850d1ac6e5befc927d9ffb7ac98421feff4f316f23d0e1`。

使用同一部署密钥推送 `phase1-lfs-deploy-v5` 分支，提交 `8035f8e0bc67b5b5266f7c04c1d49a4766093b3d`，Git 和 LFS 上传均退出 0。新增对象 324000 字节，SHA256 `f15e048b23e1463682608d976ce6c3057116236a1598303a00d3b9903cf1a388`。另一份全新 HTTP Git 客户端以显式 Owner 用户名、禁用 credential helper 后拉取，实际文件大小和哈希相同。

Owner 从仓库设置的 Deploy Keys 页面撤销该一次性密钥，UI 显示密钥已移除且列表为空。下一次 SSH Git 请求退出 128／publickey 拒绝；撤销前获取的 LFS 授权请求 batch 接口返回 401。检查时该 JWT 仍有超过 23 小时有效期，排除“只是自然到期”。不在本文记录私钥或 JWT。

## 归档状态和成员入口

叶子群组 14 在设置页标题显示“已归档”。390×844 视口下，标题徽标、面包屑、导航和操作按钮自然换行，未见遮挡。展开恢复影响后显示 1 个群组、0 个仓库以及统一恢复后代的当前语义；没有再宣称未核实的 GitLab 版本行为。

点击恢复后返回群组首页，归档徽标消失，创建入口恢复；刷新后仍然有效。成员页可直接找到“成员邀请”“访问申请”“添加成员”。同一窄屏下这些入口自然换行；验收后视口恢复默认。

## 原生 Team 管理及真实权限变化

Owner 从群组统一导航进入“高级团队权限”，创建私有团队 `phase1-native-readers`，只启用 Code Read、限定指定仓库，其他单元无权限；通过 UI 添加合成用户 `phase1-outsider`，再分配 `root-policy` 仓库。

显式以 outsider 身份、禁用 credential helper 后执行 HTTP `git ls-remote`：目标仓库退出 0，HEAD `7d52d2d0d5f3d6f75f768c01acf840829885d19a`；未分配的 `shared-workflows` 返回 Repository not found／128；目标仓库未授予的 Issue API 返回 404。

从 UI 移除团队的仓库授权后，下一次目标仓库 Git 请求返回 128。随后从 UI 移除该团队成员，列表回显 0；该账号在深层群组的独立治理 Reporter 来源不受此次撤销影响。

在空团队中创建原生邮件邀请 `phase1-native-pending@example.invalid`，刷新后 Pending Invitations 仍存在；点击 Remove 后消失。修改团队描述保存并回显，最后通过确认框删除空团队，列表只剩 Owners，显示删除成功。

## 真实发现的非阻塞问题

Owner 在父群组最后一个 Owners Team 点击 Leave，服务端正确保留唯一 Owner，但 UI 无错误提示。源码对应 `ErrConflict` 被返回为 HTTP 200 的旧 `{ok:false,err:...}`，前端将其当成功刷新，错误内容被忽略。这是可用性 P2；已交由子代理最小修复，不能把当前错误反馈计为通过。

治理 Reporter 自退入口也在第四／五版中缺失；第六版新增服务和 UI 后须另行真实验证。以上正例不替代全部角色、并发、Team LDAP 或全部生命周期组合。
