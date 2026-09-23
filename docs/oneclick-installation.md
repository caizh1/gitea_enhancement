# 增强版 Gitea 一键离线安装

目标环境为 Ubuntu 24.04 amd64。使用 `gitea-ubuntu24-amd64-installer.tar.gz`，校验旁边的 `.sha256` 后，在独立的永久目录解压，执行：

```bash
sudo bash install.sh
```

只需填写浏览器访问的 IP 或域名、HTTP 端口、Git SSH 端口。Docker 与 Compose 离线依赖、Gitea 和 PostgreSQL 镜像、数据库密码、内部密钥、管理员账号和随机密码均由安装器准备。管理员名为 `administrator`，凭据保存在 `state/access.txt`，仅安装用户可读。

脚本验证 HTTP、数据库与管理员身份后才报告成功。重跑保留原账号、密钥和数据；不会删除其他实例，不允许用首次安装入口静默升级到另一镜像版本。保留并备份 `state/`、`compose.install.json` 和本实例数据卷。

提供内网 HTTP 服务；HTTPS、DNS 和外部网络放行按实际环境配置。使用 `127.0.0.1` 时仅监听回环地址。安装成功不等于所有增强功能已经完成生产验收。

构建入口为 `bash tools/build-oneclick-installer.sh`，使用相邻 `gitea-manager` 仓库维护的共用安装器。准备机需先在 Manager 仓库执行 `bash deployment/prepare-ubuntu-deps.sh`。产物位于 Manager 仓库的 `outputs/`，最终压缩包独立运行，不依赖两份源码仓库。

固定 Gitea 镜像由构建参数 `--gitea-image` 指定，默认使用现有已交付的增强版镜像。依赖解析来自 Ubuntu 签名仓库，安装包内保存版本清单和逐文件 SHA-256。
