# LumaChat

[English](README.md) | [简体中文](README.zh-CN.md)

**LumaChat** is a developer-oriented, self-hosted AI chat web app written in Go. It supports local accounts, multiple conversations, streaming responses, and manually configured OpenAI Chat Completions-compatible providers.

This project intentionally ships **without a default AI provider or API key**. Configure a compatible endpoint and its model IDs yourself.

## Features

- Local registration and login
- Independent multi-session chat history per account
- Custom OpenAI-compatible provider configuration
- Streaming responses using Server-Sent Events (SSE)
- Runtime provider configuration stored in a plain-text INI file and hot-reloaded
- Go standard-library backend; no third-party Go modules are required

## Requirements

- Go 1.20 or later to build
- An API endpoint compatible with the OpenAI Chat Completions API
- The provider's model ID and, when required, its API key

## Quick start

Clone and build:

```sh
git clone https://github.com/aaayQwQ/lumachat.git
cd lumachat
go build -o lumachat ./src
./lumachat
```

Windows PowerShell:

```powershell
git clone https://github.com/aaayQwQ/lumachat.git
Set-Location lumachat
go build -o lumachat.exe ./src
.\lumachat.exe
```

The first launch creates a `config` directory beside the executable and an empty `config/config.ini`. Start the app, register a local account, and add your own provider under **Settings → Add Provider**. You can load model IDs from the provider's `/models` endpoint or add them manually if that endpoint is unsupported.

To configure the provider before launching, copy the example into the runtime configuration directory:

Linux/macOS:

```sh
mkdir -p config
cp config.example.ini config/config.ini
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force config | Out-Null
Copy-Item config.example.ini config/config.ini
```

Edit `config/config.ini` and replace the example URL, model ID, and API key before starting the app.

## Provider configuration

`config/config.ini` is a plain-text INI file. It is created beside the executable; changing it triggers a reload after a short delay.

```ini
[provider_custom]
name=My Custom API
base_url=https://api.example.com/v1
api_key=replace-with-your-api-key
models=your-model-id
thinking_switch=false
search_extra_body=false
```

- Each INI section defines a provider. The section name is an identifier; the values are what the app uses.
- `base_url` is the API root. LumaChat appends `/chat/completions` for chat and `/models` for model discovery. Do not include either path in the base URL.
- `models` is a comma-separated list of model IDs. If the provider does not expose `/models`, enter the IDs manually in the UI.
- `api_key` is stored in plain text. It may be empty for endpoints that do not require authentication.
- `thinking_switch=true` opts into sending the non-standard `thinking: {type: enabled|disabled}` field. Keep it false unless your endpoint supports that field.
- `search_extra_body=true` opts into sending `extra_body.search=false` when the per-user setting disables web search. This is also non-standard and should remain false unless supported by your endpoint.

These provider-specific options default to false so a custom endpoint is not sent vendor-specific fields unexpectedly. LumaChat uses a Bearer authorization header for configured API keys.

## Runtime data

- `config/config.ini`: custom provider endpoints, model IDs, and API keys.
- `config/users.json`: local accounts, preferences, and chat history.

Both files contain sensitive data and are stored as plain text. Do not commit them or publish backups that contain real credentials.

## Checks and build

```sh
gofmt -w src/*.go
go test ./...
go vet ./...
go build ./...
```

GitHub Actions runs formatting, tests, vet, and build checks.

## Security notes

This is a developer-oriented foundation, not a hardened public multi-user service. It listens on `0.0.0.0:8080` by default. Before exposing it to a network, configure HTTPS, a reverse proxy, access controls, and a firewall. Password hashing, session lifetime, rate limiting, and other security controls should be reviewed before production deployment.

Never publish API keys, passwords, cookies, private conversations, or complete sensitive configuration files in issues or logs.

## License

MIT. See [LICENSE](LICENSE).
