# API-in-one

多协议 LLM API 网关，统一 OpenAI / Claude / Gemini 等多路提供商，支持加权轮询、熔断、热重载。

## 快速开始

```bash
# 1. 修改配置
cp config.yaml config.local.yaml && vi config.local.yaml

# 2. 直接运行
CONFIG_PATH=config.local.yaml go run .

# 3. 或 Docker
docker compose up -d
```

## 配置说明

```yaml
server:
  port: 3000
  admin_key: "your-admin-key"           # 管理后台密钥
  access_keys:                          # 客户端调用密钥
    - key: sk-xxx
      allowed_models: []                # 空 = 允许所有
      excluded_models: []
      expires_at: ""                    # RFC3339, 空 = 不过期
  key_failure_threshold: 3              # 单 Key 连续失败次数，超限自动暂停
  key_failure_cooldown_seconds: 600     # 自动恢复时间
  channel_model_failure_threshold: 3    # 单模型连续失败次数，超限自动暂停

channels:
  - name: my-provider
    type: openai                        # openai | claude | gemini
    base_url: https://api.openai.com/v1
    keys: [sk-xxx]
    models: [gpt-4o, gpt-4o-mini]
    model_mapping:                      # 别名映射
      my-model: gpt-4o
    priority: 10                        # 越小越优先
    weight: 100                         # 同优先级下的流量权重
    enabled: true
```

## 路由一览

| 路径 | 说明 |
|------|------|
| `/` | 管理后台 SPA |
| `/admin` | 同上 |
| `GET /v1/models` | 模型列表（公开） |
| `POST /v1/chat/completions` | OpenAI Chat Completions |
| `POST /v1/messages` | Claude Messages（自动转换） |
| `POST /v1/responses` | OpenAI Responses API |
| `POST /v1beta/models/:model` | Gemini Generate |
| `GET /admin/channels` | 渠道列表 |
| `POST /admin/channels` | 创建渠道 |
| `DELETE /admin/channels/by-name?name=` | 删除渠道 |
| `POST /admin/channels/reload` | 热重载配置 |
| `GET /admin/settings` | 获取设置 |
| `PUT /admin/access-keys` | 更新客户端密钥 |
| `GET /admin/stats` | 请求统计 |
| `GET /admin/logs` | 请求日志 |
| `DELETE /admin/logs` | 清空日志 |

## 管理后台

打开 `http://localhost:3000`，输入 `admin_key` 登录。

支持：渠道 CRUD、密钥管理（含单 Key 熔断）、模型别名映射、请求日志查看与导出、系统提示词注入、单 Key 作用域控制。

## 部署

```bash
# 构建前端
cd web && npm run build && cd ..

# 构建二进制
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o api-in-one .

# 部署到服务器
# 替换二进制，web/ 目录放到工作目录下
# service api-in-one restart  # systemd
# 或 docker compose up -d
```
