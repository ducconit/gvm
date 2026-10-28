---
name: g
description: >-
  Cross-platform Golang Version Manager CLI (`g` / `gvm`) for macOS (darwin), Linux, and Windows.
  Use this skill whenever the user or project needs to install, switch, alias, list, update, or
  remove Go toolchains, inspect Go environment variables, or invoke the `g mcp` server.
---

# `g` — Cross-Platform Golang Version Manager Skill

`g` is a fast, zero-dependency, cross-platform Go version manager supporting `darwin` (macOS), `linux`, and `windows` across `amd64`, `arm64`, `386`, and `armv6l`.

## 1. Quick Command Reference

| Command | Description | Examples |
| :--- | :--- | :--- |
| `g install <version>` / `g i <version>` | Install a specific Go version, minor series prefix, or alias | `g i 1.27`, `g install 1.27.3`, `g i latest`, `g i like` |
| `g alias <name> <version>` | Create or update a favorite version alias | `g alias like 1.27`, `g alias prod 1.26.4` |
| `g use <version>` | Set the active default Go version (by version, prefix, or alias) | `g use 1.27`, `g use like`, `g use 1.26.4` |
| `g ls` | List locally installed Go versions, aliases, active marker, and paths | `g ls` |
| `g ls -r` / `g ls --remote` | List official Go releases available for the current OS/Arch | `g ls -r`, `g ls --remote --all` |
| `g uninstall <version>` | Remove an installed Go version (or alias target) | `g uninstall 1.26.4`, `g uninstall like` |
| `g uninstall --all` | Remove all installed Go versions **except** the currently active version | `g uninstall --all` |
| `g uninstall` | Self-uninstall the `g` CLI and `$GVM_HOME` directory | `g uninstall -y` |
| `g update [version]` | Self-update `g` CLI to latest or a specific release version | `g update`, `g update 1.2.0` |
| `g -v` / `g version` | Display `g` CLI version and currently active Go toolchain info | `g -v`, `g version` |
| `g env` | Show active Go environment variables (`GVM_HOME`, `GOROOT`, `GOPATH`, `PATH`) | `g env` |
| `g mcp` | Start the built-in Model Context Protocol (MCP) server over `stdio` | `g mcp` |
| `g skills install` | Install this AI agent skill (`--path` or `--ide antigravity`) | `g skills install --ide antigravity` |
| `g -h` / `g --help` / `g help` | Show CLI help and command usage | `g --help` |

## 2. Environment & Directory Layout

- **`GVM_HOME`**: Defaults to `~/.gvm` (`%USERPROFILE%\.gvm` on Windows).
- **Installed Versions**: Stored in `$GVM_HOME/versions/<version>` (e.g., `~/.gvm/versions/1.27.3`).
- **Active Toolchain (`GOROOT`)**: Linked at `$GVM_HOME/current` (with binaries at `$GVM_HOME/current/bin`).
- **Aliases File**: Stored in `$GVM_HOME/aliases.json`.

### Shell `PATH` Integration
Ensure `$GVM_HOME/current/bin` and `$GOPATH/bin` are in `PATH`:
```bash
# macOS / Linux (~/.bashrc or ~/.zshrc)
export GOROOT="$HOME/.gvm/current"
export GOPATH="$HOME/go"
export PATH="$GOROOT/bin:$GOPATH/bin:$PATH"
```

```powershell
# Windows PowerShell ($PROFILE)
$env:GOROOT = "$HOME\.gvm\current"
$env:GOPATH = "$HOME\go"
$env:PATH = "$env:GOROOT\bin;$env:GOPATH\bin;$env:PATH"
```

## 3. MCP Server Integration (`g mcp`)

`g` includes a native Model Context Protocol (`JSON-RPC 2.0` over `stdio`) server. Add it to your agent/IDE MCP config:
```json
{
  "mcpServers": {
    "g-go-manager": {
      "command": "g",
      "args": ["mcp"]
    }
  }
}
```
Exposed MCP tools:
- `g_version`: Return CLI version and active Go version.
- `g_env`: Return active `GVM_HOME`, `GOROOT`, `GOPATH`, `GOBIN`, `GOOS`, `GOARCH`.
- `g_list_installed`: List all locally installed Go versions, aliases, and paths.
- `g_list_remote`: List official Go versions from `go.dev/dl`.
- `g_install`: Install a Go version or alias.
- `g_use`: Switch the active Go version.
- `g_alias`: Set a version alias.
- `g_uninstall_version`: Uninstall a specific Go version or all except active (`all: true`).
