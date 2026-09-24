# 父代理实际回归结果

时间：2026-09-23T18:08:03.930798+08:00

工作目录：`/Users/archer/Work/gitea-enhancement`

命令：

```sh
go test -json -count=1 -run ^TestRepositoryReturnsToItsOwnStoragePath$ ./services/repository/
```

退出码：`1`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestRepositoryReturnsToItsOwnStoragePath/transfer_back` | fail | 0.05 |
| `TestRepositoryReturnsToItsOwnStoragePath/rename_back` | fail | 0.02 |
| `TestRepositoryReturnsToItsOwnStoragePath` | fail | 0.07 |
| `gitea.dev/services/repository` | fail | 1.256 |
