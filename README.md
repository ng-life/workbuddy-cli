# workbuddy-cli

使用 Go 实现的 WorkBuddy 签到与成长中心 CLI，读取本机已有登录态，提供状态查询、每日签到和一轮成长中心奖励处理。协议及加密凭据助手参考 [88lin/workbuddy-auto-signin](https://github.com/88lin/workbuddy-auto-signin)，参考提交为 `d6bfeae254a2ba6c93b2211d58b814178c7fd48a`，上游 MIT 声明保存在 `THIRD_PARTY_LICENSES`。

本工具使用非官方接口，客户端或服务端更新可能影响兼容性。当前实现覆盖 WorkBuddy，不包含 Qoder CN。

## 下载与安装

从 [GitHub Releases](https://github.com/ng-life/workbuddy-cli/releases) 下载与系统和架构对应的安装包，使用同一 Release 的 `SHA256SUMS` 核对文件。macOS Apple Silicon 对应 `darwin_arm64`，Intel Mac 对应 `darwin_amd64`；Windows 安装包为 ZIP，其他平台为 tar.gz。

macOS / Linux 解压后，将 `workbuddy-cli` 放入 PATH 中的目录，例如 `~/.local/bin`，再执行 `workbuddy-cli version` 和 `workbuddy-cli doctor`。已有配置无需覆盖。发布包没有经过 Apple Developer ID 签名或公证；交叉编译产物也不等于已完成目标系统运行验证。

## 构建与使用

使用 Go 1.25 或更高版本。`mise.toml` 固定开发工具链为 Go 1.27.2；使用 mise 时，先执行 `mise install`，再通过 `mise exec -- make build` 构建。

```sh
make build
./bin/workbuddy-cli --help
./bin/workbuddy-cli doctor
./bin/workbuddy-cli status --output json
./bin/workbuddy-cli signin
./bin/workbuddy-cli growth status
./bin/workbuddy-cli growth run
./bin/workbuddy-cli auto --output json
```

运行前需在本机登录 WorkBuddy；Linux 可使用 CodeBuddy CLI 写出的同结构登录态。`doctor` 只检查凭据格式和客户端运行时能力，不解密、不联网，不能据此判断服务端登录态仍然有效。

| 命令 | 行为 |
| --- | --- |
| `doctor` | 离线检查登录态；加密凭据会探测客户端原生存储接口 |
| `status` | 查询签到状态，不领取 |
| `signin` | 查询活动与今日状态，仅明确未签到时领取 |
| `growth status` | 查询旅行、任务、连登、兑换、抽奖、Buddy 配额和能量 |
| `growth run` | 执行一轮成长中心，包括接任务、奖励领取、派出旅行、补登、兑换和开盲盒 |
| `auto` | 先执行签到，再执行一轮成长中心 |
| `completion bash/zsh/fish` | 生成补全脚本，不读取登录态、不联网 |
| `version` | 输出版本 |

无参数运行显示帮助。命令不接受位置参数；全局参数可以放在子命令前后，支持 `--flag=value`。执行 `auto` 或 `growth run` 会消耗可用的补登卡、抽奖机会或 Buddy 能量。

## 配置

按以下顺序选择一个配置文件，不合并多个文件：

1. `--config` / `-c` 指定的文件。
2. 当前目录的 `config.toml`。
3. `$XDG_CONFIG_HOME/workbuddy-cli/config.toml`；未设置 `XDG_CONFIG_HOME` 时为 `~/.config/workbuddy-cli/config.toml`，macOS 同样采用此路径。

自动发现仅在候选文件不存在时继续。显式文件缺失或任何已存在的配置解析失败都会报错。全部缺失时使用默认值和登录态自动发现。配置项覆盖顺序为「命令行 > 环境变量 > 选中的配置文件 > 默认值」。

```toml
# config.example.toml 中有相同的完整示例；通常无需指定这两项。
# auth_file = "/absolute/path/workbuddy-desktop.info"
# runtime = "/Applications/WorkBuddy.app/Contents/MacOS/WorkBuddy"
timeout = "30s"
budget = "7m"
```

| 参数 | 环境变量 / 配置项 | 默认值 |
| --- | --- | --- |
| `--auth-file` | `WORKBUDDY_AUTH_FILE` / `auth_file` | 自动发现 |
| `--runtime` | `WORKBUDDY_EXE` / `runtime` | 自动发现客户端可执行文件 |
| `--timeout` | `timeout` | 单请求 `30s` |
| `--budget` | `budget` | 整轮 `7m` |
| `--output` / `-o` | 无 | `text`；可选 `json` |

`timeout` 和 `budget` 均须大于 0，且不超过 `10m`；整轮时限优先约束尚未完成的请求。文件路径按原样使用，推荐绝对路径；程序不对 TOML 中的 `~` 或环境变量进行展开。私有配置建议权限为 `600`，用户配置目录建议权限为 `700`。仓库忽略根目录的 `config.toml`。

登录态按以下候选顺序查找：Windows 的 `%LOCALAPPDATA%/CodeBuddyExtension/Data/Public/auth/workbuddy-desktop.info`、macOS 的 `~/Library/Application Support/CodeBuddyExtension/Data/Public/auth/workbuddy-desktop.info`、Linux 的 `$XDG_DATA_HOME/CodeBuddyExtension/Data/Public/auth/Tencent-Cloud.coding-copilot.info`（默认根目录为 `~/.local/share`），然后兼容旧的 `~/.config/CodeBuddyExtension/Data/Public/auth/workbuddy-desktop.info` 和 `~/.workbuddy/auth/workbuddy-desktop.info`。显式路径读取失败不会改用其他文件。

支持明文令牌和 `sym-v1` / suite 1 加密封装。加密凭据通过内嵌 JS 助手调用本机 WorkBuddy 的 Electron 原生存储能力；无需 Python 或单独安装 Node.js。macOS 从应用 `Info.plist` 定位可执行文件，Windows 检查常见安装目录；其他位置通过 `--runtime` 指定，不扫描注册表。助手时限为 `10s`，受整轮时限约束，输入与标准输出各限制为 `64 KiB`。未知加密格式会报错，不会改写登录态。

## 输出与失败处理

数据写入 stdout；错误写入 stderr。`--output json` 输出一个 JSON 对象，不混入日志或颜色。默认 `text` 输出按字段排序的展开结果，字符串与字段名转义，适合查看和保存日志。

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功，包括已签到、活动未开启、无可执行奖励 |
| `1` | 配置、登录态、网络、API 或部分业务步骤失败 |
| `2` | 参数或命令错误 |

签到结果包括 `CLAIMED`、`ALREADY`、`INACTIVE`。成长中心和自动运行的 `steps` 记录已完成操作与失败步骤，`needs_attention` 表示存在失败。部分步骤成功后出现失败会保留成功记录并返回非零退出码，已完成操作不会回滚。未取得业务结果的早期错误只写 stderr，stdout 可以为空。

令牌不会出现在错误消息中，业务输出会对令牌内容和敏感字段脱敏；服务端完整错误文案不会直接展示。JSON 保留服务端数字精度；空值和空字符串不转换为展示用替代值。

## 执行约束

- 所有 API 请求均不自动重试。网络中断或响应异常可能发生在服务端已经处理写请求之后；先用状态查询确认，再决定是否重新运行。
- 签到只在 `active=true` 且 `today_checked_in=false/0` 时领取。明确的重复签到响应按成功处理；字段缺失或未知响应返回错误。
- 旅行礼物领取失败时不继续派出。空闲时采用服务端配置中的第一个地点。
- 任务按未接单、已完成、锁定状态处理；接单每批最多 20 条，已完成任务使用独立领奖接口。
- 补登以服务端日历日期为准，只考虑当月、今天之前、活动上线之后且 `score=0` 的明确记录。排除已补登日期，最近日期优先，每轮最多消耗一张卡。缺失或冲突数据不触发补登。
- 连登兑换使用 `7d`、`14d`、`28d` 标识，跳过未提供、已兑换和锁定档位。明确返回「天数不足」的 403 作为未解锁状态跳过；其他 401/403 停止后续请求。
- 每轮最多抽奖一次；Buddy 开盒数量不超过服务端返回的可负担数量和单次上限。实物奖的 `need_address` / `require_address` 等字段会保留在输出中；需在客户端自行处理收件信息。
- 认证、权限或网络错误停止本轮后续请求；其他失败记录在 `steps` 中。API 响应限制为 `2 MiB`，禁止跟随 HTTP 重定向。

程序没有跨进程锁；定时任务应避免重叠。当前没有内置守护进程或自动安装定时任务，见 [定时运行示例](docs/scheduling.md)。

## 补全

以下命令只影响当前 shell，不修改启动文件：

```sh
# zsh：在 compinit 之后加载
source <(./bin/workbuddy-cli completion zsh)

# Bash：需先加载与当前 shell 兼容的 bash-completion
source <(./bin/workbuddy-cli completion bash)

# fish
./bin/workbuddy-cli completion fish | source
```

补全覆盖命令、选项、`--output` 枚举及文件路径，不访问远端 API。长期配置时使用二进制的绝对路径，并在 shell 初始化完成后加载。

## 开发与验证

```sh
make fmt
make check
make build
```

- `cmd/workbuddy-cli`：信号处理和进程入口。
- `internal/cli`：Cobra 命令、输出、退出码。
- `internal/config`：配置选择、覆盖及执行时限。
- `internal/auth`：凭据发现、格式校验、运行时定位及有界助手。
- `internal/workbuddy`：HTTP、签到、成长中心、失败报告和脱敏。

测试使用本地 HTTPS 模拟服务和合成凭据，不访问真实账号。真实接口和客户端加密能力需在目标机器单独联调；交叉编译只证明可构建，不证明原生运行兼容性。
