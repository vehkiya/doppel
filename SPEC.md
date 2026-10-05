# doppel: Specification

**Status:** Draft v1 · 2026-10-05

## 1. Summary

**doppel** (alias **`dop`**) manages several Git accounts on one machine. Each account has a commit identity (name and email), an SSH key for logging in to Git hosts, an optional SSH key for signing commits, and the folders where it applies. Any repo inside a work folder uses the work account. Repos anywhere else use the default account.

doppel works by writing plain Git config that Git applies on its own. It doesn't wrap `git`, and it doesn't need to run for Git to pick the right account. If doppel is uninstalled, the accounts keep working.

It's a sibling of [sshx](https://github.com/vehkiya/sshx): the same stack, look and conventions (see sshx's `AGENTS.md`).

## 2. Goals and non-goals

**Goals (v1)**
- Define accounts and bind folders to them, with nested folders resolved predictably.
- Set up an auth key and an optional signing key per account, generating keys when needed.
- Upload keys to GitHub, and give step-by-step manual instructions for every other host.
- Explain which account applies in any folder, and why.
- Detect setups that would make Git silently use the wrong account.
- Never damage existing config or keys.

**Non-goals (v1)**
- HTTPS remotes and tokens (credential helpers). `doctor` reports HTTPS remotes it finds but doesn't change them.
- Managing `~/.ssh/config` hosts. That's sshx's job.
- GPG signing keys.
- GitHub Enterprise Server, and automatic uploads to GitLab, Gitea, Forgejo and others. Those hosts are covered by `export`.
- Windows.
- Per-repo overrides. `git config --local` already handles those, and `whoami` shows them.
- Self-update. Planned for later, following sshx.

## 3. Concepts

| Term | Meaning |
| :--- | :--- |
| **Account** | A named identity: commit name and email, the hosts it's used on, an optional GitHub username, an auth key, an optional signing key, and its folders. The ID is short and lowercase (`work`, `personal`). |
| **Default account** | The account used for repos outside every bound folder. There's at most one; the first account created becomes the default. |
| **Folder** | A directory bound to one account. It applies to every repo whose `.git` directory is inside it, at any depth. |
| **Auth key** | The SSH key used to fetch and push. One per account, shared by all of the account's hosts. |
| **Signing key** | The SSH key used to sign commits and tags. It can be none, the same as the auth key, or a separate key. |

## 4. Requirements

### R1. Accounts

- **R1.1** Create, list, edit and delete accounts.
- **R1.2** Account fields:
  - **ID** (required, unique, `[a-z0-9-]+`)
  - **Name** (required): the commit author name
  - **Email** (required): the commit author email
  - **Hosts:** defaults to `github.com`; there can be several
  - **GitHub username** (optional): used for uploads and to check that keys log in as the right user
  - **Auth key** (optional)
  - **Signing key** (optional)
  - **Sign commits / sign tags:** both default to on when there's a signing key
  - **Folders**
- **R1.3** Deleting an account removes its account file, its folders and its `allowed_signers` entry. It never deletes key files. If the account was the default, doppel asks in a terminal which account becomes the new default, or none. Without a terminal it leaves none and says how to pick one.
- **R1.4** Renaming an account (changing its ID) keeps everything else unchanged.
- **R1.5 First run.** If the global Git config already has `user.name` and `user.email`, the add wizard for the first account offers to start from them.
  - It also brings in any SSH signing setup (`gpg.format = ssh`, `user.signingkey`, `commit.gpgsign`, `tag.gpgsign`) and an auth key named with `-i` in `core.sshCommand`.
  - Every value can still be changed before saving.
  - The global settings stay where they are; doppel's include overrides them.

### R2. Folders and the default account

- **R2.1** An account can have any number of folders. A folder belongs to at most one account.
- **R2.2** A folder applies to repos whose `.git` directory is inside it. Matching is by where the repo lives, not by the current directory:
  - A plain folder that isn't a repo has no account.
  - `git clone` into a bound folder already uses that account.
  - A linked worktree follows its main repo, wherever the worktree is placed.
  - *(All three verified on Git 2.56.)*
