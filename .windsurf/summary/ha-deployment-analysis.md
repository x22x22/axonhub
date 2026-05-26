# AxonHub 高可用部署分析总结

## 分析日期
2026-01-22

## 分析目标
分析 AxonHub 项目的高可用部署方案，识别无状态/有状态服务，并生成简体中文的高可用部署文档。

## 架构分析结果

### 无状态服务（可水平扩展）

#### 1. AxonHub 主服务 ⭐⭐⭐⭐⭐
- **组件**: Go HTTP Server (Gin), GraphQL API, REST API
- **无状态特性**: 
  - ✅ 无会话状态（JWT 认证）
  - ✅ 无本地缓存依赖（可选 Redis 共享）
  - ✅ 静态资源编译进二进制
  - ✅ 所有状态存储在外部数据库
- **扩展能力**: 可任意增加实例数量，无需状态同步
- **部署文件**: `cmd/axonhub/main.go`, `internal/server/server.go`

#### 2. API Gateway 层 ⭐⭐⭐⭐⭐
- **组件**: 路由、中间件、认证、CORS
- **无状态特性**: 
  - ✅ 纯转发逻辑
  - ✅ 每个请求独立处理
  - ✅ 追踪 ID 通过 HTTP 头传递
- **部署文件**: `internal/server/routes.go`, `internal/server/middleware/`

#### 3. LLM 转换器管道 ⭐⭐⭐⭐⭐
- **组件**: Inbound/Outbound Transformers, 流式处理
- **无状态特性**: 
  - ✅ 即时转换
  - ✅ 无管道缓存
  - ✅ SSE 流式传输
- **支持提供商**: OpenAI, Anthropic, Gemini, DeepSeek, AI SDK
- **部署文件**: `llm/transformer/`, `internal/server/orchestrator/`

#### 4. GraphQL API ⭐⭐⭐⭐⭐
- **组件**: gqlgen GraphQL, 查询和变更
- **无状态特性**: 
  - ✅ 查询即时响应
  - ✅ 直接从数据库获取
- **部署文件**: `internal/server/gql/`

### 有状态服务（需要高可用配置）

#### 1. 数据库 ⚠️ 必须高可用
- **支持的数据库**:
  - SQLite 3.0+ (开发/小型部署)
  - PostgreSQL 15+ (生产环境推荐)
  - MySQL 8.0+ (生产环境)
  - TiDB V8.0+ (分布式场景)
  - TiDB Cloud (Serverless, 推荐)
  - Neon DB (Serverless)
- **存储内容**:
  - 用户和角色
  - API 密钥和项目
  - 渠道配置
  - 请求日志和追踪
  - 用量统计
- **高可用方案**:
  - PostgreSQL: Patroni + etcd + HAProxy
  - MySQL: Group Replication / ProxySQL
  - TiDB: 原生分布式
- **配置文件**: `config.yml` (db.dialect, db.dsn)

#### 2. 缓存 ✅ 可选（不影响核心功能）
- **支持的模式**:
  - memory: 仅内存（不推荐生产）
  - redis: 仅 Redis
  - two-level: 内存 + Redis 二级缓存（推荐）
- **缓存内容**:
  - 渠道信息（5 秒短期）
  - 模型映射（30 分钟）
  - 权限验证结果
- **高可用方案**:
  - Redis Sentinel (3 节点)
  - Redis Cluster (分片)
- **降级策略**: 缓存不可用时自动降级到数据库查询
- **配置文件**: `config.yml` (cache.mode, cache.redis)
- **实现文件**: `internal/pkg/xcache/`

## 高可用部署方案

### 方案对比

| 方案 | 适用场景 | AxonHub 实例 | 数据库 | 缓存 | RTO | 成本 |
|-----|---------|-------------|--------|------|-----|------|
| 单区域高可用 | 中小型生产 | 3+ | PostgreSQL 主从 | Redis Sentinel | < 1 分钟 | 中 |
| 多区域高可用 | 大型生产 | 10+ | TiDB Cluster | Redis Cluster | < 2 分钟 | 高 |
| Kubernetes | 云原生 | 3+ (自动扩缩) | StatefulSet/云服务 | StatefulSet | < 10 秒 | 中-高 |

