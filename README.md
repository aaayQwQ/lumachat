# LumaChat

[English](README.md) | [简体中文](README.zh-CN.md)

**LumaChat** is a lightweight, self-hosted AI chat web application written in Go. It provides local account login, multi-conversation chat, OpenAI-compatible model provider configuration, and streaming responses over Server-Sent Events (SSE).

> **Project status:** This is a buildable foundation intended for personal testing and self-hosting experiments. Read the security notes below before exposing it to a network. It should not currently be exposed directly to the public internet as a production multi-user service.

## Features

- Local user registration and login
- Separate chat conversations for each account
- Support for providers and models compatible with the OpenAI API
- SSE streaming responses
- Provider configuration through a plain-text INI file
- Backend uses the Go standard library and requires no third-party Go modules
- GitHub Actions checks formatting, tests, static analysis, and builds

## Requirements

- Go 1.20 or later
- An API endpoint compatible with the OpenAI Chat Completions API
- An API key for the selected provider

## Quick start

### 1. Clone the repository

```sh
git clone https://github.com/aaayQwQ/lumachat.git
cd lumachat
```

### 2. Create the configuration file

Linux/macOS:

```sh
cp config.example.ini config.ini
```

Windows PowerShell:

```powershell
Copy-Item config.example.ini config.ini
```

Edit `config.ini` and replace the example API key with your own. The example configuration uses DeepSeek; you can adapt it for another OpenAI-compatible provider.

### 3. Build and run

Linux/macOS:

```sh
go build -o lumachat .
./lumachat
```

Windows PowerShell:

```powershell
go build -o lumachat.exe .
.\lumachat.exe
```

The default listen address is `0.0.0.0:8080`. After startup, open this URL in your browser:

http://127.0.0.1:8080

If you only intend to use the application locally, restrict external access with your firewall. To change the listen address or port, check the server listener settings in the source code for the version you are running.

## Configuration

The application uses a **plain-text, unencrypted INI file** named `config.ini`. The repository includes `config.example.ini`, which should not contain real credentials.

Example:

```ini
[provider_deepseek]
name=DeepSeek
base_url=https://api.deepseek.com
api_key=replace_with_your_own_api_key
models=deepseek-chat,deepseek-reasoner
```

| Field | Description |
|---|---|
| Section name | Unique provider identifier, such as `provider_deepseek` |
| `name` | Provider name displayed in the web interface |
| `base_url` | Provider API base URL |
| `api_key` | Provider API key, stored as plain text |
| `models` | Comma-separated list of model IDs |

API paths and model IDs vary by provider. Refer to the provider's official documentation. Never commit a real `config.ini`, publish it in a public issue, or include it in a publicly downloadable backup.

## Runtime data files

- `config.ini`: provider configuration, including API keys stored in plain text.
- `users.json`: local account and related chat data used by the application.

Both runtime files are listed in `.gitignore`. You are still responsible for backups, file permissions, and access controls on the host.

## Checks and build

```sh
gofmt -w main.go types.go
go test ./...
go vet ./...
go build ./...
```

GitHub Actions runs these checks on pushes and pull requests.

## Security and deployment notes

1. **INI is not encryption.** API keys in `config.ini` are stored as plain text. Restrict filesystem permissions, especially on shared hosts.
2. **Do not expose the application directly to the public internet.** It listens on all network interfaces by default. A public deployment should use HTTPS, a reverse proxy, access restrictions, and a firewall.
3. **Authentication needs further hardening.** This version is intended for development and personal testing. Before production use, review password hashing, session lifetime, logout and token revocation, login rate limiting, CSRF/XSS risks, and backup procedures.
4. **Do not reuse important passwords.** Use a dedicated password for test accounts.
5. **You are responsible for API charges.** Requests use the provider account and API key you configure. Check the provider's quotas, pricing, and terms.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).

## Reporting issues

When opening an issue, include reproduction steps, your Go version, operating system, and sanitized logs. **Do not post API keys, passwords, cookies, private chat history, or complete sensitive configuration files.**