- **R2.3** Nested folders are allowed, and the most specific folder wins. Example: `~/projects/` → personal, `~/projects/work/` → work.
- **R2.4** Repos outside every folder use the default account. With no default account, Git's own global config applies as before.
- **R2.5** Paths:
  - Stored by their real location, with symlinks resolved. Git matches a repo by its real path, so a rule written with a symlinked folder path only matches when the shell happens to be in that symlinked path. *(Verified on Git 2.56.)*
  - Stored with `~` when under the home directory, including a home directory reached through a symlink, since Git matches `~/` rules through it. *(Verified on Git 2.56.)*
  - Normalized to an absolute path with a trailing `/`, so `~/projects/work` never matches `~/projects/workshop`.
  - Matched case-insensitively on macOS.
  - A folder that doesn't exist yet is allowed, with a warning. Its nearest existing parent is resolved instead.
  - Paths containing glob characters (`*`, `?`, `[`), `\`, `"` or line breaks are rejected, because Git would read them as a pattern.
  - Folders read back from hand-edited account files are checked the same way, and must be absolute or start with `~/`, before the folder rules are written. A bad value would make the rules unreadable to Git, or match folders anywhere on disk.
- **R2.6** Binding a folder that already belongs to another account asks before moving it.

### R3. Auth key

- **R3.1** Choose an existing key from `~/.ssh`, enter a custom path, or generate a new Ed25519 key. Generated keys are named `~/.ssh/id_ed25519_<account>`.
- **R3.2** Generation asks for a passphrase and warns, without blocking, if it's left empty.
  - `ssh-keygen` asks on the terminal, so generating needs one. Without a terminal, doppel stops and suggests generating the key yourself and passing it with `--auth-key` or `--signing-key`.
  - `--dry-run` doesn't generate anything. It says which key would be created, and the `allowed_signers` diff shows a placeholder where its public key would go.
- **R3.3** A key can be given as a `.pub` file only, when its private half lives in an agent (for example 1Password or a hardware key).
- **R3.4** Stored as `doppel.authKey` and applied as `core.sshCommand = ssh -i <key> -o IdentitiesOnly=yes`. `IdentitiesOnly` stops ssh-agent from offering another account's key first; GitHub logs you in as whichever account owns the first key that works. An account without an auth key sets `core.sshCommand = ssh`, so it never inherits another account's key.
- **R3.5** GitHub only lets a key belong to one account. doppel warns when two accounts that share a host use the same auth key.
- **R3.6** Login check: for each host, run `ssh -T git@<host>` with the account's key and look for the expected username in the greeting.
  - `doppel test` runs it, and lets ssh ask for a passphrase or whether to trust a new host.
  - `whoami` runs it for the repo's remote host without letting ssh ask anything (`--offline` skips it). If the key has a passphrase and isn't loaded in the agent, it says so and suggests `ssh-add`, rather than reporting the key as rejected.
  - On GitHub, a login as a different user than the account's GitHub username counts as a failure. For example, GitHub replies "Hi `<username>`!", GitLab "Welcome to GitLab, `@<username>`!", and Gitea/Forgejo "Hi there, `<username>`!".

### R4. Signing key

- **R4.1** Choice: none, the same key as the auth key, or a separate key (existing, custom path, or generated as `~/.ssh/id_ed25519_<account>_signing`).
- **R4.2** Sets `gpg.format = ssh`, `user.signingkey = <key>.pub`, `commit.gpgsign` and `tag.gpgsign`. With no signing key, both `gpgsign` settings are explicitly `false`.
- **R4.3** `allowed_signers` has one entry per account (`<email> namespaces="git" <public key>`), kept inside a marked block that doppel owns. Entries outside the block, such as teammates' keys, are never touched. Changing an account's email or signing key updates its entry.
- **R4.4** Uses the file in `gpg.ssh.allowedSignersFile` if one is set in the global config files themselves. Otherwise it uses `~/.ssh/allowed_signers` and sets that option in the generated index.
- **R4.4a** If a signing key's public key can't be read when saving, doppel stops and names the fix (`doppel edit <id> --signing-key <key>` or `--no-signing`), rather than silently dropping that key from `allowed_signers`.
- **R4.5** Signing check: sign and verify a test message with `ssh-keygen -Y sign` and `ssh-keygen -Y verify`, against the same `allowed_signers` file Git uses. `doppel test` runs it.

### R5. Getting keys onto hosts

- **R5.1 `upload` (GitHub):** `gh ssh-key add --type authentication|signing --title "doppel: <account> (<hostname>)"`.
  - Runs with the token from `gh auth token --user <username>`, so the key goes to the account's GitHub user without switching gh's active account.
- **R5.2** Before uploading, doppel checks:
  - **Already uploaded?** It compares against `gh api user/keys` and `user/ssh_signing_keys`, and reports "already on GitHub" instead of failing.
  - **Token scopes:** the token must have `admin:public_key` and/or `admin:ssh_signing_key`. If one is missing, doppel prints the exact `gh auth refresh -h github.com -s <scope>` command.
  - **Signed in:** if gh isn't installed or isn't signed in as that user, doppel falls back to `export`.
