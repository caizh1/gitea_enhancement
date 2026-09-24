# 注册令牌终检的失败与通过证据

日期：2026-09-23；仅使用隔离 fixture。

## 固定时序

API 请求的普通 Owner 已通过仓库权限加载，随后将 fixture 仓库 owner 从 2 更新为 5，再调用真实共享注册令牌 handler。此用例刻画中间件与披露之间的顺序，不等于整条 HTTP 并发或真实转移服务测试。

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestRegistrationTokenRejectsOwnerChangedAfterAuthorization$' ./routers/api/v1/shared/
```

修改前退出码 1，明确断言：expected 403，actual 200。修改服务并接通 Web/API 后退出码 0（1.185s），同时断言未生成目的范围的新 token。

## 来源与管理权回归

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestRunnerRegistrationToken' ./services/actions/
```

退出码 0（4.100s）。覆盖实例管理员、个人所有者、组织 Owner、仓库 Owner 的合法读取和轮换；普通成员与无关用户拒绝；当前管理员降权与代办管理员降权拒绝；真实 CreateGroup 的父子层级下父组 Owner 的继承管理及移除授权后拒绝。回归不使用生产 token，证据不保存测试 token 明文。
