# 父代理独立回归记录

时间：2026-09-23T16:39:31.791622

命令：`go test -count=1 -p 1 ./models/actions ./models/secret ./models/perm/access ./models/governance ./services/actions ./services/auth ./services/repository ./routers/api/actions ./routers/api/v1/repo ./routers/web/repo ./routers/web/repo/setting ./routers/api/v1/shared ./routers/web/shared/actions`

退出码：0

```text
ok  	gitea.dev/models/actions	4.948s
ok  	gitea.dev/models/secret	1.667s
ok  	gitea.dev/models/perm/access	0.966s
ok  	gitea.dev/models/governance	3.039s
ok  	gitea.dev/services/actions	2.548s
ok  	gitea.dev/services/auth	2.118s
ok  	gitea.dev/services/repository	2.652s
?   	gitea.dev/routers/api/actions	[no test files]
ok  	gitea.dev/routers/api/v1/repo	1.474s
ok  	gitea.dev/routers/web/repo	4.044s
ok  	gitea.dev/routers/web/repo/setting	1.783s
ok  	gitea.dev/routers/api/v1/shared	1.318s
ok  	gitea.dev/routers/web/shared/actions	0.678s
```

## CLI、标签数据保全及页面补充改动后的复验

命令：`go test -count=1 ./cmd ./services/repository ./routers/web/shared/actions ./routers/api/v1/shared`。退出码：0。

```text
ok  gitea.dev/cmd 2.244s
ok  gitea.dev/services/repository 2.696s
ok  gitea.dev/routers/web/shared/actions 0.666s
ok  gitea.dev/routers/api/v1/shared 1.446s
```