- **R5.3 `export` (any host):**
  - Prints the public key and copies it to the clipboard. When two different keys are shown, nothing is copied; `--auth` or `--signing` picks one.
  - Gives step-by-step instructions, including which key type to choose, for GitHub, GitLab (usage type *Authentication*, *Signing*, or *Authentication & Signing*), Gitea/Forgejo, and other hosts.
  - When the same key is both the auth and signing key, it says to add it once with both uses where the host allows that.

### R6. `whoami`

- **R6.1** Works in any folder, or on a path argument, and shows:
  - the account in effect, and the folder rule (or default) that selected it
  - the effective name, email, auth key and signing key, with whether signing is on
  - the login check result for the repo's remote host
- **R6.2** It asks Git which account applies (each account file carries `doppel.account`), so the result always matches what Git will actually do. It never re-implements folder matching.
- **R6.3** It warns when a value in effect doesn't come from doppel, for example a `--local` override or a global setting placed after doppel's include. It shows where each value comes from, using `git config --show-origin`.
- **R6.4** Outside any repo, it shows which account a repo created there would get. Git can only answer this for an existing repo, so here doppel applies the same folder rules itself.
  - This also covers a path that doesn't exist yet, such as a clone target.
  - For a plain folder inside an enclosing repo, such as a home directory managed with yadm, it shows the enclosing repo's account, plus the account a new repo there would get when that differs. *(Verified: pointing `GIT_DIR` at a `.git` that doesn't exist yet makes Git skip the folder rules.)*

### R7. `doctor`

Checks every account and prints how to fix each problem it finds:
- Key files exist, have a passphrase, and whether they're loaded in the agent (the agent check is information only).
- Every signing key has a matching entry in `allowed_signers`.
- Every bound folder exists.
- doppel's include is still the last section of the global Git config, and nothing after it sets a setting doppel manages.
- `GIT_SSH_COMMAND` and `GIT_SSH` aren't set in the environment, because they override every account.
- `~/.ssh/config` doesn't set `IdentityFile` for one of an account's hosts. If the account's key were rejected, ssh would fall back to that key and quietly log in as a different account.
- Bound folders don't contain repos with HTTPS remotes, which these settings don't cover (reported, not changed).
- Git is 2.34 or newer, and OpenSSH supports `ssh-keygen -Y`.

### R8. Safety

- **R8.1 Changes to files doppel doesn't own:**
  - one `[include]` block appended to the end of the global Git config (whichever of `~/.gitconfig` or `$XDG_CONFIG_HOME/git/config` Git uses)
  - the marked block in `allowed_signers`

  Everything else lives in doppel's own directory.
- **R8.1a** The include goes in the global file Git reads last.
  - If it's found in another global file, doppel moves it. For example, `~/.config/git/config` may hold the include from before `~/.gitconfig` existed.
  - With `GIT_CONFIG_GLOBAL` set, that file is used exactly as Git uses it, without expanding `~`.
- **R8.2** Every write is atomic (a temporary file in the same directory, then a rename), preserves the file's mode and symlinks (including a dangling symlink, whose target is created), and keeps a hidden backup of the previous version alongside it (`.gitconfig.doppel.bak`), like sshx.
  - A command takes all its backups before replacing any file, and removes files only after every write has succeeded. A failure partway can leave an extra file behind, but never a folder rule pointing at a missing account file.
- **R8.3** Key files are never overwritten or deleted. Generating a key at an existing path is refused.
- **R8.4** `--dry-run` on any command that writes shows the file changes it would make, without writing.
- **R8.5** `doppel uninstall` removes the include block and the `allowed_signers` block from every global config file, leaving the account files and keys in place. If Git has since added other `include.path` lines to doppel's `[include]` section, only doppel's path is removed.

### R9. Interface

- **R9.1** `doppel` with no arguments opens an account browser in the style of sshx when both stdin and stdout are terminals. Otherwise it prints `ls`.
  - **Left pane:** account IDs and emails, with the default marked ★; `/` filters.
  - **Right pane:** name, email, hosts, GitHub user, folders, keys with status badges (passphrase, in agent), signing, and the account file. On terminals narrower than 100 columns, Tab switches between the list and the details.
  - **Keys:** edit (`enter`/`e`), add (`a`), delete (`d`, then `y` to confirm), bind a folder (`b`), make default (`*`), export (`x`), test (`t`), quit (`q`/`esc`). Upload (`u`) comes with milestone 4.
  - Each action leaves the browser, runs as its command would, and returns to the same account. A one-line result shows in the browser; output to read (`export`, `test`) and warnings or errors wait for Enter first.
