# 使用文档

[返回首页](<../README.md>) · [设计文档](<DESIGN.md>)

## 1. 环境与安装

目标环境为 macOS Apple Silicon。准备 Go 1.24+、Apple C 编译工具、FFmpeg / ffprobe，以及可正常访问视频号的桌面微信。

```bash
xcode-select --install  # 如系统尚未安装命令行工具
brew install go ffmpeg

git clone https://github.com/ByteFluffy/wxchannels-kit.git
cd wxchannels-kit
./scripts/build-local.sh
./dist/wxc version
./dist/wxc --help
```

安装工具链需要自行准备网络。工具链齐备后，构建使用随附 Go 依赖及嵌入资源，不需要下载其他下载器程序，不需要先运行 npm。Go 工具链过旧时请先升级；构建不会偷偷下载新工具链。

以下命令均从仓库根目录运行。可以把构建出的 `wxc` 放到自己管理的命令目录，运行期间不要替换或移动服务正在使用的二进制。

## 2. 启动与连接微信

### 普通捕获模式

```bash
./dist/wxc start --accept-system-changes
```

该参数明确授权本进程准备本机 CA、按需安装/信任证书并配置系统代理。可能出现 macOS 权限确认。**不要把整个程序以 sudo 运行。**

保持终端运行，在桌面微信中重新打开一个视频号视频页面，让页面经过本机捕获服务并建立桥接。另开终端执行：

```bash
./dist/wxc doctor
```

`doctor` 输出 JSON：版本、API 地址、ffprobe/ffmpeg 是否存在、服务是否可达、微信页面是否连接。它不会修复系统设置，也不启用捕获；缺少工具、服务未运行或页面未连接时返回非零状态。

### 仅任务 API

```bash
./dist/wxc start --api-only
```

只启动本机任务 API 和引擎，不生成 CA、不信任证书、不启动捕获。适合本地受控数据处理与开发；单独启动它不能让尚未连接的微信页面完成分享解析。

## 3. 下载

```bash
./dist/wxc download 'https://weixin.qq.com/sph/SHARE_ID' \
  --output "$HOME/Downloads/wxc" \
  --filename '我的视频' \
  --timeout 15m
```

传入**单个完整 HTTPS 分享 URL**，不要连同聊天里的整段分享文案传入。支持官方短链、视频号预览页及带必要标识的 feed 链接，不接受任意媒体地址、凭据 URL 或非官方域名。

### 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--output` / `-o` | `~/Downloads` | 最终输出根目录，创建任务前传入；支持 `~` 展开 |
| `--filename` | 引擎默认 | 仅文件名覆盖，不是路径；后缀/规格标记按原生引擎规则生成 |
| `--spec` | 原生默认 | 页面实际提供的规格标识，不承诺固定名称或“原画” |
| `--existing` | `error` | `error`、`skip`、`overwrite`、`duplicate` |
| `--timeout` | `15m` | 解析、任务等待和文件验证的整体期限，1 秒至 6 小时 |
| `--decode` | `true` | 全量解码；用 `--decode=false` 只探测媒体并计算哈希 |
| `--api-base-url` | 当前本机配置 | 仅允许回环 HTTP/S URL，无额外路径、凭据或查询参数 |

目录和文件名在任务创建前进入引擎，不在下载后脱离数据库搬运文件。文件名不能含目录分隔符、系统保留名称、控制字符等。

### 重复任务

```bash
# 已有任务时拒绝再次提交
./dist/wxc download 'https://weixin.qq.com/sph/SHARE_ID' --existing error

# 等待并校验现有任务的实际输出
./dist/wxc download 'https://weixin.qq.com/sph/SHARE_ID' \
  --output "$HOME/Downloads/wxc" --existing skip
```

`skip` 不是跳过文件验证。现有任务的文件若不在本次指定的输出根目录内，会被拒绝。`overwrite` 可能覆盖原文件，`duplicate` 明确允许创建重复任务，使用前应确认意图。

### 成功与失败

成功结果是 JSON，包含 `task_id`、`status: verified`、`skipped` 以及 `files` 数组。每个文件报告实际路径、大小、SHA-256、宽高、时长、编码与验证级别。

- `probe`：实际文件存在、包含有效视频、媒体参数与哈希检查通过。
- `decode`：在上述基础上，完成 ffmpeg 解码检查。

**创建成功不等于下载成功。超时或 Ctrl+C 也不等于后台任务取消。** 已知任务 ID 时会提示后续查询方式；提交结果不明确时，先查现有任务，不要盲目重复提交。工具不会自动重试任务创建。

## 4. 查询与独立验证

```bash
./dist/wxc task 1
./dist/wxc verify "$HOME/Downloads/wxc/实际文件名.mp4"
./dist/wxc verify "$HOME/Downloads/wxc/实际文件名.mp4" --decode --timeout 15m
```

`task` 只投影安全任务信息，不打印原始签名地址、解密密钥或后端原始错误。`verify` 不连接微信或下载服务，默认探测模式；完整解码需显式 `--decode`。

CLI 出错时返回非零状态，诊断写入 stderr。`doctor` 即使失败也可能先输出诊断 JSON，自动化脚本应同时检查退出码。

## 5. MCP

### 启动方式

先自行启动本机服务并建立微信页面桥接，再由 MCP 客户端启动：

```bash
./dist/wxc mcp --api-base-url http://127.0.0.1:2022
```

