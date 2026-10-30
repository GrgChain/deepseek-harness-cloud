# 连接器品牌图标

图标取自各服务官网公开使用的 favicon、应用图标或产品标识，具体来源记录在 [sources.json](./sources.json)。品牌名称和标识归各自权利人所有。

保留官方配色与比例。位图转换为 PNG，最长边最多 128px；有道云笔记从官网组合标识中提取独立图形部分。SVG 使用原始矢量图。

资源随前端本地发布，页面不依赖第三方图片请求。连接器 ID 与资源的映射在 `apps/src/features/connectors/connector-logo.tsx`，目录、配置说明和连接列表共用同一组件。
