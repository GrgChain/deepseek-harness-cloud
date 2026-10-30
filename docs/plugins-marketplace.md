# 插件实施说明

2026-10-27补充：插件广场继续只读发现。REQ-25创建表单另提供经过兼容检查、无需额外配置的官方随附插件选择及准确版本启用；第三方和缺依赖条目当前禁选。此创建能力不改变广场交互，见[创建扩展规则](requirements.md#619-创建工作空间扩展配置req-25)。

REQ-24 在平台工作台提供 `/app/plugins`，导航名称为“插件”，位于“模型提供商”之前。用户无需创建或选择工作空间即可浏览目录、搜索功能、筛选分类与来源、查看详情及复制固定版本包名。页面沿用 `instances:read` 保护入口，安装与启用由用户在 DSH 中自行完成。

## 实现与边界

| 位置 | 职责 |
| --- | --- |
| `apps/src/app/app.tsx` | 桌面/手机导航、页面标题与受权限保护的路由 |
| `apps/src/features/plugins/plugin-catalog.ts` | 静态目录、来源/版本/文档信息、全目录关键词及分类/来源筛选 |
| `apps/src/features/plugins/plugins-panel.tsx`、`plugins.css` | 响应式卡片、统一分页、详情抽屉、复制状态与失败恢复 |
| `apps/tests/plugins.spec.ts` | 桌面/手机浏览、筛选、详情、剪贴板、权限和无工作空间请求验证 |

本功能不新增平台 API、数据库表或依赖，不调用实例查询、生命周期、访问交接或 DSH RPC；不修改 DSH 代码、前端资源、镜像、容器配置、profile 或持久数据，不注入脚本。目录随平台前端发布，浏览时不会向 npm 或仓库发送请求；只有用户打开外部文档时访问对应来源。

全目录搜索覆盖名称、用途描述、包名、作者和关键词，多个词同时匹配；分类与官方/第三方筛选可组合。分页默认 10 条，可选 20/50 条，搜索与筛选变化回到第一页。详情关闭保留筛选；剪贴板不可用时仍可选择并手动复制包名。

## 目录来源与维护

2026-10-27 初始目录共 12 个插件，其中 9 个官方组合包来自仓库锁定的 DSH `0.2.1-alpha.2` 包清单及中文说明，3 个第三方包通过公开 npm 指定版本的元数据、来源仓库和 `dsh.bundle.patch` 声明核对。原始核对记录在本机 `.local/qa/plugins-market/source-verification.json`，第三方 npm 元数据保存在同目录的 `*.metadata.json`。

| 来源 | 收录内容与依据 |
| --- | --- |
| 官方 | 会话搜索、持久终端、智能体团队、语音输入、会话标题、Git Worktree、Ralph 循环任务、会话检查器、DSH 徽章技能；对应 `@deepseek-ai/dsh-experimental-*` 的 `0.2.1-alpha.2` 组合包，文档链接到 [DeepSeek Harness 官方仓库](https://github.com/deepseek-ai/deepseek-harness/tree/master/packages/experimental) |
| 第三方 | [`dshmarket@1.71.1`](https://www.npmjs.com/package/dshmarket/v/1.71.1)，来源 [dsh-market/dsh-market](https://github.com/dsh-market/dsh-market) |
| 第三方 | [`@openviking/dsh-memory-plugin@0.5.29`](https://www.npmjs.com/package/@openviking/dsh-memory-plugin/v/0.5.29)，来源 [volcengine/OpenViking](https://github.com/volcengine/OpenViking/tree/main/examples/dsh-memory-plugin) |
| 第三方 | [`@linxin666/dsh-ssh@0.4.11`](https://www.npmjs.com/package/@linxin666/dsh-ssh/v/0.4.11)，来源 [zhu1090093659/dsh-web](https://github.com/zhu1090093659/dsh-web)；包声明 DSH `>=0.2.1-rc.3` |

“官方”表示发布来源，来源核对表示公开包信息与组合包声明已核对，均不代表已安装、安全审查或在当前平台运行版本中兼容。未声明 DSH 版本范围时页面明确提示由 DSH 检查依赖；目录版本为收录快照，不表示实时最新版。广场不展示虚构下载量、评分或已安装状态，也不提供自动安装、更新或卸载。

新增或更新条目时，在 `plugin-catalog.ts` 维护元数据并通过代码评审发布。须核对包存在、指定版本、组合包声明、来源归属、公开 HTTPS 文档/仓库及声明的版本要求；普通 npm 库不得仅凭名称收录。说明用纯文本呈现，不执行 README 或加载第三方 HTML、图标脚本。

## 验证

TC-110 的自动验证以受控身份与本机 HTTPS 前端执行，不需要真实工作空间。覆盖目录、组合筛选、全量搜索、分页、详情、固定版本复制、剪贴板拒绝、只读权限及桌面/手机布局，并断言没有实例/DSH 请求或写操作。剪贴板成功分支使用 Chromium 实际剪贴板，失败分支模拟拒绝。

2026-10-27 验证结果：`npm run typecheck`、`npm run build` 通过；`npm test` 共 49 项通过；Playwright 插件与平台导航回归在桌面和手机共 18 项通过。浏览器验证产物位于本机 `.local/qa/plugins-market/accepted/`。目录核对和平台浏览器验证不替代逐插件安装与真实运行验证。
