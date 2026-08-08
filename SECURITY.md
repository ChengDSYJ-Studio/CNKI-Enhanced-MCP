# Security Policy

## Supported versions

当前仅 `1.x` 最新版本接收安全修复。

## Reporting a vulnerability

请不要在公开 Issue 中发布密码、Cookie、CNKI 下载链接中的临时令牌、学校 SSO 信息或可复现的付费权限绕过方法。仓库创建后，请在 GitHub 启用 Private vulnerability reporting，并通过 Security 页面私下提交报告。

报告应包含受影响版本、风险说明、最小复现步骤和建议修复方案。请先删除截图、HTML、SQLite 和日志中的个人或机构信息。

## Scope

- 路径穿越或写出项目工作区；
- Cookie、凭据或机构身份泄漏；
- 任意 URL 访问导致的 SSRF；
- 下载文件名或下载路径引发的安全问题；
- MCP 参数导致的命令执行或敏感文件读取。
- 在线阅读器 invoice、nonce 等短期授权参数泄漏。

本项目不会接受验证码绕过、付费墙绕过或规避 CNKI 访问控制的实现。
在线 HTML 工具只点击详情页实际提供的阅读入口，区分完整授权与试读，不持久化正文，也不返回带授权查询参数的阅读器 URL。自定义引文模板仅允许白名单字段替换，不执行代码。
