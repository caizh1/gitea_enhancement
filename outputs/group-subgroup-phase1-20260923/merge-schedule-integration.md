# 合并回调修复后的定时集成复验

环境：隔离 PostgreSQL 17.11、MinIO、工作区外源码副本；本次重新构建 macOS Git hook 二进制。

命令：`go test -json -count=1 -run '^TestSchedule(Update|Concurrency)$' ./tests/integration/`

退出码：0。

- TestScheduleConcurrency：pass，8.9 秒
- TestScheduleUpdate/Push：pass，3.94 秒
- TestScheduleUpdate/PullMerge/merge：pass，6.05 秒
- TestScheduleUpdate/PullMerge/rebase：pass，6.02 秒
- TestScheduleUpdate/PullMerge/rebase-merge：pass，6.05 秒
- TestScheduleUpdate/PullMerge/squash：pass，6 秒
- TestScheduleUpdate/PullMerge/fast-forward-only：pass，5.82 秒
- TestScheduleUpdate/PullMerge/manually-merged：pass，5.55 秒
- TestScheduleUpdate/PullMerge：pass，35.5 秒
- TestScheduleUpdate/DisableAndEnableActionsUnit：pass，2.59 秒
- TestScheduleUpdate/ArchiveAndUnarchive：pass，2.57 秒
- TestScheduleUpdate/MirrorSync：pass，4.14 秒
- TestScheduleUpdate：pass，48.74 秒
- 测试包：pass，60.151 秒
