# LumaChat

[简体中文](README.md) | [English](README.en.md)

**LumaChat** 是一个使用 Go 编写的轻量级、自托管 AI 聊天网页应用。它提供本地账户登录、多会话聊天、兼容 OpenAI API 的模型服务商配置，以及 SSE 流式回复。

> 项目状态：这是一个可构建的基础项目，适合个人测试和自托管实验。公开部署前请先阅读下方的安全说明；目前不应将它直接暴露到公网作为多用户生产服务。

## 功能

- 本地注册和登录
- 每个账户拥有独立的聊天会话
- 支持 OpenAI 兼容接口的服务商与模型
- SSE 流式输出
- 通过明文 INI 文件配置服务商，修改配置后可重新加载
- Go 标准库后端，不需要额外 Go 第三方依赖
- GitHub Actions 自动执行格式检查、测试、静态检查和构建

## 环境要求

- Go 1.20 或更新版本
- 一个兼容 OpenAI Chat Completions API 的服务端地址
- 对应服务商的 API Key

## 快速开始

### 1. 获取源码

```sh
git clone https://github.com/aaayQwQ/lumachat.git
cd lumachat
```

### 2. 创建配置文件

Linux/macOS：

```sh
cp config.example.ini config.ini
```

Windows PowerShell：

```powershell
Copy-Item config.example.ini config.ini
```

编辑 `config.ini`，将示例 API Key 替换成你自己的密钥。示例配置使用 DeepSeek；你也可以将其改为其他兼容 OpenAI API 的服务商。

### 3. 构建和启动

Linux/macOS：

```sh
go build -o lumachat .
./lumachat
```

Windows PowerShell：

```powershell
go build -o lumachat.exe .
.\lumachat.exe
```

默认监听地址为 `0.0.0.0:8080`。启动后在本机打开：

http://127.0.0.1:8080

如果只在本机使用，建议通过防火墙限制外部访问。若要更改监听地址或端口，请先检查当前版本源码中的服务监听设置。

## 配置说明

配置文件采用**明文、非加密 INI 格式**，文件名为 `config.ini`。仓库只包含不含真实密钥的 `config.example.ini`。

示例：

```ini
[provider_deepseek]
name=DeepSeek
base_url=https://api.deepseek.com
api_key=替换成你自己的API密钥
models=deepseek-chat,deepseek-reasoner
```

字段说明：

| 字段 | 含义 |
|---|---|
| section 名称 | 服务商的唯一标识，例如 `provider_deepseek` |
| `name` | 网页界面中显示的服务商名称 |
| `base_url` | 服务商的 API 根地址 |
| `api_key` | 服务商 API 密钥，按明文保存 |
| `models` | 逗号分隔的模型 ID 列表 |

不同服务商的 API 路径和模型 ID 可能不同，请以服务商文档为准。不要把带有真实密钥的 `config.ini` 上传到 GitHub、发送到公开 issue，或放入可公开下载的备份。

## 数据文件

- `config.ini`：服务商配置，含 API 密钥，明文存储。
- `users.json`：本地账户及相关聊天数据，由程序运行时使用。

这两个运行时文件已列入 `.gitignore`。部署时仍应自行确认备份、文件权限和服务器访问控制。

## 检查与构建

```sh
gofmt -w main.go types.go
go test ./...
go vet ./...
go build ./...
```

GitHub Actions 会在推送和 Pull Request 时自动执行上述检查。

## 安全与部署注意事项

1. **INI 不等于加密。** `config.ini` 中的 API Key 是明文，请限制文件系统权限；共享主机上不要让其他用户读取该文件。
2. **不要直接裸露在公网。** 默认监听所有网卡。公网部署至少应配置 HTTPS、反向代理、访问限制和防火墙。
3. **认证仍需加固。** 当前版本适合开发和个人测试。正式服务还应评估密码哈希算法、会话生命周期、注销与撤销机制、登录限速、CSRF/XSS 风险以及数据备份策略。
4. **不要复用重要密码。** 测试账户请使用专用密码。
5. **自行承担 API 费用。** 模型请求会使用你配置的服务商账户和密钥；请留意服务商的额度、价格和使用条款。

## 许可证

本项目采用 MIT License，详见 [LICENSE](LICENSE)。

## 问题反馈

提交 Issue 时，请附上复现步骤、Go 版本、操作系统和脱敏后的日志。**请勿发布 API Key、密码、Cookie、个人聊天记录或完整的敏感配置文件。**
