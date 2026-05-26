# AxonHub 高可用部署架构

本文档详细介绍 AxonHub 的高可用部署方案，包括服务架构分析、无状态服务识别、部署拓扑设计和配置最佳实践。

## 目录

- [架构概览](#架构概览)
- [服务架构分析](#服务架构分析)
- [高可用部署方案](#高可用部署方案)
- [负载均衡配置](#负载均衡配置)
- [数据库高可用](#数据库高可用)
- [缓存高可用](#缓存高可用)
- [监控与告警](#监控与告警)
- [故障恢复](#故障恢复)
- [性能优化建议](#性能优化建议)

---

## 架构概览

AxonHub 是一个 All-in-one AI 开发平台，采用微服务架构设计，提供统一的 AI API 网关、项目管理和全面的开发工具。系统通过转换器管道架构实现多 AI 提供商的统一接入。

### 核心特性

- **统一 API**: 兼容 OpenAI、Anthropic 和 AI SDK 的接口
- **转换管道**: 双向数据转换，支持多种 AI 提供商
- **线程追踪**: 完整的请求链路追踪和监控
- **权限控制**: 基于 RBAC 的细粒度权限管理
- **负载均衡**: 智能多策略负载均衡和自动故障转移

### 技术栈

- **后端**: Go 1.25+, Gin HTTP 框架, Ent ORM, gqlgen GraphQL
- **前端**: React 19, TypeScript, TanStack Router
- **数据库**: SQLite (开发), PostgreSQL/MySQL/TiDB (生产)
- **缓存**: 内存缓存 / Redis (可选)
- **依赖注入**: Uber FX
- **日志**: Zap 结构化日志

---

## 服务架构分析

### 无状态服务

AxonHub 的核心服务是**完全无状态**的，这是实现高可用的关键优势。

#### 1. AxonHub 主服务

**组件说明**:
- HTTP API Server (Gin 框架)
- GraphQL API (gqlgen)
- REST API (OpenAI/Anthropic 兼容)
- LLM 转换器管道
- 业务逻辑层 (biz)
- 中间件层 (认证、追踪、CORS)

**无状态特性**:
- ✅ **无会话状态**: 所有请求独立处理，不依赖本地会话
- ✅ **无本地缓存依赖**: 可选的缓存通过 Redis 共享
- ✅ **无文件系统依赖**: 静态资源编译进二进制文件
- ✅ **JWT 认证**: 无状态的 Token 认证机制
- ✅ **数据库持久化**: 所有状态存储在外部数据库

**可扩展性**:
```
水平扩展能力: ⭐⭐⭐⭐⭐ (5/5)
- 可任意增加实例数量
- 无需状态同步
- 无会话粘性要求
```

#### 2. API Gateway 层

**组件说明**:
- 路由处理 (`routes.go`)
- 请求转发和负载均衡
- 认证和授权中间件
- 请求/响应日志
- CORS 处理

**无状态特性**:
- ✅ **纯转发逻辑**: 不保存任何请求状态
- ✅ **中间件链**: 每个请求独立通过中间件链
- ✅ **追踪 ID 传递**: 通过 HTTP 头传递，不存储本地

#### 3. LLM 转换器管道

**组件说明**:
- Inbound Transformer (请求转换)
- Outbound Transformer (响应转换)
- 流式处理支持 (SSE)
- 支持的提供商: OpenAI, Anthropic, Gemini, DeepSeek, AI SDK

**无状态特性**:
- ✅ **即时转换**: 请求到达时实时转换
- ✅ **流式处理**: 使用 SSE 无状态流传输
- ✅ **无管道缓存**: 转换结果直接返回客户端

#### 4. GraphQL API

**组件说明**:
- GraphQL 查询和变更
- 实时订阅 (WebSocket)
- 数据加载器优化

**无状态特性**:
- ✅ **查询即时响应**: 每次查询直接从数据库获取
- ✅ **无查询缓存**: 可选的缓存通过 Redis 共享

### 有状态服务

#### 1. 数据库

**支持的数据库**:
- SQLite 3.0+ (开发/小型部署)
- PostgreSQL 15+ (生产环境推荐)
- MySQL 8.0+ (生产环境)
- TiDB V8.0+ (分布式场景)
- TiDB Cloud (Serverless)
- Neon DB (Serverless)

**状态类型**:
- 用户和角色数据
- API 密钥和项目
- 渠道配置
- 请求日志和追踪
- 用量统计

**高可用要求**: 必须

#### 2. 缓存 (可选)

**支持的缓存模式**:
```yaml
cache:
  mode: "memory"      # 仅内存 (不推荐生产环境)
  mode: "redis"       # 仅 Redis
  mode: "two-level"   # 二级缓存: 内存 + Redis
```

**缓存内容**:
- 渠道信息 (短期缓存 5s)
- 模型映射
- 权限验证结果

**状态类型**: 临时状态，可丢失
**高可用要求**: 推荐但非必需

---

## 高可用部署方案

### 方案一: 单区域高可用 (推荐起步)

适用于中小型部署，单个数据中心内的高可用。

```
┌─────────────────────────────────────────────────────────┐
│                    负载均衡器 (Nginx/HAProxy)              │
│                    HTTP/HTTPS 443                         │
└────────────┬────────────────────────────────────────────┘
             │
    ┌────────┴─────────┬──────────────┬─────────────┐
    │                  │              │             │
┌───▼────┐      ┌──────▼───┐   ┌─────▼────┐  ┌────▼─────┐
│AxonHub │      │AxonHub   │   │AxonHub   │  │AxonHub   │
│实例 1   │      │实例 2     │   │实例 3     │  │实例 N     │
│无状态   │      │无状态     │   │无状态     │  │无状态     │
└───┬────┘      └──────┬───┘   └─────┬────┘  └────┬─────┘
    │                  │              │             │
    └────────┬─────────┴──────────────┴─────────────┘
             │
    ┌────────▼──────────────────────────────────────┐
    │                                                │
    │  数据库集群 (PostgreSQL/MySQL/TiDB)            │
    │  - 主从复制 (Master-Slave)                    │
    │  - 自动故障转移                                │
    │                                                │
    └────────────────────────────────────────────────┘
             │
    ┌────────▼──────────────────────────────────────┐
    │                                                │
    │  Redis 集群 (可选)                             │
    │  - Sentinel 模式                               │
    │  - 自动故障转移                                │
    │                                                │
    └────────────────────────────────────────────────┘
```

**特点**:
- AxonHub 服务: 3+ 实例
- 数据库: 主从架构
- Redis: Sentinel 高可用
- 单点故障: 无 (所有组件冗余)

### 方案二: 多区域高可用 (推荐生产)

适用于大型生产环境，跨数据中心/可用区部署。

```
                      ┌─────────────────────┐
                      │   全局负载均衡 (GLB)  │
                      │   (GeoDNS/CDN)       │
                      └──────────┬──────────┘
                                 │
               ┌─────────────────┴─────────────────┐
               │                                   │
    ┌──────────▼──────────┐            ┌──────────▼──────────┐
    │   可用区 A            │            │   可用区 B            │
    │                     │            │                     │
    │  ┌───────────────┐  │            │  ┌───────────────┐  │
    │  │  LB (Nginx)   │  │            │  │  LB (Nginx)   │  │
    │  └───────┬───────┘  │            │  └───────┬───────┘  │
    │          │          │            │          │          │
    │  ┌───────┴────────┐ │            │  ┌───────┴────────┐ │
    │  │AxonHub实例x3   │ │            │  │AxonHub实例x3   │ │
    │  │(无状态)        │ │            │  │(无状态)        │ │
    │  └───────┬────────┘ │            │  └───────┬────────┘ │
    │          │          │            │          │          │
    │  ┌───────▼────────┐ │            │  ┌───────▼────────┐ │
    │  │ Redis节点      │ │            │  │ Redis节点      │ │
    │  │ (Sentinel)     │ │            │  │ (Sentinel)     │ │
    │  └────────────────┘ │            │  └────────────────┘ │
    │                     │            │                     │
    └─────────┬───────────┘            └─────────┬───────────┘
              │                                  │
              └────────────┬─────────────────────┘
                           │
                ┌──────────▼───────────┐
                │  分布式数据库集群      │
                │  (TiDB Cluster)      │
                │  - PD × 3            │
                │  - TiKV × 3+         │
                │  - TiDB × 2+         │
                │  跨区域部署           │
                └──────────────────────┘
```

**特点**:
- 跨可用区部署
- 每个区域独立运行
- 数据库使用分布式方案 (TiDB)
- Redis 跨区域主从复制
- 自动故障转移到健康区域

### 方案三: 容器化部署 (Kubernetes)

适用于云原生环境，自动化运维。

```yaml
# Kubernetes 部署拓扑
┌──────────────────────────────────────────────────────────┐
│                    Kubernetes 集群                         │
│                                                            │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Ingress Controller                     │  │
│  │              (Nginx/Traefik)                        │  │
│  └──────────────────┬─────────────────────────────────┘  │
│                     │                                     │
│  ┌──────────────────▼─────────────────────────────────┐  │
│  │          AxonHub Service (ClusterIP)               │  │
│  └──────────────────┬─────────────────────────────────┘  │
│                     │                                     │
│  ┌──────────────────▼─────────────────────────────────┐  │
│  │          AxonHub Deployment                        │  │
│  │          - Replicas: 3+                            │  │
│  │          - Rolling Update                          │  │
│  │          - Resource Limits                         │  │
│  │          - Liveness/Readiness Probes               │  │
│  │                                                    │  │
│  │  ┌─────┐  ┌─────┐  ┌─────┐  ┌─────┐             │  │
│  │  │ Pod │  │ Pod │  │ Pod │  │ Pod │   ...       │  │
│  │  └─────┘  └─────┘  └─────┘  └─────┘             │  │
│  └────────────────────────────────────────────────────┘  │
│                                                            │
│  ┌────────────────────────────────────────────────────┐  │
│  │          PostgreSQL StatefulSet                    │  │
│  │          - Primary + Replicas                      │  │
│  │          - PVC (Persistent Volume)                 │  │
│  └────────────────────────────────────────────────────┘  │
│                                                            │
│  ┌────────────────────────────────────────────────────┐  │
│  │          Redis StatefulSet (可选)                  │  │
│  │          - Sentinel 模式                            │  │
│  └────────────────────────────────────────────────────┘  │
│                                                            │
└──────────────────────────────────────────────────────────┘
```

---

## 负载均衡配置

### Nginx 负载均衡

#### 基础配置

```nginx
# /etc/nginx/nginx.conf

upstream axonhub_backend {
    # 负载均衡算法
    least_conn;  # 最少连接数 (推荐)
    # ip_hash;   # IP 哈希 (会话保持，但 AxonHub 无状态不需要)
    
    # 后端服务器列表
    server axonhub-1:8090 max_fails=3 fail_timeout=30s weight=1;
    server axonhub-2:8090 max_fails=3 fail_timeout=30s weight=1;
    server axonhub-3:8090 max_fails=3 fail_timeout=30s weight=1;
    
    # 长连接池
    keepalive 32;
    keepalive_requests 100;
    keepalive_timeout 60s;
}

server {
    listen 80;
    listen 443 ssl http2;
    server_name api.yourdomain.com;

    # SSL 配置
    ssl_certificate /etc/nginx/ssl/cert.pem;
    ssl_certificate_key /etc/nginx/ssl/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    
    # 健康检查 (需要 nginx-plus 或使用外部健康检查)
    location /health {
        access_log off;
        proxy_pass http://axonhub_backend/health;
        proxy_set_header Host $host;
        
        # 快速超时用于健康检查
        proxy_connect_timeout 2s;
        proxy_send_timeout 2s;
        proxy_read_timeout 2s;
    }
    
    # API 端点
    location / {
        proxy_pass http://axonhub_backend;
        proxy_http_version 1.1;
        
        # 保持连接
        proxy_set_header Connection "";
        
        # 传递客户端信息
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 流式响应支持 (重要: AI 生成常用流式)
        proxy_buffering off;
        proxy_cache off;
        
        # 超时设置 (AI 请求可能较长)
        proxy_connect_timeout 60s;
        proxy_send_timeout 60s;
        proxy_read_timeout 600s;
        
        # 错误处理
        proxy_next_upstream error timeout http_502 http_503 http_504;
        proxy_next_upstream_tries 2;
    }
}
```

#### 高级配置: 限流和熔断

```nginx
# 限流配置
http {
    # 限流区域定义
    limit_req_zone $binary_remote_addr zone=api_limit:10m rate=100r/s;
    limit_req_zone $binary_remote_addr zone=login_limit:10m rate=5r/s;
    
    # 连接限制
    limit_conn_zone $binary_remote_addr zone=addr:10m;
    
    upstream axonhub_backend {
        least_conn;
        server axonhub-1:8090 max_fails=3 fail_timeout=30s;
        server axonhub-2:8090 max_fails=3 fail_timeout=30s;
        server axonhub-3:8090 max_fails=3 fail_timeout=30s;
        keepalive 32;
    }
    
    server {
        listen 443 ssl http2;
        
        # 全局限流
        location / {
            limit_req zone=api_limit burst=200 nodelay;
            limit_conn addr 10;
            proxy_pass http://axonhub_backend;
        }
        
        # 登录端点特殊限流
        location /auth/login {
            limit_req zone=login_limit burst=10 nodelay;
            proxy_pass http://axonhub_backend;
        }
        
        # AI API 端点 (更宽松的限制)
        location /v1/ {
            limit_req zone=api_limit burst=500 nodelay;
            proxy_pass http://axonhub_backend;
            proxy_buffering off;  # 支持流式响应
            proxy_read_timeout 600s;  # AI 生成超时
        }
    }
}
```

### HAProxy 负载均衡

```haproxy
# /etc/haproxy/haproxy.cfg

global
    log /dev/log local0
    log /dev/log local1 notice
    maxconn 4096
    user haproxy
    group haproxy
    daemon

defaults
    log     global
    mode    http
    option  httplog
    option  dontlognull
    timeout connect 5000ms
    timeout client  600000ms  # AI 请求可能较长
    timeout server  600000ms

# 统计页面
listen stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 30s
    stats admin if TRUE

# AxonHub 前端
frontend axonhub_frontend
    bind *:80
    bind *:443 ssl crt /etc/haproxy/ssl/cert.pem
    
    # 重定向 HTTP 到 HTTPS
    redirect scheme https code 301 if !{ ssl_fc }
    
    # 健康检查端点
    acl is_health path /health
    
    # 默认后端
    default_backend axonhub_backend

# AxonHub 后端
backend axonhub_backend
    balance leastconn  # 最少连接算法
    
    # 健康检查
    option httpchk GET /health
    http-check expect status 200
    
    # 后端服务器
    server axonhub-1 axonhub-1:8090 check inter 5s fall 3 rise 2
    server axonhub-2 axonhub-2:8090 check inter 5s fall 3 rise 2
    server axonhub-3 axonhub-3:8090 check inter 5s fall 3 rise 2
    
    # HTTP 选项
    option http-server-close
    option forwardfor
    http-request set-header X-Forwarded-Proto https if { ssl_fc }
```

### Kubernetes Ingress

```yaml
# axonhub-ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: axonhub-ingress
  namespace: axonhub
  annotations:
    # Nginx Ingress 配置
    nginx.ingress.kubernetes.io/proxy-body-size: "50m"
    nginx.ingress.kubernetes.io/proxy-connect-timeout: "60"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "600"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "600"
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
    
    # SSL 配置
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    
    # 限流
    nginx.ingress.kubernetes.io/limit-rps: "100"
    nginx.ingress.kubernetes.io/limit-connections: "10"
    
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - api.yourdomain.com
      secretName: axonhub-tls
  rules:
    - host: api.yourdomain.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: axonhub-service
                port:
                  number: 8090
```

---

## 数据库高可用

### PostgreSQL 高可用

#### 方案一: Streaming Replication (流复制)

```yaml
架构:
  Primary (主节点)
    ↓ 实时复制
  Standby-1 (从节点 1) → 可读
  Standby-2 (从节点 2) → 可读

工具: Patroni + etcd/Consul + HAProxy
```

**Patroni 配置示例**:

```yaml
# /etc/patroni/patroni.yml
scope: axonhub
namespace: /db/
name: postgresql-1

restapi:
  listen: 0.0.0.0:8008
  connect_address: postgresql-1:8008

etcd:
  hosts: etcd-1:2379,etcd-2:2379,etcd-3:2379

bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 1048576
    postgresql:
      use_pg_rewind: true
      parameters:
        max_connections: 200
        shared_buffers: 256MB
        effective_cache_size: 1GB
        maintenance_work_mem: 64MB
        checkpoint_completion_target: 0.9
        wal_buffers: 16MB
        default_statistics_target: 100
        random_page_cost: 1.1
        effective_io_concurrency: 200
        work_mem: 1310kB
        min_wal_size: 1GB
        max_wal_size: 4GB

postgresql:
  listen: 0.0.0.0:5432
  connect_address: postgresql-1:5432
  data_dir: /var/lib/postgresql/15/main
  pgpass: /tmp/pgpass
  authentication:
    replication:
      username: replicator
      password: rep_password
    superuser:
      username: postgres
      password: postgres_password
  parameters:
    unix_socket_directories: '/var/run/postgresql'
```

**AxonHub 连接配置**:

```yaml
# config.yml
db:
  dialect: "postgres"
  # 使用 HAProxy 连接（自动路由到主节点）
  dsn: "postgres://axonhub:password@haproxy:5432/axonhub?sslmode=disable"
```

#### 方案二: Cloud Managed 数据库

**TiDB Cloud (推荐)**:

```yaml
# 优势
- Serverless 和 Dedicated 两种模式
- 自动故障转移
- 跨区域部署
- 自动备份和恢复
- MySQL 协议兼容

# AxonHub 配置
db:
  dialect: "tidb"
  dsn: "<USER>.root:<PASSWORD>@tcp(gateway01.us-west-2.prod.aws.tidbcloud.com:4000)/axonhub?tls=true"
```

**AWS RDS PostgreSQL**:

```yaml
# 优势
- Multi-AZ 部署
- 自动故障转移 (60-120 秒)
- 自动备份
- 只读副本
- 性能监控

# AxonHub 配置
db:
  dialect: "postgres"
  dsn: "postgres://axonhub:password@axonhub-cluster.xxxxx.rds.amazonaws.com:5432/axonhub?sslmode=require"
```

**Neon DB (Serverless)**:

```yaml
# 优势
- Serverless PostgreSQL
- 按需付费
- 即时分支和克隆
- 自动扩缩容

# AxonHub 配置
db:
  dialect: "postgres"
  dsn: "postgres://user:password@ep-xxx.us-east-2.aws.neon.tech/axonhub?sslmode=require"
```

### MySQL 高可用

#### 方案一: MySQL Group Replication

```yaml
架构:
  MySQL-1 (Primary)
    ↕ 双向复制
  MySQL-2 (Primary)
    ↕ 双向复制
  MySQL-3 (Primary)

特点:
- 多主模式
- 自动故障检测和转移
- 数据一致性保证
```

#### 方案二: MySQL + ProxySQL

```yaml
架构:
  ProxySQL (智能代理)
    ↓
  MySQL-Master (写)
    ↓ 复制
  MySQL-Slave-1 (读)
  MySQL-Slave-2 (读)

特点:
- 读写分离
- 连接池管理
- 查询缓存
- 自动故障转移
```

**AxonHub 配置** (连接到 ProxySQL):

```yaml
# config.yml
db:
  dialect: "mysql"
  dsn: "axonhub:password@tcp(proxysql:6033)/axonhub?charset=utf8mb4&parseTime=True"
```

---

## 缓存高可用

AxonHub 的缓存是**可选的**，主要用于提升性能。缓存不可用不会影响系统核心功能。

### Redis Sentinel 模式

**架构**:

```yaml
            ┌──────────────┐
            │  Redis-1     │
            │  (Master)    │
            └──────┬───────┘
                   │
       ┌───────────┴───────────┐
       ↓                       ↓
┌──────────────┐        ┌──────────────┐
│  Redis-2     │        │  Redis-3     │
│  (Replica)   │        │  (Replica)   │
└──────────────┘        └──────────────┘
       ↑                       ↑
       │                       │
┌──────┴────────┬──────────────┴──────┐
│ Sentinel-1    │ Sentinel-2   │ Sentinel-3 │
│ (Monitor)     │ (Monitor)    │ (Monitor)  │
└───────────────┴──────────────┴────────────┘
```

**Redis Sentinel 配置**:

```bash
# /etc/redis/sentinel.conf
port 26379
sentinel monitor axonhub-cache redis-1 6379 2
sentinel down-after-milliseconds axonhub-cache 5000
sentinel parallel-syncs axonhub-cache 1
sentinel failover-timeout axonhub-cache 10000
sentinel auth-pass axonhub-cache your_redis_password
```

**AxonHub 配置**:

```yaml
# config.yml
cache:
  mode: "redis"
  redis:
    # Sentinel 模式连接
    url: "redis://default:password@sentinel-1:26379,sentinel-2:26379,sentinel-3:26379/0?master=axonhub-cache"
    expiration: "30m"
```

### Redis Cluster 模式

适用于大规模数据缓存，支持自动分片。

```yaml
架构:
  Master-1  →  Replica-1
  Master-2  →  Replica-2
  Master-3  →  Replica-3

特点:
- 数据自动分片
- 高可用性
- 水平扩展
```

### 二级缓存 (推荐)

```yaml
# config.yml
cache:
  mode: "two-level"  # 内存 + Redis 二级缓存
  memory:
    expiration: "5s"          # 内存缓存 5 秒
    cleanup_interval: "10m"
  redis:
    url: "redis://redis-sentinel:6379/0"
    expiration: "30m"         # Redis 缓存 30 分钟
```

**优势**:
- 极速本地缓存 (5 秒)
- Redis 跨实例共享
- 降低 Redis 负载
- 即使 Redis 不可用，本地缓存仍可用

---

## 监控与告警

### 健康检查端点

AxonHub 提供内置健康检查端点:

```bash
# 健康检查
GET /health

# 响应
HTTP/1.1 200 OK
Content-Type: text/plain

healthy
```

**集成到负载均衡器**:
- Nginx: `proxy_pass http://axonhub_backend/health;`
- HAProxy: `option httpchk GET /health`
- Kubernetes: `livenessProbe` 和 `readinessProbe`

### Kubernetes 探针配置

```yaml
# axonhub-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: axonhub
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: axonhub
          image: looplj/axonhub:latest
          ports:
            - containerPort: 8090
          
          # 存活探针: 检测服务是否存活
          livenessProbe:
            httpGet:
              path: /health
              port: 8090
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          
          # 就绪探针: 检测服务是否就绪接收流量
          readinessProbe:
            httpGet:
              path: /health
              port: 8090
            initialDelaySeconds: 10
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3
          
          # 启动探针: 检测服务是否启动完成
          startupProbe:
            httpGet:
              path: /health
              port: 8090
            initialDelaySeconds: 5
            periodSeconds: 5
            failureThreshold: 30  # 最多 150 秒启动时间
          
          resources:
            requests:
              memory: "256Mi"
              cpu: "250m"
            limits:
              memory: "1Gi"
              cpu: "1000m"
```

### Prometheus 监控

AxonHub 支持可选的 Prometheus 指标导出。

**配置启用**:

```yaml
# config.yml
metrics:
  enabled: true
  exporter:
    type: "oltphttp"           # 或 "prometheus"
    endpoint: "localhost:8080"
    insecure: true
```

**关键指标**:
- HTTP 请求总数
- 请求延迟分布
- 错误率
- 数据库连接数
- 缓存命中率
- 渠道请求成功率
- AI 提供商响应时间

**Prometheus 配置**:

```yaml
# prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'axonhub'
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names:
            - axonhub
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app]
        action: keep
        regex: axonhub
      - source_labels: [__meta_kubernetes_pod_name]
        target_label: instance
```

### Grafana 仪表板

推荐监控面板:

1. **系统概览**
   - QPS (每秒查询数)
   - 平均响应时间
   - 错误率
   - 实例状态

2. **AI 提供商监控**
   - 各渠道请求分布
   - 渠道成功率
   - 渠道响应时间
   - 渠道故障转移次数

3. **数据库监控**
   - 连接数
   - 查询时间
   - 慢查询
   - 死锁

4. **缓存监控**
   - 缓存命中率
   - 缓存大小
   - Redis 连接数

### 告警规则

**Prometheus AlertManager 规则**:

```yaml
# alerts.yml
groups:
  - name: axonhub
    interval: 30s
    rules:
      # 实例不可用
      - alert: AxonHubInstanceDown
        expr: up{job="axonhub"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "AxonHub 实例 {{ $labels.instance }} 不可用"
          description: "实例已经下线超过 1 分钟"
      
      # 高错误率
      - alert: AxonHubHighErrorRate
        expr: rate(http_requests_total{status=~"5.."}[5m]) > 0.05
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "AxonHub 错误率过高"
          description: "5xx 错误率超过 5%"
      
      # 高响应时间
      - alert: AxonHubHighLatency
        expr: histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m])) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "AxonHub 响应时间过长"
          description: "P95 响应时间超过 5 秒"
      
      # 数据库连接池耗尽
      - alert: DatabaseConnectionPoolExhausted
        expr: db_connections_in_use / db_connections_max > 0.9
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "数据库连接池即将耗尽"
          description: "使用率超过 90%"
```

### 日志聚合

**推荐方案**: ELK Stack 或 Grafana Loki

```yaml
# config.yml - 日志配置
log:
  level: "info"
  encoding: "json"  # JSON 格式方便日志聚合
  output: "file"
  file:
    path: "/var/log/axonhub/axonhub.log"
    max_size: 100    # MB
    max_age: 30      # 天
    max_backups: 10
```

**Filebeat 收集日志**:

```yaml
# filebeat.yml
filebeat.inputs:
  - type: log
    enabled: true
    paths:
      - /var/log/axonhub/*.log
    json.keys_under_root: true
    json.add_error_key: true
    fields:
      app: axonhub
      env: production

output.elasticsearch:
  hosts: ["elasticsearch:9200"]
  index: "axonhub-logs-%{+yyyy.MM.dd}"
```

---

## 故障恢复

### 故障场景与应对

#### 场景 1: 单个 AxonHub 实例故障

**现象**:
- 实例健康检查失败
- 负载均衡器自动摘除实例
- 流量转发到其他健康实例

**恢复时间**: < 10 秒 (自动)

**应对**:
1. 负载均衡器检测到故障 (5 秒)
2. 自动停止转发流量到故障实例
3. 其他实例继续服务
4. 无需人工干预

**预防**:
- 至少部署 3 个实例
- 配置合理的健康检查间隔
- 设置资源限制防止 OOM

#### 场景 2: 数据库主节点故障

**现象**:
- 数据库写入失败
- AxonHub 实例报错
- Patroni 检测到主节点下线

**恢复时间**: 30-60 秒 (自动)

**应对**:
1. Patroni 检测到主节点故障
2. 自动选举新的主节点
3. 从节点提升为主节点
4. AxonHub 重新连接新主节点
5. 服务恢复

**预防**:
- 使用 Patroni 管理 PostgreSQL
- 配置多个从节点
- 定期备份数据库

#### 场景 3: Redis 缓存故障

**现象**:
- 缓存读写失败
- AxonHub 降级到直接查询数据库

**影响**: 性能下降，但功能正常

**恢复时间**: 无需恢复 (降级运行)

**应对**:
1. AxonHub 检测到 Redis 不可用
2. 自动降级: 跳过缓存，直接查询数据库
3. 性能略有下降但功能完整
4. Redis 恢复后自动重新连接

**预防**:
- 使用 Redis Sentinel 高可用
- 配置二级缓存 (内存 + Redis)
- 缓存设计为可选组件

#### 场景 4: 整个可用区故障

**现象**:
- 可用区 A 完全不可用
- 该区域所有服务宕机

**恢复时间**: 1-2 分钟 (自动)

**应对**:
1. 全局负载均衡器检测到可用区 A 不可用
2. 自动将所有流量切换到可用区 B
3. 可用区 B 继续服务
4. 可用区 A 恢复后自动重新加入

**预防**:
- 多可用区部署
- 使用分布式数据库 (TiDB)
- 配置跨区域复制

### 灾难恢复 (DR)

#### 备份策略

**数据库备份**:

```bash
# 每日全量备份
0 2 * * * /usr/bin/pg_dump -h localhost -U postgres axonhub | gzip > /backup/axonhub-$(date +\%Y\%m\%d).sql.gz

# 保留策略
- 每日备份: 保留 7 天
- 每周备份: 保留 4 周
- 每月备份: 保留 12 月
```

**配置备份**:

```bash
# 备份 config.yml
cp /app/config.yml /backup/config-$(date +\%Y\%m\%d).yml
```

#### 恢复流程

**数据库恢复**:

```bash
# 1. 停止 AxonHub 服务
systemctl stop axonhub

# 2. 恢复数据库
gunzip < /backup/axonhub-20240101.sql.gz | psql -h localhost -U postgres axonhub

# 3. 验证数据
psql -h localhost -U postgres axonhub -c "SELECT COUNT(*) FROM users;"

# 4. 启动服务
systemctl start axonhub

# 5. 验证服务
curl http://localhost:8090/health
```

**完整系统恢复**:

```bash
# 1. 部署 AxonHub 新实例
docker-compose up -d

# 2. 恢复配置文件
cp /backup/config-20240101.yml /app/config.yml

# 3. 恢复数据库
# (见上述数据库恢复流程)

# 4. 验证系统
curl -H "Authorization: Bearer <token>" http://localhost:8090/v1/models
```

### 故障演练

**建议每季度进行故障演练**:

1. **实例故障演练**: 随机杀掉一个实例，验证自动恢复
2. **数据库故障演练**: 停止主数据库，验证自动故障转移
3. **全链路压测**: 模拟高负载，验证系统容量
4. **灾难恢复演练**: 从备份恢复完整系统

---

## 性能优化建议

### 1. AxonHub 实例优化

**资源配置**:

```yaml
# Docker Compose
services:
  axonhub:
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 2G
        reservations:
          cpus: '1'
          memory: 1G
```

**Go 运行时优化**:

```bash
# 环境变量
GOMAXPROCS=4          # CPU 核心数
GOGC=100              # GC 触发阈值 (默认 100)
GOMEMLIMIT=2GiB       # 内存限制
```

### 2. 数据库优化

**PostgreSQL 配置**:

```ini
# postgresql.conf
max_connections = 200
shared_buffers = 4GB
effective_cache_size = 12GB
maintenance_work_mem = 1GB
checkpoint_completion_target = 0.9
wal_buffers = 16MB
default_statistics_target = 100
random_page_cost = 1.1
effective_io_concurrency = 200
work_mem = 20971kB
min_wal_size = 2GB
max_wal_size = 8GB
max_worker_processes = 8
max_parallel_workers_per_gather = 4
max_parallel_workers = 8
```

**索引优化**:

```sql
-- 常用查询索引
CREATE INDEX idx_requests_project_id ON requests(project_id);
CREATE INDEX idx_requests_created_at ON requests(created_at);
CREATE INDEX idx_usage_logs_project_id_created_at ON usage_logs(project_id, created_at);
CREATE INDEX idx_api_keys_project_id ON api_keys(project_id);
```

### 3. 缓存优化

**缓存策略**:

```yaml
# config.yml
cache:
  mode: "two-level"
  memory:
    expiration: "5s"         # 渠道信息短期缓存
    cleanup_interval: "1m"
  redis:
    expiration: "30m"        # 模型映射长期缓存
```

**缓存预热**:

```bash
# 启动时预加载常用数据
- 渠道列表
- 模型映射
- 权限规则
```

### 4. 网络优化

**连接池配置**:

```yaml
# AxonHub 内部配置
数据库连接池: 
  最小连接数: 10
  最大连接数: 100
  连接最大空闲时间: 5m
  连接最大生命周期: 30m
```

**Keep-Alive**:

```nginx
# Nginx 配置
keepalive_timeout 65s;
keepalive_requests 100;

upstream axonhub_backend {
    keepalive 32;  # 与后端保持 32 个长连接
}
```

### 5. 并发优化

**推荐实例配置**:

| 负载级别 | 实例数 | CPU | 内存 | 并发请求 |
|---------|--------|-----|------|----------|
| 低负载   | 2-3    | 1核  | 1GB  | < 100    |
| 中负载   | 3-5    | 2核  | 2GB  | 100-500  |
| 高负载   | 5-10   | 4核  | 4GB  | 500-2000 |
| 超高负载 | 10+    | 8核  | 8GB  | 2000+    |

**自动扩缩容 (Kubernetes)**:

```yaml
# hpa.yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: axonhub-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: axonhub
  minReplicas: 3
  maxReplicas: 20
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: 80
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 300  # 5 分钟稳定期
      policies:
        - type: Percent
          value: 50
          periodSeconds: 60
    scaleUp:
      stabilizationWindowSeconds: 60   # 1 分钟稳定期
      policies:
        - type: Percent
          value: 100
          periodSeconds: 60
        - type: Pods
          value: 2
          periodSeconds: 60
      selectPolicy: Max
```

---

## 部署检查清单

### 上线前检查

- [ ] **AxonHub 实例**
  - [ ] 至少 3 个实例
  - [ ] 健康检查配置正确
  - [ ] 资源限制已设置
  - [ ] 环境变量已配置

- [ ] **数据库**
  - [ ] 主从复制已配置
  - [ ] 自动故障转移已启用
  - [ ] 备份策略已设置
  - [ ] 连接池已优化

- [ ] **负载均衡器**
  - [ ] 健康检查已配置
  - [ ] SSL 证书已安装
  - [ ] 超时时间已调整 (支持流式响应)
  - [ ] 限流规则已设置

- [ ] **缓存 (可选)**
  - [ ] Redis Sentinel 已配置
  - [ ] 二级缓存已启用
  - [ ] 故障降级已验证

- [ ] **监控告警**
  - [ ] Prometheus 已部署
  - [ ] Grafana 仪表板已创建
  - [ ] 告警规则已配置
  - [ ] 日志聚合已设置

- [ ] **备份恢复**
  - [ ] 自动备份已配置
  - [ ] 恢复流程已验证
  - [ ] 备份存储空间充足

- [ ] **安全**
  - [ ] HTTPS 已启用
  - [ ] 数据库连接加密
  - [ ] API 密钥轮换策略
  - [ ] 防火墙规则已配置

### 日常运维检查

- [ ] **每日检查**
  - [ ] 所有实例健康
  - [ ] 错误率 < 1%
  - [ ] 响应时间正常
  - [ ] 备份成功

- [ ] **每周检查**
  - [ ] 数据库性能
  - [ ] 缓存命中率
  - [ ] 磁盘空间
  - [ ] 慢查询日志

- [ ] **每月检查**
  - [ ] 安全补丁更新
  - [ ] 证书有效期
  - [ ] 备份恢复演练
  - [ ] 容量规划

---

## 总结

### 无状态服务优势

AxonHub 的核心服务是**完全无状态**的，这带来了以下优势:

1. **水平扩展简单**: 只需增加实例数，无需状态同步
2. **快速故障恢复**: 实例故障不影响其他实例，秒级恢复
3. **滚动更新友好**: 可无缝升级，不中断服务
4. **负载均衡灵活**: 支持任意负载均衡算法，无会话粘性
5. **资源利用高效**: 可根据负载动态调整实例数

### 推荐部署方案

| 场景 | 推荐方案 | AxonHub 实例 | 数据库 | 缓存 |
|-----|---------|-------------|--------|------|
| **开发环境** | Docker Compose | 1 实例 | SQLite | 内存 |
| **小型生产** | VM + Docker | 2-3 实例 | PostgreSQL 主从 | Redis Sentinel |
| **中型生产** | Kubernetes | 3-5 实例 | TiDB Cloud / RDS | Redis Cluster |
| **大型生产** | Kubernetes 多区域 | 10+ 实例 | TiDB Cluster | Redis Cluster |

### 关键要点

1. **AxonHub 服务是无状态的**，可任意水平扩展
2. **数据库是唯一的有状态服务**，需要高可用配置
3. **缓存是可选的**，不可用不影响核心功能
4. **至少部署 3 个实例**以实现高可用
5. **使用健康检查**进行自动故障检测
6. **配置监控告警**以快速发现问题
7. **定期备份**并验证恢复流程
8. **进行故障演练**以验证高可用方案

---

## 参考资源

- [AxonHub 官方文档](https://deepwiki.com/looplj/axonhub)
- [配置参考](../deployment/configuration.md)
- [Docker 部署](../deployment/docker.md)
- [性能优化指南](../guides/performance.md)
- [监控指南](../guides/monitoring.md)

---

**最后更新**: 2026-01-22