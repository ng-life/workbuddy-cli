# 定时运行示例

以下示例只供手动配置，工具不会安装系统任务或修改 shell 配置。定时进程必须以持有登录态的本机用户身份运行。任务之间至少间隔整轮执行时限，避免重叠。

## cron

先确认二进制的绝对路径，并在仓库外运行 `doctor` 和 `status`。下面示例每天 00:05 执行一次完整流程：

```cron
5 0 * * * /absolute/path/workbuddy-cli auto --config /absolute/path/config.toml --output json >> /absolute/path/workbuddy-cli.log 2>&1
```

cron 的日期和时间由系统配置决定；电脑关机、休眠或网络不可用时不能保证执行。路径中包含空格时按 shell 规则引用。不要将令牌写入 cron；通过本机登录态文件读取。

## macOS launchd

将示例中的二进制、配置和日志路径替换为本机绝对路径，再按系统的 LaunchAgent 管理流程安装。`ProgramArguments` 每个元素对应一个参数，路径有空格也无需额外增加 shell 引号。

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>local.workbuddy-cli</string>
  <key>ProgramArguments</key>
  <array>
    <string>/absolute/path/workbuddy-cli</string>
    <string>auto</string>
    <string>--config</string><string>/absolute/path/config.toml</string>
    <string>--output</string><string>json</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict><key>Hour</key><integer>0</integer><key>Minute</key><integer>5</integer></dict>
  <key>StandardOutPath</key><string>/absolute/path/workbuddy-cli.log</string>
  <key>StandardErrorPath</key><string>/absolute/path/workbuddy-cli.error.log</string>
</dict>
</plist>
```

定时任务运行失败时，先检查退出码与错误日志，再使用相同用户、二进制和配置手动执行 `doctor`、`status`。写请求超时后先确认远端状态，避免直接重跑消耗类操作。定时日志需要自行轮换。
