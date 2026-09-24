# 第五批父代理真实验收与整合记录

日期：2026-09-23。此记录区分源码、测试副本与实际运行构建。基线仍为 `main @ 9a3f93813847f53b4762e28863b09b9266f75943`，未提交或推送；测试仅使用本机隔离实例与合成账号。

## 开始时的构建边界

运行中为 `gitea-phase1-ancestors-v4`。本批开始时，Release/LFS 最终授权、原生团队邀请、门禁说明文字和群组归档徽标的源码修改尚未部署；不能用 v4 正例替这些修改背书。

## 真实产物下载与误判反证

普通 Owner 在来源仓库更新一期工作流，使用官方 `actions/upload-artifact` 的 `v3.1.3` 对应提交 `a8a3f3ad30e3422c9c7b888a15615d19a852ae32`。来源本地提交 `3c63178` 推送至隔离服务器；深层仓库没有自己的 YAML，由父组来源提供工作流。

- 浏览器手动触发深层仓库 `ancestor-ci` 的 Run 30，根群组 Runner 2 完成 Job 33。UI 显示成功，产物 `phase1-ancestor-result` 为 46 字节。
- 最初 UI 点击后，浏览器工具等待下载事件超时；后续 Basic Auth 客户端对普通 Web 下载 URL 均返回 404。仅凭这两个现象曾怀疑下载失败，现已反证并撤回：普通 Web 路由不接受该测试所用的 Basic 身份；工具未返回事件也不代表浏览器未下载。
- 只读数据库佐证：Run 30 属于仓库 2，最新 Attempt ID 为 33；产物 ID 2 对应上述 Run/Attempt，状态为已确认上传，内容路径为 `result.txt`。
- 模型查询及 Web 下载固定测试在未修改业务代码时直接通过，排除切片求值、Attempt 解析和存储猜测。
- 最初 Owner 点击生成的 `phase1-ancestor-result.zip` 修改时间为 21:58:19；子级 Owner 重新登录后于 22:17:43 再次点击得到第二份 ZIP。两者均包含 46 字节的 `result.txt`，与工作流生成内容完全相符，SHA-256 均为 `8d5f32877961a67c5e142de8887505f68b94a242f9400028a7222b8a4bb03bc9`。
- 对第二次点击启用仅匹配 `artifacts` 的隔离路由诊断，记录为 `200 OK @ actions.ArtifactsDownloadView`；错误 Basic 请求则在 `RepoAssignment` 被 404 拒绝。业务代码未因该误判修改；其他角色及撤权的真实浏览器下载仍不能用这次 Basic 结果冒充通过。

## 父群组审批继承与新差异失效

普通 Owner 在根群组 UI 新建“一期父组审批闭环”规则，要求子级 Owner 批准一票。真实 HTTP Git 推送测试分支 `phase1-approval-ui`，提交 `3de8d0d8a91da83437c5db910d5620f7503ce98a`；浏览器创建深层仓库 PR 4。

祖先 CI 的 push/PR Run 31、32 成功后，页面仍显示“还需 1 人批准”，不能立即合并；实际 API 合并请求被 403 拒绝。子级 Owner 从 UI 阅读差异并批准后，页面显示全部审批满足、出现立即合并入口。随后再推送 `2303c9d39b3e5331e68b32241e7fd650c04ca43a`，即使 Run 33、34 再次成功，旧批准仍不计数，恢复“还需 1 人批准”。

诊断重启使浏览器会话失效，旧页面上的一次自动合并点击返回 404；刷新可见匿名状态，不能将此算作产品自动合并故障或有效权限负例。重新登录后，子级 Owner 在仍缺批准时开启“检查通过后自动合并”；页面继续等待。再次阅读第二版差异并批准后，旧审批标为已失效，22:19:25 自动合并成功，提交为 `da33580fc4d4eb315a02943ab207047ae0b784da`。此结果通过 PR 时间线和合并提交核对。

## 邮件邀请与验收客户端身份

子级 Owner 从实际设置页进入成员邀请，邀请合成邮箱 `phase1-outsider@example.invalid` 为深层群组 Reporter。隔离 SMTP 实际接收邮件；匹配账号从邮件链接进入时自动预览成功，显示资源、邀请者、角色与到期时间。点击接受后页面显示授权已生效，刷新后能看到该子树的三个仓库，不能管理父级配置。

受邀 Reporter 通过 UI 下载 Run 30 的产物，第三份 ZIP 内容和前述 SHA-256 一致。HTTP Git 的子树读取也成功，兄弟私有仓库拒绝。

首次 Git 范围对比受本机 `credential.helper=osxkeychain` 缓存影响，环境变量中的用户名没有替换缓存身份；这次结果已丢弃。重新执行时显式使用 `git -c credential.helper= -c credential.username=phase1-outsider ls-remote ... HEAD`：获授 `ancestor-ci` 退出 0，兄弟 `sibling-private` 退出 128／Repository not found。后续权限验收必须固定用户名并禁用该命令的缓存，不能仅靠更换 askpass 环境变量假定用户已经改变。未修改用户全局 Git 配置或钥匙串。

## 真实 SSH 部署密钥首错

普通 Owner 从 `deep-lfs` 仓库的“设置 → Deploy Keys”创建一次性隔离测试密钥，UI 保存并回显 Read / Write。公钥指纹为 `SHA256:HXL2OoShQiS8ZMlMiiYkIdF+lM707lxW8fl6Ewa+xTE`，私钥仅存本机权限受限的测试目录，本文不记录。

使用该密钥进行 SSH Git clone 返回 0，关闭自动 smudge 后执行 `git lfs pull` 返回 2，服务返回 `Authentication required: Unauthorized`。这是 v4 的真实协议失败，下一构建必须验证修复后同一客户端能下载，且撤销后旧授权不能继续使用。

## 环境与尚未验证

用户已明确目标性能环境暂未提供。Linux amd64、8 vCPU、16 GiB、PostgreSQL 的容量指标继续保持未验证；本机 arm64 结果不能替代。人工活跃时间和重复操作均减少 80% 的指标仍无人工对照记录。

本地 SMTP 接收器限定 `.invalid` 收件人，不连接真实邮箱。22:15 正常停止 v4 后，备份真实根目录 SQLite 数据库和配置至权限受限的 `before-local-mailer` 目录，再为同一个 v4 构建启用本机 SMTP 13252 和产物路由诊断。PID 87840 恢复服务，邮件真实验收另行记录。原生团队邀请修改的确定性红绿和独立审查另见对应证据文档。
