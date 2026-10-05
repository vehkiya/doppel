# Agent & Contributor Guidelines (`AGENTS.md`)

This document sets the quality gates and design rules for `doppel`. All automated agents and human contributors must follow it. [`SPEC.md`](SPEC.md) describes what doppel does; when behavior changes, update the spec in the same change.

---

## 1. Non-Negotiable Pre-Commit Quality Gates

Before committing, all of the following must pass cleanly:

1. **Formatting:** `gofmt -s -w .` leaves no changes.
2. **Tests:** `go test -race ./...` passes. The `-race` flag is mandatory.
3. **Linting:** `golangci-lint run` reports no issues (`errcheck`, `govet`, `staticcheck`, `gosec`, `unused`, …). Don't silence a warning with `//nolint` unless an inline comment says why.
4. **Dependencies:** `go mod tidy` leaves `go.mod` and `go.sum` unchanged.

---

## 2. Design Rules

### 2.1 Dependencies
* doppel is a single static binary: the Go standard library plus the Charm libraries (`bubbletea`, `bubbles`, `huh`, `lipgloss`). No CGO.
* At runtime it calls `git`, `ssh`, `ssh-keygen`, `ssh-add` and, optionally, `gh`. Nothing else.

### 2.2 Git config integrity
* **Git is the authority on its config format.** Read and write Git config through `git config --file` (`readConfigFile`, `configFile`, `reconcile`). The only text doppel writes itself is the generated index and the include block, which it fully controls.
* **Every account file sets every managed key** (`Account.settings`), including "reset" values such as `commit.gpgsign = false` and `core.sshCommand = ssh`. Git applies the default account first and the folder account on top, so a key one account leaves out leaks in from another.
* **Only managed keys are touched.** Settings a user adds to an account file, such as `pull.rebase`, must survive every command, including rename.
* **The include stays last.** doppel appends its include block to the end of the global Git config as text; `git config --add` would put it inside an existing `[include]` section.
* **Folder rules go from broad to specific** (`renderIndex`), because Git lets the last match win.
* **Folders are stored by their real path**, `~/`-shortened and ending in `/` (`NormalizeFolder`). Git matches repos by their real path, and the trailing `/` stops `~/projects/work` from matching `~/projects/workshop`.
* **Only `whoami` outside a repo matches folders itself.** Everywhere else, ask Git (`doppel.account`) which account applies.

### 2.3 Single write path
* Every file change goes through a `Plan`: stage, then `Apply`. `Apply` keeps a hidden backup of each file it replaces or removes (`.<name>.doppel.bak`) and writes atomically, preserving the file's mode and any symlink.
* Because commands only stage changes, `--dry-run` shows exactly what a real run would write. Never write a file outside a plan.
* doppel never overwrites or deletes key files.

### 2.4 Design system
Keep styling consistent with sshx's palette (`palette.go`):
* **Brand / Accent:** Charm Purple (`#7D56F4`)
* **Headers / Selections:** Coral Pink (`#FF5F87`)
* **Prompts / Cursors / Keys:** Vibrant Cyan (`#00D7D7`)
* **Success / Badges:** Spring Green (`#5FD787`)
* **Warnings:** Amber (`#FFAF00`)
* **Errors / Destructive actions:** Red (`#FF4672`)

### 2.5 Testing
* Tests never touch the real home directory or Git config. Use `newSandbox`, which points `HOME` and `XDG_CONFIG_HOME` at a temp directory and clears every `GIT_*` variable that could leak in a developer's own setup.
* Prefer end-to-end tests that run doppel and then ask real `git` what applies in a repo, over tests of internal functions.
