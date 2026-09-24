# 第十五批：推送提交与定时计划真实执行

日期：2026-09-24。构建为 v15，SHA-256 `cbd72c5fd98cddc82e48c14091b58d8149d24945ada72b33660479051425ac19`。仅本机隔离站点、合成普通 Owner 21、群组 23、私有仓库 21；没有改动生产或源码远端。

## 实际链路

普通 Owner 从群组 UI 创建并初始化仓库 `phase1-native-v15/ci-event-binding`，真实 HTTP Git clone／commit／push。专用真实 Runner 3 注册在所属根群组，使用 `phase1-v15:host`，不是用数据库伪造运行记录。此仓库直属根群组，本批不冒充三层祖先回归；深层继承证据保留在此前记录。

| 样本 | 实际提交 | 实际执行与 UI |
| --- | --- | --- |
| 连续推送第一版 | `1803ba04ca8af17b105a565e0a1cd4c21f53456b` | Run88／Job97 成功；日志显示 push、相同 SHA 和“第一版” |
| 连续推送第二版 | `826501bc46519517bb80aab7ee9f2997fd67cb06` | Run89／Job98 成功；日志显示 push、相同 SHA 和“第二版” |
| 带 `[skip ci]` 更新定时表达式 | `3ba93a53cfb07be8190cf51018523ad935d73b1e` | 没有普通 push Run；定时计划 4 实际生成 Run90／91，均成功。浏览器 Run90 显示 Triggered via schedule，日志显示 schedule、此 SHA 和“定时版” |
| 带 `[skip ci]` 移除 cron | `5bb86378172d0812d9321f249efcfbb82a08a4a8` | 推送后实际计划数 0，该 SHA 运行数 0；既有四次成功记录仍在 |

页面从 Actions 列表进入各 Run／Job 后展开执行步骤，核对了 Run88、89、90 的真实日志。工作流的 push 步骤实际执行 `test "$EVENT_AFTER" = "$RUN_SHA"`，不是只打印未校验的字段。只读数据库又确认两次 push 的 commit_sha／workflow_commit_sha 分别等于各自事件提交；定时触发字段为 schedule。计划删除通过正常 Git 更新触发，没有手改数据库或手工调用定时队列。

## 证据边界

连续真实推送不保证固定发生旧异步时序；确定性红绿由 `TestPushEventUsesAfterCommitAndCurrentSchedules` 在 H2 已为 HEAD 后消费 H1 实现。真实执行用于证明修复可运行及页面可核对，不能代替屏障测试。定时分支锁的 PostgreSQL 证据另见 [整合记录](parent-v15-integrated-validation.md)。已披露凭据不可收回；授权边界是持久分支表提交，不宣称 Git ref 变化瞬间撤回。

私有缓存 `ci-pushes.json`、`schedule-sha.txt`、`schedule-remove-sha.txt`、`ci-final-state.json` 和 Runner 日志保留原始样本；交付文档不含认证头、注册令牌或账号密码。目标容量、全部定时／fork／复用组合与 80% 人工效率均未在本记录验收。
