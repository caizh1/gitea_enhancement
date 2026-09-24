# 父代理实际回归结果

时间：2026-09-23T19:27:35.316377+08:00

工作目录：`/Users/archer/.cache/gitea-phase1-integration-lco96594`

命令：

```sh
/Users/archer/.cache/gitea-governance/go/bin/go test -json -tags=bindata sqlite sqlite_unlock_notify -count=1 -run ^(TestCreateTaskForRunnerConcurrentClaim|TestCreateTaskForRunnerRejectsOwnerChangeAfterCandidateScan|TestGroupMoveInvalidatesActionsTrust|TestGroupMoveOrdersConcurrentRunCreation|TestScopedSourceArchiveOrdersConcurrentRunCreation|TestGroupMoveOrdersConcurrentScheduleRunCreation)$ ./tests/integration
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestCreateTaskForRunnerConcurrentClaim` | pass | 0.9 |
| `TestCreateTaskForRunnerRejectsOwnerChangeAfterCandidateScan` | pass | 0.42 |
| `TestGroupMoveInvalidatesActionsTrust` | pass | 0.54 |
| `TestGroupMoveOrdersConcurrentRunCreation` | pass | 0.56 |
| `TestScopedSourceArchiveOrdersConcurrentRunCreation` | pass | 0.48 |
| `TestGroupMoveOrdersConcurrentScheduleRunCreation` | pass | 0.45 |
| `gitea.dev/tests/integration` | pass | 5.154 |
