# LumaChat

[English](README.md) | [简体中文](README.zh-CN.md)

**LumaChat** 是一个面向开发者的轻量级、自托管 AI 聊天网页应用，使用 Go 编写。支持本地账户、多会话、流式回复，以及自行配置兼容 OpenAI Chat Completions API 的服务商。

项目**不再内置 DeepSeek 官方服务商，也不会生成默认 API Key**。服务商地址、密钥和模型 ID 都需要开发者自行配置。

## 功能

- 本地注册与登录
- 每个账户独立的多会话聊天记录
- 自定义 OpenAI 兼容 API 服务商
- 基于 SSE 的流式回复
- INI 配置文件热加载
- 后端仅使用 Go 标准库，无需第三方 Go 模块

## 环境要求

- Go 1.20 或更新版本
- 兼容 OpenAI Chat Completions API 的服务端地址
- 服务商的模型 ID，以及接口要求时使用的 API Key

## 快速开始

Linux/macOS：

```sh
git clone https://github.com/aaayQwQ/lumachat.git
cd lumachat
go build -o lumachat ./src
./lumachat
```

Windows PowerShell：

```powershell
git clone https://github.com/aaayQwQ/lumachat.git
Set-Location lumachat
go build -o lumachat.exe ./src
.\lumachat.exe
```

首次启动会在**可执行文件所在目录**旁创建 `config/config.ini`，内容为空配置模板。启动网页后先注册本地账户，再进入 **设置 → 添加服务商**，填入你自己的 Base URL、API Key 和模型。若服务商支持 `/models`，可以自动获取模型；否则可以手动输入模型 ID。

也可以在启动前先创建配置文件：

Linux/macOS：

```sh
mkdir -p config
cp config.example.ini config/config.ini
```

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force config | Out-Null
Copy-Item config.example.ini config/config.ini
```

编辑 `config/config.ini`，把示例 URL、模型 ID 和 API Key 替换为实际配置。

## 服务商配置

`config/config.ini` 是明文 INI 文件，位于可执行文件旁的 `config` 目录。修改后程序会自动重新加载。

```ini
[provider_custom]
name=我的自定义 API
base_url=https://api.example.com/v1
api_key=替换成你自己的 API Key
models=your-model-id
thinking_switch=false
search_extra_body=false
```

- 每个 INI 节定义一个服务商。节名是标识，实际使用的是字段值。
- `base_url` 填 API 根地址。聊天请求会追加 `/chat/completions`，获取模型会追加 `/models`，不要把这两个路径放入 Base URL。
- `models` 是逗号分隔的模型 ID。如果服务商没有 `/models` 接口，可在网页中手动添加模型 ID。
- `api_key` 以明文存储；只有在你的接口不需要认证时才可以留空。
- `thinking_switch=true` 时，才会发送非标准字段 `thinking: {type: enabled|disabled}`。服务端明确支持时再开启。
- `search_extra_body=true` 时，在用户关闭联网搜索的情况下发送非标准字段 `extra_body.search=false`。服务端明确支持时再开启。

这些服务商专用参数默认关闭，避免向普通 OpenAI 兼容接口意外发送不支持的字段。配置 API Key 时程序使用 Bearer Authorization 请求头。

## 运行时数据

- `config/config.ini`：自定义服务商、模型和 API Key。
- `config/users.json`：本地账户、偏好设置与聊天历史。

这两个文件均为明文，可能包含敏感数据。不要将真实配置提交到 GitHub，也不要公开包含凭据的备份。

## 检查与构建

```sh
gofmt -w src/*.go
go test ./...
go vet ./...
go build ./...
```

GitHub Actions 会执行格式检查、测试、静态检查和构建。

## 安全说明

本项目面向开发和个人自托管实验，并非已加固的公网多用户生产系统。默认监听 `0.0.0.0:8080`。公开访问前应配置 HTTPS、反向代理、访问控制和防火墙；正式部署还需复审密码哈希、会话生命周期、限速等安全措施。

不要在 Issue 或日志中发布 API Key、密码、Cookie、私人聊天内容或完整的敏感配置文件。
