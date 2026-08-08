# Third-party notices

CNKI-Enhanced-MCP 的直接运行时依赖包括：

| Component | Purpose | License |
|---|---|---|
| FastMCP | MCP server framework | Apache License 2.0 |
| Playwright for Python | Browser automation | Apache License 2.0 |

开发依赖包括 pytest 和 pytest-asyncio。打包与安装还会使用 Hatchling、uv 和 Chromium；它们分别受各自许可证约束。

本文件不是完整依赖树。发布 release 前应从锁文件和实际构建环境生成 Software Bill of Materials（SBOM）或完整许可证清单，并随 release 一起归档。
