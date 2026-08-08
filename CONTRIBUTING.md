# Contributing

感谢参与 CNKI-Enhanced-MCP。

## 开发环境

```bash
python3 install.py --dev
./scripts/test.sh
```

Windows PowerShell 使用 `py install.py --dev` 和 `.\scripts\run-mcp.ps1`。Linux 需要自动安装 Chromium 系统库时可增加 `--with-system-deps`。

所有运行数据必须保留在项目目录内。不要提交以下内容：

- `.cnki-data/` 中的 Cookie、浏览器 profile、数据库、诊断页面和下载文件；
- `.playwright-browsers/`、`.venv/` 或构建产物；
- CNKI 账号、学校 SSO 信息、机构名称或任何访问凭据；
- 未经授权下载的论文全文。

## 提交要求

1. 为行为变化补充测试。
2. 运行 `./scripts/test.sh`。
3. 保持 Playwright 请求串行、限速且可中止。
4. 不实现验证码绕过、付费墙绕过或隐匿自动化的功能。
5. 页面选择器变化应包含诊断依据，并避免记录 Cookie 值或表单凭据。

提交贡献即表示你有权贡献相关代码，并同意贡献内容按本项目的 GPL-3.0-or-later 许可证发布。