- **R9.1a** `doppel add` and `doppel edit <id>` without flags, in a terminal, walk through a wizard instead:
  - identity, hosts and GitHub user
  - folders and whether it's the default
  - auth key: keep, generate, a key found in `~/.ssh` (including `.pub` files for agent-held keys), another file, or none
  - signing, and what to sign
  - a review before saving

  With any account or key flag, or without a terminal, they never ask: scripts get errors, not questions.
- **R9.1b** With `ACCESSIBLE` set, as in other Charm tools, forms become plain line-by-line prompts for screen readers.
- **R9.2** Every action is also a subcommand, with flags for non-interactive use, so configsh or scripts can set up accounts.
- **R9.3** `dop` is an alias for `doppel` (a shell alias in configsh, like sshx's `fssh`), and doppel behaves identically under either name.

## 5. Command line

```
doppel                                   Browse accounts (prints ls when not in a terminal)
doppel ls                                List accounts
doppel add [id]                          Add an account (wizard, or flags below)
doppel edit <id>                         Edit an account (wizard, or flags below)
doppel rm <id>                           Delete an account (keys are kept)
doppel rename <id> <new-id>              Change an account's ID
doppel bind <id> <folder>...             Bind folders to an account
doppel unbind <folder>...                Remove folder bindings
doppel default [<id> | --none]           Show or set the default account
doppel whoami [path] [--offline]         Show which account applies here, and why
doppel test [<id>]                       Log in to each host and sign a test message
doppel doctor                            Check every account for problems
doppel export <id> [--auth|--signing]    Print and copy a public key, with host instructions
doppel upload <id> [--auth|--signing]    Upload keys to GitHub with gh
doppel uninstall                         Remove doppel's changes to your Git and SSH files
doppel version

Flags for add and edit:
  --name, --email, --host (repeatable), --github-user,
  --auth-key <path> | --generate-auth-key,
  --signing-key <path> | --generate-signing-key | --sign-with-auth-key | --no-signing,
  --sign-commits=false, --sign-tags=false (sign only tags, or only commits),
  --auth-key "" (edit: go back to ssh's own keys),
  --folder (repeatable), --default
Global:
  --dry-run, --yes
```

## 6. Technical design

### 6.1 Files

Paths follow `XDG_CONFIG_HOME`:

```
~/.gitconfig                                  + one include block at the end (R8.1)
~/.config/doppel/index.gitconfig              generated: default account, then folder rules
~/.config/doppel/accounts/<id>.gitconfig      one per account (source of truth)
~/.ssh/allowed_signers                        + one marked block (R4.3)
```

**Account files are the source of truth.** `index.gitconfig` is generated entirely from them, so it can always be rebuilt. There's no hidden state file. Hand edits to account files are respected, and `git config --show-origin` explains any value.

### 6.2 Account file

Every account file sets every setting doppel manages, including a "reset" value for anything it doesn't use. Git applies the default account first and the folder account on top, so an unset value would otherwise leak in from the default account.

```ini
[doppel]
    account = work
    default = false
    host = github.com
    githubUser = jane-at-acme
    folder = ~/projects/work/
    authKey = ~/.ssh/id_ed25519_work
[user]
    name = Jane Doe
    email = jane@acme.com
    signingkey = ~/.ssh/id_ed25519_work_signing.pub
[gpg]
    format = ssh
[commit]
    gpgsign = true
[tag]
    gpgsign = true
[core]
    sshCommand = ssh -i ~/.ssh/id_ed25519_work -o IdentitiesOnly=yes
```

- Git ignores the `[doppel]` section. Only the single-valued `doppel.account` is read through Git (R6.2).
- **What doppel reads back vs. derives:**
  - Settings with a one-to-one Git equivalent are read back from the file, so hand edits to them stick: `user.name`, `user.email`, `user.signingkey` (the signing key's `.pub`), `commit.gpgsign`, `tag.gpgsign`.
  - `core.sshCommand` combines several values, so it is always regenerated from `doppel.authKey`, and hand edits to it are overwritten.
  - doppel's own fields live under `[doppel]`.
- **Other settings are kept.** Settings doppel doesn't manage, such as a per-account `pull.rebase`, are never touched, so an account file can carry any extra Git settings for that account.
- The list-valued keys (`host`, `folder`) build up across includes, so doppel reads them from each account file directly with `git config --file`, never from a repo's combined config.

### 6.3 Generated index

```ini
# Generated by doppel. Do not edit: changes are overwritten.
[include]
    path = ~/.config/doppel/accounts/personal.gitconfig     # default account
[includeIf "gitdir:~/projects/"]
    path = ~/.config/doppel/accounts/personal.gitconfig
[includeIf "gitdir:~/projects/work/"]
    path = ~/.config/doppel/accounts/work.gitconfig
```

- The default account comes first. Folder rules follow, sorted from broad to specific (by path depth, then alphabetically), because Git lets the last match win.
- On macOS the condition is `gitdir/i:`.
- Folder paths always end in `/` (R2.5).

### 6.4 Include block in the global config

```ini
# Added by doppel. Keep this at the end of the file so doppel's accounts take effect.
[include]
    path = ~/.config/doppel/index.gitconfig
```

- This block is appended as text rather than with `git config --add`, which would put it inside an existing `[include]` section that may not be last.
- Everything else is read and written with `git config --file <file>` (syntax that works on Git 2.34), never a hand-written parser. Git's quoting and escaping rules are subtle, and Git is the authority on its own format.
- These single-file calls run with `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1`. A broken or unusual user config can then neither stop doppel nor change what it writes, and doppel can always regenerate a damaged index.

### 6.5 External tools

| Tool | Used for | Required |
| :--- | :--- | :--- |
| `git` ≥ 2.34 | reading and writing config, `whoami` | yes |
| `ssh-keygen` | generating keys, fingerprints, passphrase check, signing check | yes |
| `ssh` | login check | yes |
| `ssh-add` | agent status | no |
| `gh` | GitHub uploads | no (falls back to `export`) |

### 6.6 Code and conventions

- Go (the same version as sshx). Charm stack: Bubble Tea, Bubbles, Huh, Lip Gloss. Static binary, no CGO, macOS and Linux.
- Code is organized in packages under `internal/`. `main.go` stays at the repo root, so `go install github.com/vehkiya/doppel@latest` builds a `doppel` binary, as with sshx.
  ```
  main.go          calls cli.Run
  internal/
    cli/       commands, flags, prompts, whoami, the wizards, and the loop around the browser
    tui/       the account browser (picks an action; cli carries it out)
    accounts/  the Account model, its managed settings, loading and validation
    store/     writing accounts: account files, the generated index, the global include
    plan/      staged writes with backups and atomic replacement (what --dry-run previews)
    paths/     where files live; normalizing and matching folders
    git/       running git; reading and writing Git config files through it
    keys/      reading, generating and checking SSH keys; the login and signing checks
    ui/        palette, styles and diff rendering
    version/   build version
    testenv/   a sandboxed home directory and Git environment for tests
  ```
  A later milestone adds `github/` (uploads through gh).
- The palette, badges and Huh theme are copied from sshx. The quality checks follow sshx's `AGENTS.md`: `gofmt -s`, `go test -race`, `golangci-lint`, a tidy `go.mod`. A doppel `AGENTS.md` adds the rules from §6.2 to §6.4.

### 6.7 Testing

- Every test runs in a sandbox: a temporary `HOME`, `XDG_CONFIG_HOME` and `GIT_CONFIG_GLOBAL`, with `GIT_CONFIG_NOSYSTEM=1`.
- Fake `ssh`, `ssh-keygen` (where real key generation isn't needed) and `gh` executables on `PATH` record calls and return canned output.
- Integration tests run real `git` against sandboxed repos:
  - folder precedence
  - clones into bound folders
  - worktrees
  - leak-proofing between the default and folder accounts
  - anything placed after the include block in the global config
- Table tests cover path normalization and rule ordering. The generated files are compared against saved expected output.
- CI runs on Linux and macOS (macOS for `gitdir/i:` and its case-insensitive filesystem).

### 6.8 Distribution

- `go install github.com/vehkiya/doppel@latest`, plus GitHub release binaries with signed checksums and build provenance, reusing sshx's workflows.
- configsh's `build-tools.sh` installs doppel, and `.zshrc` adds `alias dop="doppel"`.
- Cheatsheet section 8 is rewritten around doppel.

## 7. Milestones

1. **Core:** account files, generated index, include block, default account, folders, `ls`, `whoami`, flag-based `add`/`edit`/`rm`/`bind`/`unbind`/`default`, `--dry-run`, `uninstall`.
2. **Keys:** choosing and generating auth and signing keys, `allowed_signers` block, login check, signing check, `test`, `export`.
3. **Interactive:** Huh wizards for add and edit, the TUI list view, first-run import.
4. **GitHub and checks:** `upload` through gh, `doctor`.
5. **Release:** CI and release workflows, README, configsh integration.

## 8. Open questions

- Repository visibility and license. Proposed: public and MIT, like sshx.
