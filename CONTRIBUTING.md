# Contributing

感谢你愿意参与 NatPunch。本项目是 [ehang-io/nps](https://github.com/ehang-io/nps)（GPLv3）的修改版，请遵守以下约定。

## 流程

1. Fork 并基于 `master` 新建分支（如 `fix/xxx`、`feat/xxx`）。
2. 提交前自查（与发布门禁一致）：
   - `go build ./...`、`go vet ./...`、`go test ./...` 全绿；
   - 涉及并发/边界改动运行 `go test -race ./...`（integration 除外）；
   - `govulncheck ./...` 无新增可达漏洞；
   - `golangci-lint run`（配置见 `.golangci.yml`）无告警。
3. 提交信息中文、描述改动动机与验证方式（本项目惯例：`git commit -F` 文件方式提交，避免引号问题）。
4. 提 PR 到 `master`；CI（check → integration → build）全绿后合入。

## 代码约定

- 不引入无必要新依赖；新增依赖需说明理由并在 SECURITY.md 更新漏洞状态。
- 安全默认 fail-closed，但不破坏存量升级（0=不限、空=旧行为+告警 等兼容语义保持）。
- 配置新增：同步 `conf/natpunch.conf`、`cmd/natpunch/natpunch.go` 默认模板、`docs/config-reference.md`。
- 库代码禁止 `os.Exit`；错误处理不忽略（`err.Error()` 只在 err 非 nil 时调用）。
- 并发共享状态必须加锁/原子；新增 map 并发访问需带 `-race` 验证。
- 敏感信息（密码、vkey、VerifyKey）不落日志、不下发接口（DTO 脱敏）。

## 文档

改动涉及用户可见行为时同步：README / README_zh / docs/ / CHANGELOG.md。GPL 声明（README 顶部 + LICENSE 版权行 + NOTICE）保持不变。

## 行为准则

只面向合法运维场景（见 README AUP）。任何绕过认证、滥用终端、未授权穿透他人系统的代码不接受。
