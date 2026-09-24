# 收尾增量静态检查

时间：2026-09-23T16:46:37.397355

命令：`GOOS=linux golangci-lint run --new-from-rev=HEAD --build-tags=linux,bindata`

退出码：1

```text
services/repository/transfer_test.go:133:2: equal-values: use assert.Equal (testifylint)
	assert.EqualValues(t, recipient.ID, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).OwnerID)
	^
1 issues:
* testifylint: 1
```

## 修正后的复验

将同类型 ID 断言改为 `assert.Equal` 后，以相同命令复验。退出码：0。

```text
0 issues.
```

该检查仅报告相对 HEAD 的增量问题，不等于全仓 lint 通过。
