# Coder 源码复用

固定来源为 [Coder v2.37.4](https://github.com/coder/coder/tree/v2.37.4)，commit `1d246291a8a6ae1948b8c825ee252f6a46bfd561`。原始文件和下载归档摘要见 [source-lock.json](source-lock.json)，许可证原文见 [LICENSE](LICENSE)。修改日期为 2026-10-13。

`services/internal/workspace/upstream_status.go` 抽取 Workspace transition/status 枚举和 `ConvertWorkspaceStatus` 实现。`upstream_types.go` 从 SDK 抽取 Workspace、Template、TemplateVersion、Build、Parameter 和 Job 字段；UUID 改为字符串，裁剪 Agent、App、计费、调度和企业功能字段。

`upstream_terraform.go` 改编原 Terraform executor、safeenv 和 provisionEnv：保留 init → plan → apply → state 流程、Workspace 元数据和参数协议；改用平台受控二进制、私有 state 目录、进程/文件锁、环境白名单及固定 Provider 镜像目录。未引入 Coder Server、UI、Agent、企业授权代码或 Coder Docker 镜像。

数据库迁移 `000009_embedded_workspaces.sql` 参考并改编 Coder 的 workspaces、workspacebuilds、templateversions 查询实体和连续 build_number 规则。平台重新实现应用服务、授权与 pgx 持久化，接入已有 Outbox、租约、Owner、配额和访问链路。复用范围不等于完整复制 Coder 编排服务。

这些衍生代码保留 AGPL-3.0 许可。对外提供包含这些代码的修改版网络服务时，应向服务用户提供对应源码及构建安装脚本；发布制品须包含许可证和来源说明。本项目的部署源码包应包含 Go/前端源码、数据库迁移、Terraform 与构建脚本，排除 `.local`、凭据、用户数据和二进制缓存。正式发布前需确定整个组合程序适用的许可及源码提供地址。

Terraform CLI 和 Provider 是独立安装的固定发行物，不是 Coder 镜像；其版本、归档校验和 Provider lock 单独验证。
