# CPA OpenCode Plugin

一个 CLIProxyAPI（CPA）插件：**把 [OpenCode](https://opencode.ai) Console 的 API Key（[OpenCode Go](https://opencode.ai/docs/go) 订阅或按量付费的 Zen）作为 CPA 认证文件接入，转发 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 三种协议。**

- 每个 OpenCode API Key（`oc_sk_…`）是一个认证文件（`{"type":"opencode","api_key":"..."}`），和其他凭证一样参与 CPA 的选号、冷却和故障转移；
- 自动拉取 OpenCode 模型目录（`GET /v1/models`）并注册到 CPA，带显示名、上下文长度、最大输出、输入类型（图片 / 视频 / 音频 / PDF）、工具调用和推理强度；前缀走 CPA 通用的 `prefix` 机制，默认 `opencode/<模型 ID>`；每个 Key 可以只注册选中的模型；
- 请求按客户端格式原样转发到 OpenCode 对应的接口，不做协议转换（OpenCode 自己会在三种协议之间转换，所以每个模型三种协议都能调）；
- 自动带上 OpenCode Go 必需的会话头 `x-opencode-session`，客户端带了会话 ID 就沿用，没带就按对话开头推算，同一个对话的每一轮保持不变；
- 支持 Anthropic 的 `count_tokens`；
- 通过 Go 用量接口查询每个 Key 所属工作区的 5 小时 / 每周 / 每月额度用了多少、是否被限流、什么时候重置，CPA 原生额度面板和插件管理页都能看到；
- 每个 Key 可以单独配置出口代理（CPA 通用认证文件字段 `proxy_url`）；
- 自带管理页：添加、编辑、启停、删除 Key，选择上游模型，查看 Go 用量、模型价格，管理代理列表，查看请求日志。

> 这是社区插件，与 OpenCode 官方无关。OpenCode 名称和图标归 OpenCode 所有，这里只用来标识所接入的服务。

## 环境要求

- CLIProxyAPI v8（按 v8.0.58 的插件 SDK 构建），且开启了动态插件支持
- Linux amd64/arm64、macOS amd64/arm64 或 Windows amd64
- Unix 平台上需要启用 cgo 编译的 CPA
- 一个 OpenCode 账号，以及在 [OpenCode Console](https://opencode.ai/auth) 的工作区里创建的 API Key（`oc_sk_` 开头）；用 Go 模型需要工作区订阅了 OpenCode Go

## 安装

### 通过插件商店

在 CPA 配置里加上 berry-shake 的商店源：

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/berry-shake/CLIProxyAPI-Plugins-Store/main/registry.json
```

然后在管理面板的插件商店里找到 **OpenCode**（ID `opencode`）安装，也可以直接调用管理接口：

```bash
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"version":"0.1.0"}' \
  http://127.0.0.1:8317/v0/management/plugin-store/opencode/install
```

### 手动安装

1. 从 [Release](https://github.com/berry-shake/cpa-opencode-plugin/releases) 下载对应平台的 zip，解压出 `opencode.<so|dylib|dll>`。
2. 放进对应平台的插件目录，例如：

   ```text
   plugins/linux/amd64/opencode.so
   plugins/linux/arm64/opencode.so
   plugins/darwin/amd64/opencode.dylib
   plugins/darwin/arm64/opencode.dylib
   plugins/windows/amd64/opencode.dll
   ```

3. 在 `config.yaml` 里启用：

   ```yaml
   plugins:
     enabled: true
     configs:
       opencode:
         enabled: true
   ```

> 如果 `config.yaml` 里已经有 `store.version`，CPA 只会加载文件名版本和它一致的插件文件。手动换文件时记得同时改 `store.version` 和 `release-tag`。

## 插件配置

所有配置项都是可选的，写在 `plugins.configs.opencode` 下面：

```yaml
plugins:
  configs:
    opencode:
      enabled: true
      base-url: https://opencode.ai/inference/go   # 推理接口地址，末尾的 /v1、/openai、/anthropic 会自动去掉
      model-prefix: opencode                # 认证文件没写 prefix 时使用的默认前缀；写 "" 表示不加前缀
      models-refresh-minutes: 60          # 模型目录轮询间隔（分钟），0 = 不轮询，只按需加载
      request-timeout-seconds: 600        # 非流式请求超时
      stream-idle-timeout-seconds: 300    # 流式响应多久没有新数据就中断
      data-dir: plugins/opencode-data       # 插件数据目录（请求日志数据库 opencode.db、代理列表 proxies.json），相对路径按 CPA 的工作目录算
      log-retention: 100000               # 请求日志最多保留多少条（上限 1000000），0 = 不记录
      log-retention-days: 30              # 请求日志保留多少天，0 = 不按天数清理，只受条数限制
```

非法的值会被忽略并回退到默认值，同时写一条警告日志，不会让插件注册失败。

`base-url` 决定走哪种计费：

| base-url | 模型 | 计费 |
|---|---|---|
| `https://opencode.ai/inference/go`（默认） | OpenCode Go 的模型 | 用 Go 订阅额度（5 小时 / 每周 / 每月限额） |
| `https://opencode.ai/inference` | Zen 的全部模型 | 按量付费，从工作区余额扣，免费模型不扣 |

两种地址下的接口路径一样，模型目录也按地址分别拉取。只想让某个 Key 走另一种计费时，在它的认证文件里写 `base_url` 即可。

## 认证文件

放在 CPA 认证目录里的 JSON 文件，`type` 为 `opencode` 的文件由本插件接管。通过管理页添加的 Key 会保存为 `opencode-<Key 指纹>.json`，文件名不包含 Key 本身。

```json
{
  "type": "opencode",
  "api_key": "<OpenCode API Key>",
  "label": "主账号",
  "models": ["kimi-k3", "glm-5.3"],
  "prefix": "opencode",
  "proxy_url": "socks5://127.0.0.1:1080",
  "priority": 0,
  "note": "",
  "disabled": false
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `type` | 是 | 固定为 `opencode` |
| `api_key` | 是 | OpenCode API Key（也接受 `api-key`、`key`） |
| `label` | 否 | 显示名称；为空时用认证文件名（不带 `.json`） |
| `models` | 否 | 要注册的上游模型 ID 列表。**不写或为 `null` 表示注册目录里的全部对话模型**；`[]` 表示一个都不注册 |
| `prefix` | 否 | CPA 通用的模型前缀字段。不写时使用插件配置的 `model-prefix`；`""` 表示这个 Key 不加前缀 |
| `proxy_url` | 否 | 出口代理，见下文 |
| `base_url` | 否 | 覆盖插件配置里的 `base-url`，比如让这个 Key 走按量付费的 `https://opencode.ai/inference` |
| `priority` | 否 | CPA 通用的凭证优先级 |
| `note` | 否 | 备注 |
| `disabled` | 否 | CPA 通用的停用开关 |
| `model_revision` | — | 插件自己维护的目录版本标记，不要手动修改 |

### 模型前缀

前缀使用 CPA 通用的 `prefix` 字段，由 CPA 自己加到注册的模型名上，转发前再去掉。前缀为 `opencode` 时，客户端请求 `opencode/kimi-k3`，OpenCode 收到的是 `kimi-k3`。

- 认证文件没写 `prefix` 时，插件把配置里的 `model-prefix`（默认 `opencode`）作为这个 Key 的前缀交给 CPA，不会写进文件；改了 `model-prefix` 后插件会重新保存所有 OpenCode 认证文件，让 CPA 按新前缀重新注册。
- 写了 `prefix` 就用文件里的值，`""` 表示不加前缀。在管理页或 CPA 面板里修改认证文件的 `prefix`，效果相同。

> CPA 的 `routing.force-model-prefix` 默认是 `false`。这时 CPA 会同时注册带前缀和不带前缀的两个名字（`opencode/kimi-k3` 和 `kimi-k3`），OpenCode 的 Key 也会进入同名模型的公共号池，和其他渠道一起轮询。如果只想通过 `opencode/...` 访问 OpenCode，在 CPA 配置里打开：
>
> ```yaml
> routing:
>   force-model-prefix: true
> ```
>
> 这是全局开关，打开后其他配置了 `prefix` 的凭证也只会注册带前缀的名字。管理页的"注册到 CPA 的模型名"预览会按这个开关显示。

选中的模型如果暂时不在 OpenCode 目录里（比如上游下线了），仍然会注册，管理页会把它标成"已不在模型目录中"。

### 出口代理

`proxy_url` 是 CPA 认证文件的通用字段，本插件的所有上游请求（对话、模型目录、Go 用量）都按以下顺序选择出口：

1. 认证文件里的 `proxy_url`；
2. CPA 全局配置的 `proxy-url`；
3. 环境变量 `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`。

支持 `http://`、`https://`、`socks5://`、`socks5h://`，可以带用户名密码。写 `direct` 或 `none` 表示这个 Key 强制直连，不走全局代理。管理页里显示的代理地址会隐藏密码。

常用的代理可以在管理页的 **代理** 页里存成列表，编辑 Key 时直接从列表里选。选列表里的代理，实际上就是把它的地址写进 `proxy_url`，CPA 那边看到的还是普通的认证文件字段。

## 模型同步

- 插件启动后按 `models-refresh-minutes` 轮询 `<base-url>/v1/models`（无需鉴权，不发送 API Key；接口不带 ETag，每次都完整拉取）。一次请求就带全部元数据：显示名、模型家族、上下文长度、最大输出、输入类型、工具调用、推理强度（如 `low` / `medium` / `high` / `max`）、价格。
- "全部模型" 只注册对话模型（`type` 为 `chat` 的模型）；其他类型的模型在模型目录页会标出来，需要的话可以在"只用选中的模型"里手动勾选。
- 注册信息包括显示名、上下文长度、最大输出、输入模态、支持的参数（`tools`、`reasoning`）和推理强度列表。模型增减、上下文长度或能力变化时，插件会重新保存受影响的认证文件，CPA 随即重新注册这些 Key 的模型；只改价格不会触发重新注册。
- 目录拉取失败时保留上一次的目录，不会把已注册的模型清空。

## 请求转发

| 客户端格式 | OpenCode 接口（相对 `base-url`） |
|---|---|
| OpenAI Chat Completions | `POST /openai/v1/chat/completions` |
| OpenAI Responses | `POST /openai/v1/responses` |
| Anthropic Messages | `POST /anthropic/v1/messages` |
| Anthropic Count Tokens | `POST /anthropic/v1/messages/count_tokens` |

- 所有接口都用 `Authorization: Bearer <api_key>` 鉴权，客户端自己的鉴权头不会转发。
- 请求体只改写顶层的 `model` 字段，其余字节原样转发；
- Chat Completions 流式请求会自动加上 `stream_options.include_usage`，让 CPA 能统计用量；
- `anthropic-version` / `anthropic-beta` 只转发给 Messages 接口，客户端没带 `anthropic-version` 时补 `2023-06-01`；
- `count_tokens` 直接转发，不记进请求日志。

### 会话头

OpenCode Go 要求每个请求带 `x-opencode-session`，没带会返回 400 `MissingSessionID`；OpenCode 也用它把同一个对话路由到同一个上游，提高缓存命中。插件按以下顺序确定会话 ID：

1. 客户端带的会话头：`x-opencode-session`、`x-claude-code-session-id`、`session_id`、`session-id`、`x-session-id`、`conversation_id`、`conversation-id`；
2. 请求体里的 `prompt_cache_key`（Codex 等客户端会带）；
3. Claude Code 在 `metadata.user_id` 里带的会话 ID；
4. 都没有时，用系统提示词加第一条用户消息之前（含）的内容算一个哈希（`cpa_<32 位十六进制>`），同一个对话的每一轮都相同；
5. 请求体解析不了时退回 `cpa_<auth_id>`。

流式响应：

- 开头几帧（`message_start`、`response.created`、只带角色的 chunk、ping）先攒着，等到第一帧真正的输出再一起发给 CPA。这期间如果 OpenCode 在流里报错（限流、过载等），这次调用按对应状态码失败，CPA 会和 HTTP 报错一样冷却 Key 或换 Key 重试；攒的内容超过 64KB 就不再等。
- 已经开始输出以后的错误帧原样转给客户端。
- 流没收到结束帧就断了（Chat 的 `[DONE]` 或 `finish_reason`、Responses 的 `response.completed`、Messages 的 `message_stop`），按失败处理，错误码 `stream_truncated`（502）。CPA 对这个错误码不冷却 Key。

上游错误到 CPA 的映射（流里报的错按错误类型归到同样的状态码）：

| OpenCode 返回 | 交给 CPA | 效果 |
|---|---|---|
| 401（Key 无效、被吊销或过期） | 401 | 这个 Key 进入冷却 |
| 402（工作区余额不足） | 402 | 这个 Key 进入冷却 |
| 403（Key 没有权限、模型被工作区隐私设置禁用等） | 403 | 这个 Key 进入冷却 |
| 404（模型不存在） | 400 | 只失败这一次请求，不冷却 Key |
| 429（Go 额度用完、工作区预算到顶或限流） | 429，保留 `Retry-After` | 按上游给的时间冷却 |
| 529 | 503 | 可重试 |
| 499 | 408 | 可重试 |

## Go 用量

查询一个 Key 时，插件调用 `GET https://opencode.ai/zen/go/v1/usage`（`base-url` 所在域名下的固定路径），返回三个窗口：

| 窗口 | 说明 |
|---|---|
| 5 小时 | 滚动窗口，额度是月额度的 20% |
| 每周 | 每周一 00:00 UTC 重置，额度是月额度的 50% |
| 每月 | 按订阅日重置 |

每个窗口给出已用百分比、是否已被限流（`rate-limited`）和重置时间。需要注意：

- **用量是工作区级的**：同一工作区的所有 Key 查到的数字相同。接口不返回工作区 ID，管理页把月度重置时间和三个百分比都相同的 Key 视为同一个工作区，汇总时只算一次。
- 工作区没有订阅 Go 时接口返回 403，管理页显示"该工作区未订阅 Go"；这样的 Key 仍然可以走按量付费地址调用 Zen 模型。添加 Key 时的校验也调用这个接口，403 视为 Key 有效。
- OpenCode 没有查询余额的 API（按量付费的余额只在 Console 网页里看得到），所以插件不显示余额。按量付费请求的花费 OpenCode 会写在响应的 `usage.cost` 里。
- 每个 Key 的查询结果缓存 60 秒。

CPA 原生额度面板显示 **OpenCode Go** 一组，三个窗口各一条剩余比例进度条，带重置时间；被限流的窗口剩余显示为 0。

## 管理页

在 CPA 管理面板的插件菜单里点 **OpenCode** 打开，页面地址是：

```text
/v0/resource/plugins/opencode/manage
```

- 从管理面板里打开时复用面板"记住密码"保存的管理密钥；直接打开时手动输入，只保存在页面内存里。
- **凭据管理** 页：
  - 顶部一条统计条：Go 工作区数（以及限流中、未订阅 Go 的数量），5 小时 / 本周 / 本月平均剩余比例。按工作区去重，同一工作区的多个 Key 只算一次。
  - Key 以卡片网格展示（宽屏一行三张，窄屏单列）。每张卡片从上到下依次是：名称（默认是认证文件名，不带 `.json`）、状态、打码后的 Key 和优先级；左边是模型、前缀、出口代理和成功 / 失败次数（2×2），右边是 5 小时 / 本周 / 本月剩余比例和限流状态（同样 2×2，卡片较窄时移到下方）；这四列都按内容宽度排，第一列贴左、最后一列贴右，多出的宽度平均分到列与列之间，已用比例和重置时间在鼠标悬停提示里；下面是本月额度进度条，左边是重置时间，右边是剩余比例（颜色跟进度条一致）；被限流时另起一行显示恢复时间；再下面是备注和状态；底部是查询时间和操作按钮。
  - 卡片上可以编辑、启用/停用、删除（删除需要 4 秒内再点一次确认）；可以按名称、Key 或文件名搜索。
  - 打开页面时自动查询所有 Key 的 Go 用量（停用的也查），"刷新用量"重新查询全部。查询按出口排队：同一个出口（同一个代理，或同样直连）一次只查一个 Key；不同出口之间同时查。CPA 额度面板发来的查询、保存 Key 时的校验也一起排队。
- 添加和编辑在弹窗里进行：名称、出口代理（继承 CPA 全局代理 / 直连 / 代理列表里的代理 / 自定义地址）、模型前缀、优先级，以及"全部对话模型 / 只用选中的模型"，选模型时可以按 ID、名称、模型家族或输入类型搜索，并对比上下文长度和最大输出。添加 Key 时默认先调用 Go 用量接口校验 Key 是否有效（OpenCode 暂时连不上时可以勾选"跳过 Key 校验"）；名称留空时用认证文件名。
- **代理** 页：管理出口代理列表。
  - 可以添加、编辑、删除代理。名称留空时用 `主机:端口`；地址只显示隐藏了密码的样子，编辑时地址留空表示不改。
  - 改了某个代理的地址时，正在用它的 Key（`proxy_url` 和旧地址完全相同）会一起改成新地址。删除代理不影响 Key：用它的 Key 保留原地址，变成"自定义地址"。
  - 有 Key 用了列表里没有的代理时，可以点"导入 Key 里的代理"一次加进来。
  - 列表上面固定两行：继承 CPA 全局代理、直连，对应 Key 编辑弹窗里 CPA 内置的两个选项，位置不能改。
  - 列表里的代理可以按住名称左边的手柄拖动排序（鼠标和触屏都行），也可以聚焦手柄后按 ↑ ↓ 移动；顺序存进 `proxies.json`，Key 编辑弹窗的下拉框也按这个顺序列出。
  - 检测要手动点"检测"（单个）或"检测全部"，不会自动检测。检测时插件通过这个代理请求 `https://ipinfo.io/json`，失败再试 `https://api.ip.sb/geoip`，User-Agent 用 `curl/8.7.1`；每次检测新建连接，延迟包含连上代理的时间，单个服务超时 10 秒。
  - 出口列显示国旗和出口 IP，下面是国家和城市（国家名按页面语言显示）；另外两列是延迟和检测时间（本地时间，带日期）。还没检测过的显示"—"。
  - 每次检测的结果（包括失败）都会保存，下次打开页面直接显示上一次的结果。代理改了地址，旧结果作废；CPA 全局代理变了，"继承"那行的旧结果也不再显示。
  - 代理列表和检测结果存在 `<data-dir>/proxies.json`（默认 `plugins/opencode-data/proxies.json`，权限 0600，里面有代理的用户名密码）。
  - 国旗图标来自 [Twemoji](https://github.com/jdecked/twemoji)（CC-BY 4.0），编进插件里，由 `/v0/resource/plugins/opencode/flag.svg?cc=<国家代码>` 提供，不访问外部网站。
- **模型目录** 页：当前 `base-url` 下的模型，显示名称和模型家族、输入类型和能力（图片 / 视频 / 音频 / PDF、推理、工具）及推理强度、上下文长度和最大输出、每百万 Token 价格（输入 / 输出、缓存读写、长上下文分档、高峰时段倍率，免费模型标"免费"），以及每个模型在 CPA 里注册的名字和服务它的 Key 数。
- **请求日志** 页：经过本插件的每次调用，最新的在前。
  - 最上面是时间范围：全部、今天、24 小时、近 7 天、近 30 天、自定义（精确到分钟的起止时间，点"应用"生效）。按请求开始时间筛选，选择会记在浏览器里。
  - 统计条：请求数（成功 / 失败）、输入 / 输出 / 缓存 Token、缓存命中率、平均输出速度，按当前时间范围和筛选条件统计。
  - 每行显示时间、模型（和发给 OpenCode 的上游模型）、思考强度、结果状态码、凭据（认证文件名，不带 .json）、OpenCode 实际返回的模型、首字时间 / 总耗时、Token 用量；点开一行能看到 CPA 的请求 ID、接口、是否流式、CPA 的 auth_index、打码的 Key、缓存写入、思考 Token、输出速度，失败的还有错误码、出错阶段（输出前 / 流式中途）和错误信息。
  - 可以按请求 ID、模型、凭据、认证文件、错误信息或状态码搜索，按成功 / 失败筛选；每页 20 / 50 / 100 / 200 条，可以输入页码跳转；清空需要 3 秒内再点一次确认。
  - 日志只记录元数据和用量，不记录请求和响应内容。
- **配置概览** 页：插件配置的当前生效值（可以展开成 YAML）、CPA 的强制前缀开关和全局代理。强制前缀没开时会提示同名模型会进入公共号池。
- 支持 English / 中文。

请求日志存在 SQLite 数据库 `<data-dir>/opencode.db`（默认 `plugins/opencode-data/opencode.db`，WAL 模式，旁边会有 `-wal` 和 `-shm` 文件），查询、筛选和统计都在数据库里做。超过 `log-retention` 条或早于 `log-retention-days` 天的记录会自动删掉。SQLite 用的是 [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)，随插件一起编译进去，不依赖系统的 libsqlite3。

详情里显示完整的请求 ID。CPA 日志和请求日志文件名里用的是它的末 8 位，把这 8 位粘贴进搜索框也能找到对应的记录。CPA 只把请求 ID 交给请求拦截器，所以插件注册了一个拦截器：CPA 选中 OpenCode 凭据后，拦截器把请求 ID 放进内部请求头 `X-OpenCode-Cpa-Request-Id`，执行器读出来记进日志。这个头不会转发给 OpenCode；其他渠道的请求，拦截器不做任何修改。拦截器只读请求头和元数据，不接收请求体，需要 CPA 支持插件协议 schema 7；CPA 版本更老时不注册拦截器，日志里也就没有请求 ID。

启用、停用和删除走 CPA 原生的认证文件接口（`PATCH /v0/management/auth-files/status`、`DELETE /v0/management/auth-files?name=...`），和在 CPA 面板里操作效果相同。

## 管理接口

```text
GET   /v0/resource/plugins/opencode/manage             管理页外壳，无需鉴权，不含数据
GET   /v0/resource/plugins/opencode/logo.svg           插件 logo，无需鉴权
GET   /v0/resource/plugins/opencode/flag.svg?cc=hk     国旗图标，无需鉴权
GET   /v0/management/plugins/opencode/status           插件状态、目录摘要、Key 列表
GET   /v0/management/plugins/opencode/models           模型目录（能力、上下文长度、最大输出、推理强度、价格）
POST  /v0/management/plugins/opencode/models/refresh   强制重新拉取模型目录
POST  /v0/management/plugins/opencode/credentials      添加 Key
PATCH /v0/management/plugins/opencode/credentials      编辑 Key
GET   /v0/management/plugins/opencode/credentials/quota?auth_index=...   Key 所属工作区的 Go 用量（5 小时 / 每周 / 每月）
GET   /v0/management/plugins/opencode/logs?search=&status=all|success|error&from=&to=&limit=50&offset=0   请求日志（最新的在前）和统计；from / to 是 RFC 3339 时间，按请求开始时间筛选，含 from 不含 to
DELETE /v0/management/plugins/opencode/logs            清空请求日志
GET   /v0/management/plugins/opencode/proxies          代理列表（地址隐藏密码、上一次检测结果）和 CPA 全局代理
POST  /v0/management/plugins/opencode/proxies          添加代理 {"name","url"}
PATCH /v0/management/plugins/opencode/proxies          修改代理 {"id","name","url"}，改地址时同步改用它的 Key
DELETE /v0/management/plugins/opencode/proxies?id=...  删除代理，用它的 Key 保留原地址
POST  /v0/management/plugins/opencode/proxies/import   把 Key 里用到、列表里还没有的代理加进列表
POST  /v0/management/plugins/opencode/proxies/order    调整代理顺序 {"ids":[...]}
POST  /v0/management/plugins/opencode/proxies/check    检测出口 IP、国家、城市和延迟并保存结果，{"id"} 或 {"target":"direct"|"inherit"}
```

`/v0/management` 路由由 CPA 宿主先校验管理密钥，通过后才转发给插件。

```bash
# 添加 Key，只注册两个模型，不加前缀
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"api_key":"oc_sk_...","models":["kimi-k3","glm-5.3"],"prefix":""}' \
  http://127.0.0.1:8317/v0/management/plugins/opencode/credentials

# 改成注册全部对话模型、使用默认前缀、走指定代理
curl -X PATCH -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"auth_index":"<AUTH_INDEX>","models":null,"prefix":null,"proxy_url":"socks5://127.0.0.1:1080"}' \
  http://127.0.0.1:8317/v0/management/plugins/opencode/credentials
```

添加 Key 时加 `"skip_verify": true` 可以跳过 Key 校验。添加和编辑 Key 时可以用 `"proxy_id"` 代替 `"proxy_url"`，表示使用代理列表里的那个代理（写入它的地址）。Key 的 `proxy_url` 和列表里某个代理完全相同时，`status` 返回的这个 Key 带 `proxy_id` 和 `proxy_name`。

编辑接口只修改请求里出现的字段：`null` 表示恢复默认（`models` 恢复为全部对话模型，`prefix` 恢复为插件默认前缀，`priority` 清除），空字符串表示清除（`label`、`note`、`proxy_url`、`base_url`）。`api_key` 不能修改，换 Key 请新增一个再删掉旧的。

## 安全说明

- 读取和修改 Key 的接口都需要 CPA 管理密钥；公开的只有不含数据的页面外壳、logo 和国旗图标。
- 管理页设置了 `frame-ancestors 'self'`，其他站点不能嵌入。
- 接口和页面只显示打码后的 Key 和隐藏了密码的代理地址。
- 模型目录接口（`/v1/models`）不需要鉴权，插件拉取目录时不发送任何 API Key。

## 本地构建

管理页在 `web/`，用 React + TypeScript + Vite 写，构建成单个 HTML 文件后嵌进插件。需要 Node 20 和 Go：

```bash
make web        # 构建管理页，输出到 internal/web/dist/index.html
CGO_ENABLED=1 go build -buildmode=c-shared \
  -ldflags '-X main.pluginVersion=0.1.0-dev' \
  -o opencode.so .
```

macOS 用 `.dylib`，Windows 用 `.dll`。c-shared 构建会顺带生成一个 C 头文件，宿主用不到它。没执行 `make web` 就构建的话，管理页只是一个提示"尚未构建"的占位页。

本地调试管理页可以在 `web/` 里运行 `npm run dev`，开发服务器会把管理接口代理到 `VITE_CPA_BASE`（默认 `http://127.0.0.1:8317`）。

推送 `v*` tag 后，GitHub Actions 会先构建管理页，再跑 gofmt / vet，然后构建五个平台的产物，按插件商店规范打包成 `opencode_<version>_<goos>_<goarch>.zip` 并连同 `checksums.txt` 发布 Release。仓库目前不带单元测试。

## License

MIT
