# 父代理实际回归结果

时间：2026-09-23T19:23:46.873870+08:00

工作目录：`/Users/archer/Work/gitea-enhancement`

命令：

```sh
/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -run ^TestGroupIssuesFilterScopeBeforeCountsAndPagination$ ./services/governance
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestGroupIssuesFilterScopeBeforeCountsAndPagination` | pass | 0.14 |
| `gitea.dev/services/governance` | pass | 2.701 |
