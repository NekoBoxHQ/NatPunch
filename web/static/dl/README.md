# 自建源目录（运行时由 install_server.sh 填充）

install_server.sh 在安装/升级成功后会把当前版本的发布物拉到这里：

    web/static/dl/<版本>/           包 + SHA256SUMS + SHA256SUMS.minisig + 校验器 + 三个脚本
    web/static/dl/install.sh        稳定入口（始终指向最新版本，供客户端一键安装）

面板通过磁盘覆盖层直接对外提供 <web_base_url>/static/dl/...，
配置项 install_base_url=auto 时「部署」按钮按当前访问域名自动拼地址。

本文件只是让目录存在于仓库/嵌入资源里，便于静态资源对账检查通过。