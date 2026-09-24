# 父代理 PostgreSQL 独立复验

时间：2026-09-23T16:38:58.428079

使用隔离 PostgreSQL 与 MinIO，测试口令不记录。环境参数同 runner-safety-evidence.md。

命令：`go test -count=1 -run ^TestCreateTaskForRunner(ConcurrentClaim|RejectsOwnerChangeAfterCandidateScan)$ ./tests/integration`

退出码：0

```text
ok  	gitea.dev/tests/integration	7.526s
```
