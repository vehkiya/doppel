# doppel 👥

[![Release](https://github.com/vehkiya/doppel/actions/workflows/cd.yml/badge.svg)](https://github.com/vehkiya/doppel/actions/workflows/cd.yml)
[![CodeQL Analysis](https://github.com/vehkiya/doppel/actions/workflows/codeql.yml/badge.svg)](https://github.com/vehkiya/doppel/actions/workflows/codeql.yml)
[![Linted with golangci-lint](https://img.shields.io/badge/linted%20with-golangci--lint-00ADD8?logo=go&logoColor=white)](https://golangci-lint.run)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**doppel** manages several Git accounts on one machine. Repos under `~/projects/work` commit as your work identity, push with your work SSH key and sign with your work signing key. Everything else uses your personal account. You never switch anything by hand.

doppel only writes plain Git config, which Git applies by itself: it doesn't wrap `git`, and it doesn't need to be running. It's a sibling of [sshx](https://github.com/vehkiya/sshx), built with the [Charm](https://charm.sh) stack.

```console
$ doppel whoami ~/projects/work/api
Repo      ~/projects/work/api
Account   work (folder ~/projects/work/)
Name      Jane Doe
Email     jane@acme.com
Auth key  ~/.ssh/id_ed25519_work (passphrase, in agent)
Signing   commits and tags · ~/.ssh/id_ed25519_work.pub
Login     ✓ github.com: logged in as jane-acme
```

---

## ✨ Features

* **Accounts per folder:**
  * Each account has a commit identity, an SSH key for fetching and pushing, an optional SSH key for signing, and the folders where it applies.
  * Nested folders work: the most specific one wins.
  * Repos outside every folder use the default account.
* **The right key, every time:**
  * Each account pushes with `ssh -i <its key> -o IdentitiesOnly=yes`, so ssh-agent can't offer another account's key first. If it could, GitHub would log you in as whichever account owns the first key that works.
  * Clones into a bound folder already use that account.
* **Signed commits that verify:**
  * SSH signing is set up per account.
  * doppel keeps a marked block in your `allowed_signers` file, so `git log --show-signature` shows a good signature. Other entries in that file, such as teammates' keys, are left alone.
* **Keys handled for you:**
  * Pick an existing key, or generate one (`ssh-keygen` asks for the passphrase).
  * Keys held by an agent work too: 1Password, Bitwarden, gpg-agent, or a key you load from Vault/OpenBao with `ssh-add`. See [Using a different SSH agent](#-using-a-different-ssh-agent).
* **GitHub uploads:**
  * `doppel upload` adds the keys to the account's GitHub user through `gh`, without switching `gh`'s active account.
  * Works with github.com (including GitHub Enterprise Cloud), GHE.com, and GitHub Enterprise Server.
  * `doppel export` gives step-by-step instructions for GitLab, Gitea, Forgejo, Codeberg and other hosts.
* **Checks:**
  * `doppel whoami` explains which account applies, and why.
  * `doppel test` logs in to each host and signs a test message.
  * `doppel doctor` finds anything that could make Git use the wrong account.
* **Interactive or scripted:** an account browser and step-by-step wizards in a terminal; plain flags for scripts.
* **Updates itself:** `doppel update` installs the latest release, but only one signed with doppel's release key. The browser says when a newer release is out.
* **Careful with your files:**
  * `--dry-run` shows every change as a diff.
  * Every write is atomic and keeps a backup.
  * `doppel uninstall` takes doppel back out of your Git config.

---

## 📦 Installation

doppel runs on Linux and macOS. It needs Git 2.34 or newer and OpenSSH 8.2 or newer. `gh` 2.40 or newer is optional; it's only needed for `doppel upload`.

### Release binary

Each [release](https://github.com/vehkiya/doppel/releases) has a prebuilt binary for Linux and macOS, on Intel (`amd64`) and ARM (`arm64`, including Apple silicon). These commands download the latest one for your machine, check it against the release's checksums, and install it in `~/.local/bin`:

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')        # linux or darwin
arch=$(uname -m)
case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
archive="doppel_${os}_${arch}.tar.gz"

cd "$(mktemp -d)"
curl -fsSLO "https://github.com/vehkiya/doppel/releases/latest/download/$archive"
curl -fsSLO "https://github.com/vehkiya/doppel/releases/latest/download/checksums.txt"
grep " $archive\$" checksums.txt | sha256sum -c -     # macOS without sha256sum: shasum -a 256 -c -
```

Go on only if that printed `doppel_<os>_<arch>.tar.gz: OK`. Then unpack it and install the binary:

```bash
tar -xzf "$archive"
mkdir -p ~/.local/bin
install -m 0755 doppel_*/doppel ~/.local/bin/doppel
doppel version
```

- **`doppel: command not found`?** Add `~/.local/bin` to your `PATH`, for example with `export PATH="$HOME/.local/bin:$PATH"` in `~/.zshrc` or `~/.bashrc`.
- **For every user on the machine:** install it with `sudo install -m 0755 doppel_*/doppel /usr/local/bin/doppel` instead. `doppel update` then needs `sudo` too, since it replaces the binary where it is.
- **A specific version:** replace `latest/download` with `download/<version>`, such as `download/v0.8.1`. The versioned archives, such as `doppel_v0.8.1_linux_amd64.tar.gz`, hold the same binary.
- **Downloaded in a browser on macOS?** macOS may refuse to open it. Clear the quarantine flag with `xattr -d com.apple.quarantine ~/.local/bin/doppel`. Files downloaded with `curl` aren't flagged.
- **Checking more:** to also check the signature on `checksums.txt` and GitHub's build provenance, see [SECURITY.md](SECURITY.md#verifying-a-release).

Once installed, `doppel update` keeps it current, and only installs releases signed with doppel's release key.

### With Go

```bash
go install github.com/vehkiya/doppel@latest
```

This builds doppel from source into `$(go env GOPATH)/bin`, which needs to be on your `PATH`.

### With configsh

With [configsh](https://github.com/vehkiya/configsh), `./setup.sh` installs doppel and sets up the `dop` alias.

---

## 🚀 Quick start

```bash
doppel add                      # a wizard: identity, hosts, folders, keys, signing
doppel                          # the account browser
doppel whoami                   # which account applies here, and why
doppel test                     # log in to each host and sign a test message
doppel upload work              # add work's keys to its GitHub user
```

The first `doppel add` offers to start from the identity already in your `~/.gitconfig`, including an existing SSH signing setup.

The same without questions, for scripts:

```bash
doppel add personal --name "Jane Doe" --email jane@personal.dev --generate-auth-key --sign-with-auth-key
doppel add work --name "Jane Doe" --email jane@acme.com --github-user jane-acme \
  --folder ~/projects/work --auth-key ~/.ssh/id_ed25519_work --sign-with-auth-key
```

---

## 🧭 Commands

| Command | What it does |
| :--- | :--- |
| `doppel` | Opens the account browser in a terminal; otherwise lists accounts |
| `doppel ls` | Lists accounts, their keys and folders |
| `doppel add [<id>]` | Adds an account, with a wizard or with flags |
| `doppel edit <id>` | Changes an account, with a wizard or with flags (`--host` and `--folder` replace the list) |
| `doppel rm <id>` | Deletes an account; key files are kept |
| `doppel rename <id> <new-id>` | Changes an account's ID |
| `doppel bind <id> <folder>...` | Uses an account for repos in these folders |
| `doppel unbind <folder>...` | Removes folder rules |
| `doppel default [<id> \| --none]` | Shows or sets the default account |
| `doppel whoami [path] [--offline]` | Shows which account applies, and why; tries the repo's host unless `--offline` |
| `doppel test [<id>]` | Logs in to each host and signs and verifies a test message |
| `doppel export <id> [--auth\|--signing] [--no-copy]` | Prints and copies a public key, with where to add it on each host (`--no-copy` only prints it) |
| `doppel upload <id> [--auth\|--signing]` | Adds the keys to the account's GitHub user through `gh` |
| `doppel doctor [--fix]` | Checks for anything that could make Git use the wrong account; `--fix` redoes doppel's own files |
| `doppel update [--check] [--force]` | Installs the latest signed release over this binary (`--check` only looks, `--force` reinstalls the current one) |
| `doppel uninstall` | Removes doppel from your Git config and `allowed_signers`; accounts and keys are kept |
| `doppel completion zsh\|bash\|fish` | Prints a shell completion script |

`ls`, `rm` and `update` also answer to `list`, `remove` or `delete`, and `upgrade`.

**Key flags** for `add` and `edit`:
- Auth key: `--auth-key <key>` or `--generate-auth-key`. On `edit`, `--auth-key ""` goes back to ssh's own keys.
- Signing key: `--signing-key <key>`, `--generate-signing-key`, `--sign-with-auth-key`, or `--no-signing`.
- `--sign-commits=false` or `--sign-tags=false` sign only tags, or only commits.

**Shell completion** covers commands, flags, account IDs, bound folders and key files, for `doppel` and `dop`. Load it from your shell's startup file:

```bash
source <(doppel completion zsh)     # ~/.zshrc, after compinit
source <(doppel completion bash)    # ~/.bashrc
doppel completion fish | source     # ~/.config/fish/config.fish
```

With [configsh](https://github.com/vehkiya/configsh), `.zshrc` already does this.

Commands that change files accept `--dry-run` and `--yes`. Set `ACCESSIBLE=1` for plain prompts instead of interactive forms, for screen readers. Set `DOPPEL_NO_UPDATE_CHECK=1` to stop the browser checking for new releases.

### Account browser keys

| Key | Action |
| :--- | :--- |
| `enter` / `e` | Edit the account |
| `a` | Add an account |
| `b` | Bind a folder |
| `*` | Make it the default |
| `x` | Export a key |
| `u` | Upload keys to GitHub |
| `t` | Test logins and signing |
| `d` | Delete (asks first) |
| `/` | Filter |
| `tab` | Details (on narrow terminals) |
| `U` | Update doppel, when a newer release is out |
| `q` / `esc` | Quit |

In the wizards, **Shift+Tab** goes back to an earlier page and **Esc** cancels without saving. The folder and key-file inputs complete paths as you type: **Tab** accepts the suggestion shown.

---

## ⚙️ How it works

```
~/.gitconfig                                  + one include line, at the end
~/.config/doppel/index.gitconfig              generated: the default account, then folder rules
~/.config/doppel/accounts/<id>.gitconfig      one file per account
~/.ssh/allowed_signers                        + one marked block
```

The index uses Git's own [`includeIf "gitdir:…"`](https://git-scm.com/docs/git-config#_conditional_includes):

```ini
[include]
	path = ~/.config/doppel/accounts/personal.gitconfig     # the default account
[includeIf "gitdir:~/projects/"]
	path = ~/.config/doppel/accounts/personal.gitconfig
[includeIf "gitdir:~/projects/work/"]
	path = ~/.config/doppel/accounts/work.gitconfig         # more specific, so it wins
```

**Account files:**
- **Every setting, every time:** each account file sets every setting doppel manages, including values that only reset the default account's (`commit.gpgsign = false`, `core.sshCommand = ssh`), so nothing leaks from one account to another.
- **Your own settings stay:** settings doppel doesn't manage, such as a per-account `pull.rebase`, survive every change.

**Folders:**
- **Real location:** folders are stored by their real path (symlinks resolved), with a trailing `/`. Git matches a repo by its real path, and the slash stops `~/projects/work` from also matching `~/projects/workshop`.
- **Matching is per repo:** an account applies to repos inside its folders. A plain folder that isn't a repo has no account. `doppel whoami` on a clone target that doesn't exist yet says which account the clone will get.

---

## 🔑 Using a different SSH agent

doppel never talks to an agent itself. Two programs do, and both use the agent that `SSH_AUTH_SOCK` points at:
- **ssh,** when Git fetches and pushes with an account's `core.sshCommand` (`ssh -i <key> -o IdentitiesOnly=yes`)
- **`ssh-keygen -Y sign`,** when Git signs a commit

So to keep keys in another agent, such as 1Password, Proton Pass, Bitwarden or gpg-agent, point `SSH_AUTH_SOCK` at it and give each account the key's public half. Nothing else in doppel changes.

### 1. Point `SSH_AUTH_SOCK` at the agent

Set it in your shell profile, so Git, `ssh-keygen` and doppel's checks all use the same agent:

```bash
export SSH_AUTH_SOCK="$HOME/.1password/agent.sock"    # 1Password on Linux
```

| Agent | `SSH_AUTH_SOCK` |
| :--- | :--- |
| 1Password (macOS) | `$HOME/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock` |
| 1Password (Linux) | `$HOME/.1password/agent.sock` |
| Bitwarden (Linux, macOS `.dmg`) | `$HOME/.bitwarden-ssh-agent.sock` (Snap and the Mac App Store build keep it in their own folder; see Bitwarden's docs) |
| gpg-agent (`enable-ssh-support`) | `$(gpgconf --list-dirs agent-ssh-socket)` |

Paths can change between app versions, so check the app's own docs. With the right path, `ssh-add -L` lists the agent's keys.

**Why not `IdentityAgent`?** `IdentityAgent` in `~/.ssh/config` only works for fetching and pushing, because ssh is the only program that reads that file. Git signs with `ssh-keygen`, and doppel checks keys with `ssh-add`, and both only look at `SSH_AUTH_SOCK`. With the agent set only in `IdentityAgent`, pushes work but signing fails, and `doppel doctor` says the key isn't loaded.

### 2. Give the account the public key

The agent keeps the private key. doppel needs the public half as a file, usually in `~/.ssh`:

```bash
ssh-add -L                                                    # every key the agent holds
ssh-add -L | grep 'Work key' > ~/.ssh/id_ed25519_work.pub     # pick one by its comment
doppel edit work --auth-key ~/.ssh/id_ed25519_work.pub --sign-with-auth-key
```

The wizard offers `.pub` files in `~/.ssh` too. Most apps can also copy the public key for you; in 1Password, it's the key item's public key field.

### 3. Check it

`doppel test work` logs in and signs through the agent. `doppel whoami` marks a key the agent holds with "in agent", and `doppel doctor` warns when the agent doesn't hold it right now, for example while the app is locked.

### Good to know

- **One agent for every account.** `SSH_AUTH_SOCK` names a single agent, so keep all your agent-held keys in it. Keys on disk still work alongside it, but some agents don't accept keys from `ssh-add` (1Password's doesn't). A passphrase-protected key on disk then can't be cached, so Git keeps asking for its passphrase: move it into the agent too.
- **An agent full of keys is fine.** `IdentitiesOnly=yes` makes ssh offer only the account's own key. GitHub can't log you in as the wrong user, and servers don't stop with "Too many authentication failures".
- **Secret stores without an agent,** such as Vault, OpenBao or KeePassXC, load keys into the agent you already use. There's nothing to point; keep the public key in `~/.ssh` and load the private key yourself:
  ```bash
  bao kv get -field=private_key secret/ssh/work | ssh-add -t 8h -
  doppel edit work --auth-key ~/.ssh/id_ed25519_work.pub --sign-with-auth-key
  ```
- **macOS Keychain:** doppel offers to keep a passphrase in the Keychain only for keys on disk. A key that lives in an agent doesn't need it.

---

## 🏢 GitHub Enterprise

`doppel upload`, `test`'s GitHub user check and `export`'s GitHub steps work on any GitHub host:

* **github.com:** GitHub Enterprise Cloud, including Enterprise Managed Users, lives here too. `ssh.github.com` (port 443) counts as the same host.
* **`*.ghe.com`:** GitHub Enterprise Cloud with data residency.
* **GitHub Enterprise Server:** any host `gh` is signed in to (`gh auth login -h github.acme.com`).

An account's GitHub username applies to all its GitHub hosts. Separate identities, say a personal user and an Enterprise Server user, belong in separate accounts.

---

## 🛡️ Safety

* **What changes:** doppel only changes two files of yours, and only with one block each: the include line in your global Git config and its block in `allowed_signers`. Everything else lives in `~/.config/doppel`.
* **How it writes:** every write goes through a plan. `--dry-run` prints it as a diff. A real run writes atomically, keeps symlinks, and keeps the last three versions of every file it changes as hidden backups next to it (`.<name>.doppel.bak`, then `.bak.1` and `.bak.2`). If any step fails, it puts back everything it already wrote.
* **A damaged file can't lock doppel out:** doppel's own Git calls run without your global config, so even a broken folder-rules file can't stop doppel from rewriting it. doppel won't append to a `~/.gitconfig` that Git itself can't read.
* **Key files:** doppel never overwrites or deletes them.
* **Getting out:** `doppel uninstall` takes the include and the `allowed_signers` block back out.

---

## 🧪 Development

See [`AGENTS.md`](AGENTS.md) for the quality gates and design rules, and [`SPEC.md`](SPEC.md) for the full specification. Before committing:

```bash
gofmt -s -w . && go test -race ./... && golangci-lint run && go mod tidy
```

Tests never touch your real home directory, Git config, ssh-agent, GitHub, or any host. They run real `git` and `ssh-keygen` in a sandbox, with stand-ins for `ssh` and `gh`.

## License

[MIT](LICENSE)