通用 stdio 客户端配置示例，实际字段以客户端要求为准：

```json
{
  "mcpServers": {
    "wxchannels": {
      "command": "/absolute/path/to/wxchannels-kit/dist/wxc",
      "args": ["mcp", "--api-base-url", "http://127.0.0.1:2022"]
    }
  }
}
```

命令使用绝对路径。图形客户端未必继承终端 PATH，应确保它也能找到 ffprobe/ffmpeg；如使用自定义 `WXC_HOME`，为服务和客户端配置同一值。

### 四个工具

| 工具 | 用途 |
| --- | --- |
| `download_video` | 解析分享、创建/等待原生任务并验证实际文件 |
| `get_video_task` | 按正整数任务 ID 查询安全状态 |
| `verify_video` | 验证本地文件，可选完整解码 |
| `get_wxchannels_status` | 查询当前微信页面桥接是否可用 |

`download_video` 参数示例：

```json
{
  "url": "https://weixin.qq.com/sph/SHARE_ID",
  "download_dir": "~/Downloads/wxc",
  "filename": "我的视频",
  "existing_action": "error",
  "timeout_seconds": 900,
  "decode": true
}
```

可选 `spec` 指定页面提供的规格。MCP 下载的 `decode` 默认 `false`，与 CLI 下载默认值不同；需要完整解码时传 `true`。MCP 只操作已启动的服务，不安装证书、不改配置、不部署云服务。

### 协议注意事项

标准输出仅用于 JSON-RPC，不能混入日志或横幅。自行实现客户端时，应初始化协议、按请求 ID 等待响应，**请求完成前不要关闭 stdin**；关闭输入会结束会话并取消仍在等待的请求，不保证取消已创建的原生任务。

## 6. 运行目录与停止

默认使用 macOS 用户配置目录下的 `wxchannels-kit`，通常为 `~/Library/Application Support/wxchannels-kit`。可覆盖为一个独立私有目录：

```bash
export WXC_HOME="$HOME/.local/share/wxchannels-kit"
./dist/wxc start --accept-system-changes
# 另一个终端也应使用同一 WXC_HOME
./dist/wxc task 1
./dist/wxc stop
```

新建运行目录使用 `0700`；新日志、会话记录和本机 CA 文件使用 `0600`。这些数据及下载文件不应提交 Git。日志可能含私密运行信息，分享问题时请先人工脱敏。

`stop` 会核对 PID、可执行文件和启动时间，只正常终止匹配的会话，不按名称批量杀进程、不强制终止其他程序。也可在服务终端 Ctrl+C。更换了二进制路径或运行目录时，身份核对可能拒绝，优先回到原终端停止。

## 7. 代理与证书

- 默认 API 地址为 `http://127.0.0.1:2022`，捕获端口为 `2023`，实际以本机配置为准。
- 自动模式拒绝覆盖已经启用的系统代理；工具不会代替你更改 Clash 等代理软件的配置。
- 高级场景可使用手动转发模式：

```bash
./dist/wxc start --accept-system-changes --manual-proxy
```

手动模式**仍然涉及本机 CA 和信任**，只是不自动改系统代理；具体转发关系由使用者配置。TUN / TCP relay 不在当前入口范围。

正常结束只关闭仍匹配本程序地址的系统代理，不修改后来切换到其他地址的代理。没有完整网络设置快照，不保证还原原本关闭的代理所保存的全部字段。

如被强制终止或断电后网络异常，先在 macOS 网络设置检查 HTTP/HTTPS 代理是否仍指向本工具的回环地址与端口，核实后手动关闭；不要盲目重置所有代理或删除其他根证书。

CA 信任可能在停止后保留。需要移除时，在钥匙串访问中按本工具生成的唯一名称/指纹核对后操作。程序不提供批量删除证书命令。

## 8. 常见问题

| 现象 | 检查方向 |
| --- | --- |
| `doctor` 找不到 ffprobe / ffmpeg | 确认 FFmpeg 已安装，当前终端或 MCP 客户端 PATH 正确 |
| 本机服务不可达 | 检查服务终端、API 地址/端口，以及启动和控制使用的 `WXC_HOME` 是否相同 |
| 服务可达但页面未连接 | 在正常桌面微信中打开视频号页面；检查流量是否经过本机捕获服务及 CA 信任 |
| 提示已有系统代理 | 不会自动覆盖；自行处理既有代理或明确使用手动转发模式 |
| 提示任务已存在 | 先用 `task` 检查，按意图选择 `skip` / `overwrite` / `duplicate` |
| 等待超时 | 按返回的任务 ID 查询；不要认定任务已取消后立即重复提交 |
| 原生任务完成但验证失败 | 检查实际输出、磁盘权限与媒体完整性；用独立 `verify --decode` 排查 |
| 会话记录存在或停止身份不符 | 检查原服务终端和运行目录，不要按旧 PID 盲目终止进程 |

接口和页面脚本随微信版本变化可能需要适配。不要通过暴露回环 API、关闭证书校验或删除其他程序配置来绕过故障。

## 9. 开发命令

```bash
./scripts/build-local.sh
./scripts/test-local.sh
```

测试以隔离的 API-only 服务、回环页面 RPC 和生成媒体运行，不需要真实账号数据。模块与数据流见[设计文档](<DESIGN.md>)。用途、禁售与责任边界以[首页声明](<../README.md#用途与责任声明>)和[许可证](<../LICENSE>)为准。
