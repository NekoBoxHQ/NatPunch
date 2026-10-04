# Security Policy

## 支持的版本

| 版本 | 支持 |
|---|---|
| master（dev，阶段四后） | ✅ 安全修复持续跟进 |
| 历史 tag | ❌ 仅建议升级到包含安全修复的最新版 |

## 漏洞报告

请**不要**在公开 Issue 中披露漏洞细节。请通过以下任一私密渠道报告：

- Telegram 群组（README 中链接）@ 维护者
- 或通过 GitHub Security Advisory（私密）功能

请附上：受影响版本、复现步骤、PoC（如可行）、影响评估。常规披露流程：确认后 7 天内修复并发布补丁版。

## 已知依赖风险与可达性评估（2026-10，阶段四 F4-2 实测）

`govulncheck`（2026-10 运行，go 1.26）结论：**代码 0 漏洞，import 包 0 漏洞，module 级 1 项**。

| ID | 模块 | 描述 | 可达性 | 状态 |
|---|---|---|---|---|
| GO-2026-5932 | golang.org/x/crypto（openpgp 子包） | openpgp 未维护、不安全 by design、无修复版 | **不可达**：本项目不使用 openpgp（仅用 crypto/bcrypt、ssh 等），govulncheck 未发现任何调用路径 | 接受残余；文档记录 |

### beego（github.com/astaxie/beego v1.12.0 → replace github.com/exfly/beego v1.12.0-export-init）

- beego 1.12 系列（2019 年冻结）存在已披露 CVE（如 CVE-2019-16354/16355 等），本项目**未升级 beego 2.x**（迁移风险大，且 replace 目标为 NekoBoxHQ 维护分支以导出 `InitBeforeHTTPRun`）。
- **可达性评估（逐项，2026-10）**：
  - 涉及 Web 路由/会话的 beego 漏洞，其入口（面板控制器、会话中间件）在本项目中被使用，但**本项目已在业务层加固**：面板认证纯会话 + fail-closed、变更强制 POST、SameSite=Strict、DTO 脱敏、终端权限收紧；未发现可利用的未授权入口（阶段一~三门禁已验证）。
  - `govulncheck` 对当前构建**未报任何 beego 漏洞为可达**（0 code / 0 import 级）。
- **结论**：beego 历史 CVE 在本项目当前加固姿态下**不可达**；升级 beego 2.x 列为后续项（独立 PR + 全量回归）。

### kcp-go（github.com/xtaci/kcp-go v5.4.20+incompatible）

- 无已知漏洞（govulncheck 未报告）。v5.6+ 因 module path 变更为 `github.com/xtaci/kcp-go/v5` 需要改 import（超本阶段范围），列为后续项。

### 其他依赖

- x/net v0.59.0 / x/crypto v0.57.0 / x/text v0.42.0 / x/sys v0.48.0 / snappy v1.0.0 / ants v2.12.1 均为当次升级后最新稳定线；升级后 govulncheck 无 import 级漏洞。

## 升级校验信任模型

- 发布物附 `SHA256SUMS`（**强制校验**，防传输损坏/镜像篡改）与 `SHA256SUMS.minisig`（minisign 签名，本机有签名工具时校验，防发布方密钥泄露场景）。
- 发布流程门禁：vet / test / govulncheck / golangci-lint / integration 全绿 + 8 架构构建 + 架构自检。

## 加固指引

详见 [docs/security-hardening.md](docs/security-hardening.md)。
