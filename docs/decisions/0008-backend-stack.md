# ADR-08 Go、Redis 与 PostgreSQL 的职责

- 版本：V0.1
- 范围：单组织 Docker 初始版本
- 状态：技术栈已确定；实现方案待验证
- 验证负责人职责：后端与测试
- 关联需求：REQ-01、REQ-04、REQ-05、REQ-08、REQ-10、REQ-11

## 1 合理性评估

Go + Redis + PostgreSQL 符合当前 API、异步编排、事务和短期流量控制需求。三者职责清晰时适合初始版本，Redis 不能代替持久业务约束。

## 2 背景与选择

Go 运行 API 和受信 Worker，按模块组织、分进程部署。PostgreSQL 保存权威业务数据、Operation/Outbox、幂等、配额和审计。Redis 处理短期脱敏缓存与请求限流。

| 数据/职责 | 当前选择 | 理由 |
| --- | --- | --- |
| 实例、配置、权限、认证代次 | PostgreSQL | 需要事务、一致版本和可恢复事实 |
| 持久任务、幂等和资源预留 | PostgreSQL | 与业务命令同事务，未知结果可对账 |
| 短期脱敏查询缓存 | Redis | 可丢弃、有限 TTL，数据库可重建 |
| 请求频率计数 | Redis 原子限流 | 限制短期流量，持久费用/资源预算仍在数据库 |
| 自定义票据防重放 | PostgreSQL 原子消费 | 按已定义交接契约实施，不能因缓存清空恢复已消费票据 |

Redis 锁不作为生命周期正确性的唯一保障；不将 Redis Pub/Sub 用作唯一任务交付，不同时维护两套 Operation 真相。

## 3 缓存与失效

cache-aside Key 包含环境、组织、主体或资源及版本，所有缓存带 TTL。缓存查询仍校验当前请求权限，失权用户不能获得缓存数据。先提交 PostgreSQL，再失效缓存；缓存失效失败允许非安全字段短期展示滞后，安全操作必须读当前权威状态，不能依据缓存旧许可授权。

模型 Key、原生启动 Token 和工作正文不进 Redis。连接使用 ACL、内部网络和最小权限；密码引用单独管理。缓存容量有界，同实例承载限流计数时采用 noeviction，内存写失败不能视为限流成功。

## 4 故障与限制

缓存不可用时有界回源；创建、计费模型探测和访问交接的限流不可确认时拒绝新请求。权限撤销、停止、删除按数据库授权及单操作约束继续处理；已接受任务由 PostgreSQL 恢复，不依赖 Redis 留存。

Redis 重启会丢失短期计数，因此持久模型费用预算及资源上限必须在 PostgreSQL 保证。部署、TTL、窗口和容量见 [后端架构](../backend.md#71-postgresql-与-redis-分工)，数值按 [配置基线](../deployment.md#8-配置与业务规则基线) 实现；Redis重启/run_id变化冷却120秒，持久探测次数仍由PG控制。

## 5 验证条件

TC-17、TC-24、TC-25、TC-28、TC-32、TC-34。验证正常连接、缓存命中/失效、权限变化、原子限流、并发上限、重启/内存满/不可用、受信任务恢复及安全清理。用例结果当前未执行，定义见 [测试验收](../testing.md)。

技术依据：[Redis Go 客户端](https://redis.io/docs/latest/develop/clients/go/connect/)、[Key eviction](https://redis.io/docs/latest/develop/reference/eviction/)、[ACL](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/)。这些能力不等于平台事务或授权保证，后者由应用与数据库规则实现。
