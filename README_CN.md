# JoyCode 插件

CLIProxyAPI 的 JoyCode（京东智能编码助手）上游提供商插件，通过 JoyCode 的 OpenAI 兼容 chat completions 端点路由请求。

## 功能

- **执行器**: 向 JoyCode API (`https://joycode-api.jd.com`) 发送 chat-completions 请求
- **认证提供商**: 解析、验证和刷新 JoyCode 凭据 (ptKey)
- **模型提供商**: 列出可用的 JoyCode 模型（静态兜底 + 动态获取）
- **动态 loginType**: 从 auth metadata 中读取 loginType 而非硬编码，支持多种 JD 账号类型

## 支持的模型

| 模型 | 说明 |
|------|------|
| JoyAI-Code | JoyCode 默认编码模型 |
| GLM-5.1 | 智谱 GLM 5.1 |
| Kimi-K2.6 | 月之暗面 Kimi K2.6 |
| MiniMax-M2.7 | MiniMax M2.7 |
| Doubao-Seed-2.0-pro | 字节豆包 Seed 2.0 Pro |

当有效 ptKey 可用时，额外模型从 JoyCode 模型列表 API 动态获取。

## 认证文件格式

在 CPA 认证目录中创建 JSON 文件：

```json
{
  "type": "joycode",
  "ptKey": "你的-pt-key",
  "userId": "你的用户ID",
  "tenant": "JD",
  "orgFullName": "你的组织",
  "loginType": "N_PIN_PC"
}
```

可以放置多个认证文件实现多账号轮换，CPA 调度器会自动 round-robin。

## 配置

```yaml
plugins:
  enabled: true
  configs:
    joycode:
      enabled: true
```

## 构建

```bash
make build
```

Makefile 根据目标平台选择插件扩展名：

| GOOS | 输出 |
|------|------|
| `linux` / `freebsd` | `joycode.so` |
| `darwin` | `joycode.dylib` |
| `windows` | `joycode.dll` |

发布构建可注入运行时版本号：

```bash
make build VERSION=0.1.3
```

## 许可证

Apache License 2.0
