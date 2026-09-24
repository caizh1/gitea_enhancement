# 第十一版补充：转移后接受邀请与多来源撤权

日期：2026-09-24。运行构建仍为 v11，所有操作只在 `127.0.0.1:13043` 一次性实例及合成账号中执行；发信只到本机 SMTP。源码仓库未提交或推送，Git 写入只发生在隔离测试仓库。

## 转移后接受邀请

1. 普通 Owner 从私有仓库 12 的协作者页进入邮箱邀请，给合成收件人创建 Developer 邀请 4；本地邮件接收器确实收到。
2. Owner 从原生仓库设置预览并确认将仓库从 `phase1-other/request-private/request-private` 转回 `phase1-request-private/request-private`。预览解释移除旧群组来源、取消旧队列、失效旧信任和保持受权限控制的旧别名；本样本没有活动任务、部署密钥、仓库 Runner 或组织标签。
3. 收件人登录并打开同一封邮件，预览显示当前完整新路径。点击“接受邀请”后显示“邀请已接受，授权已生效”；协作者页刷新后显示本项目直接 Developer 来源。
4. 真实 HTTP Git 对新旧地址均能读取相同默认分支提交 `35c9015118ae2c7dfe668467e7034147ba089fba`。收件人向测试分支 `phase1-invitation-accepted` 推送合成提交 `75e19b0` 成功，没有向源码仓库远端写入。

原始协议结果：`/Users/archer/.cache/gitea-phase1-git-7n1x_l_6/v11-invitation-accepted-git.json`。每次 Git 调用都禁用系统 credential helper，使用明确的合成用户身份，不借用 Owner 缓存凭据。

## 独立来源的移除与完全撤权

1. Owner 从群组 15 成员 UI 添加同一用户为 Reporter。仓库成员列表仍显示有效 Developer，展开可分别看到群组 Reporter 与本项目直接 Developer 两条来源。
2. Owner 点击“预览移除直接授权”，页面明确显示 `Developer → Reporter`，并列出剩余群组来源；确认后刷新只剩 Reporter。
3. 真实 HTTP Git 新旧地址仍可读，但创建新分支的 push 返回 1，服务端明确拒绝写权限；该拒绝分支没有创建。
4. Owner 再从群组成员 UI 移除 Reporter 来源。收件人刷新私有仓库页面为原生 404，随后新旧 HTTP Git 请求均返回 128、仓库不可见。

原始协议结果分别为 `v11-invitation-direct-revoked-git.json` 和 `v11-invitation-all-revoked-git.json`，位于上述私有缓存目录。此样本证明管理者撤销受邀者的直接关系、保留独立继承来源和最后来源撤销均真实生效；不替代 Team、共享、LDAP 及全部角色组合。

## 短期授权到期

通过正式 API 给群组 15 添加 90 秒 Reporter 授权，没有直接写数据库或修改系统时间。返回 204 后 HTTP Git 可读；收件人在真实协作者页看到来源与精确到期时间 `2026-09-24 00:52:39 +08:00`。到期后刷新原页面为 404，新旧 HTTP Git 读取均返回 128。Owner 的真实审计页显示 00:52:48 的“成员授权到期”成功事件，主体是授权到期任务，对象为该合成用户。

只读数据库核对到期来源已经由任务清理，并存在 `member.expired` 审计 375；没有手工删除或补配置。结果文件为 `v11-expiry-before.json` 与 `v11-expiry-after.json`。到期后的协议检查晚于后台清理，本次实测不单独证明清理前瞬间的授权路径，也不作为并发屏障证据。页面配置只提供 UTC 日期，因此短期秒级样本由现有 API 建立；不能将其写成 UI 完成秒级到期设置。

## 边界

本记录覆盖单个无活动执行环境的私有仓库、Owner 和被邀请账号，以及 HTTP Git；并发接受与转移、SSH／LFS／包旧地址、来源 Owner 撤权、邀请到期和机器凭据均不因本样本自动通过。v11 预览仍使用旧“邀请时的资源”文案，文案修复需下一开发构建真实核对。
