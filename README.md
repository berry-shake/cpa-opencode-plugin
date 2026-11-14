# CPA OpenCode Plugin

CLIProxyAPI（CPA）插件：把 [OpenCode](https://opencode.ai) Console 的 API Key（[OpenCode Go](https://opencode.ai/docs/go) 订阅或按量付费的 Zen）作为 CPA 认证文件接入，转发 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages。

- 每个 Key（`oc_sk_…`）是一个认证文件，和其他凭证一样参与 CPA 的选号、冷却和故障转移，可以单独设出口代理
- 自动拉取 OpenCode 模型目录注册到 CPA（带上下文、最大输出、输入类型、推理强度），默认模型名 `opencode/<模型 ID>`，也可以只注册选中的模型
- 请求原样转发，不做协议转换；OpenCode 自己支持三种协议，每个模型都能用；支持 `count_tokens`
- 自动带上 OpenCode Go 必需的会话头 `x-opencode-session`
- 查看工作区 Go 订阅的 5 小时 / 每周 / 每月额度，CPA 额度面板也能看
- 自带管理页：Key、模型和价格、出口代理、请求日志

> 社区插件，与 OpenCode 官方无关。

## 安装

需要 CLIProxyAPI v8 并开启动态插件（Unix 上 CPA 要用 cgo 编译）；支持 Linux / macOS（amd64、arm64）和 Windows amd64。API Key 在 [OpenCode Console](https://opencode.ai/auth) 的工作区里创建，用 Go 模型需要工作区订阅 OpenCode Go。

**插件商店**：在 CPA 配置里加上商店源，然后在管理面板的插件商店里安装 **OpenCode**。

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/berry-shake/CLIProxyAPI-Plugins-Store/main/registry.json
```

**手动安装**：从 [Release](https://github.com/berry-shake/cpa-opencode-plugin/releases) 下载对应平台的 zip，把里面的 `opencode.so`（macOS 是 `.dylib`，Windows 是 `.dll`）放到 `plugins/<系统>/<架构>/` 下，再在配置里打开 `plugins.configs.opencode.enabled`。配置里已有 `store.version` 时，CPA 只加载版本号对得上的文件，手动换文件要同时改 `store.version` 和 `release-tag`。

## 配置

都是可选项，写在 `plugins.configs.opencode` 下；填错的值回退默认并记一条警告。

```yaml
plugins:
  configs:
    opencode:
      enabled: true
      base-url: https://opencode.ai/inference/go  # 末尾的 /v1、/openai、/anthropic 会自动去掉
      model-prefix: opencode                      # 认证文件没写 prefix 时的默认前缀，"" = 不加
      models-refresh-minutes: 60                  # 模型目录轮询间隔，0 = 不轮询
      request-timeout-seconds: 600                # 非流式请求超时
      stream-idle-timeout-seconds: 300            # 流式响应多久没数据就断开
      data-dir: plugins/opencode-data             # 请求日志和代理列表的目录
      log-retention: 100000                       # 请求日志最多保留条数，0 = 不记录
      log-retention-days: 30                      # 请求日志保留天数，0 = 只按条数清理
```

`base-url` 决定计费方式，接口路径相同，模型目录按地址分别拉取：

| base-url | 模型 | 计费 |
|---|---|---|
| `https://opencode.ai/inference/go`（默认） | OpenCode Go 的模型 | Go 订阅额度 |
| `https://opencode.ai/inference` | Zen 的全部模型 | 按量付费，从工作区余额扣 |

只想让某个 Key 走另一种计费，就在它的认证文件里写 `base_url`。

## 认证文件

`type` 为 `opencode` 的认证文件归本插件管理；管理页添加的 Key 保存为 `opencode-<指纹>.json`。

```json
{
  "type": "opencode",
  "api_key": "<OpenCode API Key>",
  "label": "主账号",
  "models": ["kimi-k3", "glm-5.3"],
  "prefix": "opencode",
  "proxy_url": "socks5://127.0.0.1:1080"
}
```

| 字段 | 说明 |
|---|---|
| `api_key` | 必填 |
| `label` | 显示名称，默认用文件名 |
| `models` | 要注册的模型。不写或 `null` = 目录里全部对话模型，`[]` = 不注册 |
| `prefix` | 模型名前缀。不写用配置里的 `model-prefix`，`""` = 不加前缀 |
| `proxy_url` | 出口代理，支持 `http(s)://`、`socks5(h)://`，`direct` = 强制直连；不写时依次用 CPA 的 `proxy-url`、环境变量 |
| `base_url` | 覆盖配置里的 `base-url`，比如让这个 Key 走按量付费 |
| `priority` / `note` / `disabled` | CPA 通用字段 |

`model_revision` 由插件维护，不要手改。

> CPA 的 `routing.force-model-prefix` 默认关闭，这时模型会同时注册成 `opencode/kimi-k3` 和 `kimi-k3`，后者会和其他渠道的同名模型混在一起轮询。只想用带前缀的名字就把它打开（全局生效）。

## 工作方式

**模型**：每隔 `models-refresh-minutes` 拉一次 `<base-url>/v1/models`（不需要鉴权，不发 API Key）。「全部模型」只注册对话模型；模型或能力有变化才重新注册，只改价格不会；拉取失败保留上一次的目录。

**转发**（路径相对 `base-url`）：

| 客户端格式 | OpenCode 接口 |
|---|---|
| OpenAI Chat Completions | `POST /openai/v1/chat/completions` |
| OpenAI Responses | `POST /openai/v1/responses` |
| Anthropic Messages | `POST /anthropic/v1/messages`、`/anthropic/v1/messages/count_tokens` |

- 只改写请求体里的 `model`，其余原样转发；Chat 流式请求会自动加上 `stream_options.include_usage`
- 流式响应在第一帧真正的输出之前，上游报错（限流、过载）按普通错误处理，CPA 照常冷却或换 Key；流没收到结束帧就断了，记为 `stream_truncated`（502）

**会话头**：OpenCode Go 要求每个请求带 `x-opencode-session`（不带返回 400），同一会话也会路由到同一个上游，缓存命中更高。插件按顺序取会话 ID：

1. 客户端带的会话头，如 `x-opencode-session`、`x-claude-code-session-id`、`session_id`
2. 请求体里的 `prompt_cache_key`（Codex 等客户端会带）
3. Claude Code 在 `metadata.user_id` 里带的会话 ID
4. 都没有时，用系统提示词和第一条用户消息算一个哈希，同一个对话每一轮都相同

**上游错误**：

| OpenCode 返回 | 交给 CPA | 效果 |
|---|---|---|
| 401 / 402（余额不足）/ 403 | 原状态码 | 冷却这个 Key |
| 404（模型不存在） | 400 | 只失败这一次 |
| 429（Go 额度用完、预算到顶或限流） | 429，带 `Retry-After` | 按上游给的时间冷却 |
| 529 / 499 | 503 / 408 | 可重试 |

## Go 用量

插件查 `https://opencode.ai/zen/go/v1/usage`，得到 Go 订阅三个窗口的已用比例、是否被限流和重置时间：

| 窗口 | 说明 |
|---|---|
| 5 小时 | 滚动窗口，额度是月额度的 20% |
| 每周 | 每周一 00:00 UTC 重置，额度是月额度的 50% |
| 每月 | 按订阅日重置 |

- 用量是工作区级的，同一工作区的 Key 数字相同，汇总时只算一次
- 工作区没订阅 Go 时显示「未订阅 Go」，这样的 Key 仍然可以走按量付费地址用 Zen 模型
- OpenCode 没有查余额的接口，插件不显示余额
- 结果缓存 60 秒；查询按出口排队，同一个出口一次只查一个 Key

CPA 额度面板显示 **OpenCode Go** 一组，`5h` / `7d` / `monthly` 三条进度条，被限流的窗口显示为 0。

## 管理页

在 CPA 管理面板的插件菜单里点 **OpenCode**（`/v0/resource/plugins/opencode/manage`）。

- **凭据**：Key 卡片，添加、编辑、启停、删除，选模型，看 Go 用量
- **代理**：常用出口代理列表，编辑 Key 时直接选；可以检测出口 IP、地区和延迟
- **模型目录**：模型的输入类型、能力、上下文、最大输出、每百万 Token 价格，以及在 CPA 里注册的名字
- **请求日志**：每次调用的模型、状态、首字时间、耗时、Token 和速度，可以用 CPA 日志里的 8 位请求 ID 搜索；只记元数据，不记内容
- **配置概览**：当前生效的配置

请求日志里有两种速度，统计条取成功请求的中位数：

- **生成速度** =（输出 Token − 1）÷（总耗时 − 首字时间），只算流式请求，即 vLLM 的 1 ÷ TPOT
- **端到端速度** = 输出 Token ÷ 总耗时，包含等首字的时间，即 Artificial Analysis 的 Output Speed

数据都在 `data-dir` 下：`opencode.db`（请求日志，SQLite）和 `proxies.json`（代理列表，含代理密码，权限 0600）。

## 管理接口

都在 `/v0/management/plugins/opencode/` 下，CPA 先校验管理密钥再转给插件。

| 接口 | 用途 |
|---|---|
| `GET status` | 插件状态和 Key 列表 |
| `GET models`、`POST models/refresh` | 模型目录、强制重新拉取 |
| `POST credentials`、`PATCH credentials` | 添加、编辑 Key |
| `GET credentials/quota?auth_index=` | 工作区的 Go 用量 |
| `GET logs`、`DELETE logs` | 查询、清空请求日志 |
| `GET` / `POST` / `PATCH` / `DELETE proxies`，`POST proxies/import`、`proxies/order`、`proxies/check` | 代理列表 |

```bash
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" -H "Content-Type: application/json" \
  -d '{"api_key":"oc_sk_...","models":["kimi-k3"],"prefix":""}' \
  http://127.0.0.1:8317/v0/management/plugins/opencode/credentials
```

编辑只改传了的字段：`null` 恢复默认，`""` 清空。`api_key` 不能改，换 Key 就新增一个再删掉旧的。启停和删除用 CPA 原生的认证文件接口。

## 构建

需要 Go 和 Node 20：

```bash
make web    # 构建管理页（web/，React + Vite），嵌进插件
CGO_ENABLED=1 go build -buildmode=c-shared -o opencode.so .
make web-test && go test ./...
```

推送 `v*` tag 后，GitHub Actions 跑测试、构建五个平台并发布 Release。

## License

MIT。国旗图标来自 [Twemoji](https://github.com/jdecked/twemoji)（CC-BY 4.0）。