### 推荐配置

#### 小型生产环境
```
AxonHub: 3 实例 (2 核 2GB 每个)
数据库: PostgreSQL 主 + 2 从
缓存: Redis Sentinel (3 节点)
负载均衡: Nginx
```

#### 大型生产环境
```
AxonHub: 10+ 实例 (4 核 4GB 每个)
数据库: TiDB Cloud Dedicated
缓存: Redis Cluster (6 节点)
负载均衡: HAProxy + GeoDNS
```

## 关键配置文件

### 1. docker-compose.yml
- 提供 PostgreSQL/MySQL/SQLite 多种部署示例
- 包含健康检查配置
- 支持环境变量配置优先级

### 2. config.example.yml
- 完整的配置示例
- 支持环境变量覆盖 (AXONHUB_* 前缀)
- 包含数据库、缓存、日志、监控等配置

### 3. deploy/nginx.conf
- 负载均衡配置
- 流式响应支持
- 限流和安全配置

### 4. Dockerfile
- 多阶段构建
- 前后端分离构建
- 最小化镜像大小

## 监控与告警

### 健康检查
- 端点: `GET /health`
- 响应: HTTP 200 "healthy"
- 用途: 负载均衡器健康检查、Kubernetes 探针

### 可选监控
- Prometheus 指标导出 (config.yml: metrics.enabled)
- 支持 OLTP HTTP 和 Prometheus 格式
- 关键指标: QPS, 延迟, 错误率, 数据库连接数

## 核心优势

1. **完全无状态**: AxonHub 核心服务无状态，水平扩展简单
2. **快速恢复**: 实例故障自动恢复，无需人工干预 (< 10 秒)
3. **灵活部署**: 支持 VM、Docker、Kubernetes 多种部署方式
4. **数据库兼容**: 支持多种数据库，从 SQLite 到分布式 TiDB
5. **可选缓存**: 缓存层可选，不影响核心功能
6. **自动降级**: 缓存故障时自动降级，保证服务可用

## 文档输出

### 主文档
- 路径: `docs/zh/deployment/high-availability.md`
- 内容: 1468 行完整的高可用部署指南
- 包含:
  - 服务架构分析
  - 三种部署方案
  - 负载均衡配置（Nginx/HAProxy/K8s）
  - 数据库高可用方案
  - 缓存高可用方案
  - 监控告警配置
  - 故障恢复流程
  - 性能优化建议
  - 部署检查清单

### 特色内容
- ✅ 完整的 Nginx/HAProxy 配置示例
- ✅ Kubernetes Deployment/StatefulSet/HPA 配置
- ✅ PostgreSQL Patroni 高可用配置
- ✅ Redis Sentinel/Cluster 配置
- ✅ Prometheus/Grafana 监控配置
- ✅ 故障场景与应对流程
- ✅ 性能优化建议
- ✅ 部署检查清单

## 实施建议

### 最小高可用配置
```
✅ AxonHub 实例: 3 个
✅ 数据库: PostgreSQL 主 + 1 从（Patroni）
✅ 负载均衡: Nginx
✅ 监控: Prometheus + Grafana
✅ 备份: 每日自动备份
```

### 生产推荐配置
```
✅ AxonHub 实例: 5-10 个（支持自动扩缩容）
✅ 数据库: TiDB Cloud Dedicated（跨区域）
✅ 缓存: Redis Cluster（6 节点）
✅ 负载均衡: HAProxy + GeoDNS
✅ 监控: Prometheus + Grafana + AlertManager
✅ 日志: ELK Stack / Grafana Loki
✅ 备份: 每日+每周+每月备份策略
```

## 总结

AxonHub 的架构设计非常适合高可用部署：

1. **核心服务完全无状态** - 这是最大的优势，意味着可以轻松实现水平扩展
2. **唯一的有状态服务是数据库** - 有成熟的高可用方案可选
3. **缓存是可选的** - 即使缓存不可用，核心功能也不受影响
4. **自动故障恢复** - 负载均衡器自动检测和恢复故障实例
5. **灵活的部署选项** - 从单机到分布式，从 VM 到 Kubernetes

这使得 AxonHub 能够轻松实现 99.9% 甚至 99.99% 的可用性。
