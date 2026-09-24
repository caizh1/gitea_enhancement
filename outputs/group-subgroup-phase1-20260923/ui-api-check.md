# 浏览器流程的 API 与数据库佐证

时间：2026-09-23T16:45:08.232584

普通 Owner 身份 API：HTTP 200，is_admin=False。

普通 Owner 读取子群组：HTTP 200。

phase1-owner 读取子组注册令牌：HTTP 404，返回令牌字段=False；不记录令牌值。

phase1-outsider 读取子组注册令牌：HTTP 403，返回令牌字段=False；不记录令牌值。

刷新页面及 API 读取后，同一子组有效注册令牌记录数：1。

## 方法修正后的实测

初次佐证误用 GET，已按现有路由纠正为 POST；GET 404 不计为产品缺陷或正例通过。

POST 注册令牌，phase1-owner：HTTP 200，返回令牌字段=True；不记录令牌值。

POST 注册令牌，phase1-outsider：HTTP 403，返回令牌字段=False；不记录令牌值。
