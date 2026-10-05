# doppel 👥

[![Release](https://github.com/vehkiya/doppel/actions/workflows/cd.yml/badge.svg)](https://github.com/vehkiya/doppel/actions/workflows/cd.yml)
[![CodeQL Analysis](https://github.com/vehkiya/doppel/actions/workflows/codeql.yml/badge.svg)](https://github.com/vehkiya/doppel/actions/workflows/codeql.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/vehkiya/doppel)](https://goreportcard.com/report/github.com/vehkiya/doppel)
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
  * Keys held by an agent work too: 1Password, Proton Pass, or a key you load from Vault/OpenBao with `ssh-add`.
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

```bash
go install github.com/vehkiya/doppel@latest
```

Or download a release binary for Linux or macOS from [Releases](https://github.com/vehkiya/doppel/releases). Each release's checksums are signed; [SECURITY.md](SECURITY.md#verifying-a-release) shows how to verify a download.

Once installed, `doppel update` keeps it current.

With [configsh](https://github.com/vehkiya/configsh), `./setup.sh` installs doppel and sets up the `dop` alias.

doppel needs Git 2.34 or newer and OpenSSH 8.2 or newer. `gh` is optional; it's only needed for `doppel upload`.

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
| `doppel export <id> [--auth\|--signing]` | Prints and copies a public key, with where to add it on each host |
| `doppel upload <id> [--auth\|--signing]` | Adds the keys to the account's GitHub user through `gh` |
| `doppel doctor [--fix]` | Checks for anything that could make Git use the wrong account; `--fix` redoes doppel's own files |
| `doppel update [--check] [--force]` | Installs the latest signed release over this binary (`--check` only looks) |
| `doppel uninstall` | Removes doppel from your Git config and `allowed_signers`; accounts and keys are kept |

**Key flags** for `add` and `edit`:
- Auth key: `--auth-key <key>` or `--generate-auth-key`. On `edit`, `--auth-key ""` goes back to ssh's own keys.
- Signing key: `--signing-key <key>`, `--generate-signing-key`, `--sign-with-auth-key`, or `--no-signing`.
- `--sign-commits=false` or `--sign-tags=false` sign only tags, or only commits.

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

In the wizards, **Shift+Tab** goes back to an earlier page and **Esc** cancels without saving.

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

## 🔑 Keys held by an agent

An auth key can be just a `.pub` file whose private half lives in an agent:

* **1Password, Proton Pass:** export the public key into `~/.ssh`, and point `SSH_AUTH_SOCK` at the app's agent, or let the app load keys into your usual agent.
* **Vault / OpenBao:** neither runs an agent, so keep the public key in `~/.ssh` and load the private key into your agent yourself:
  ```bash
  bao kv get -field=private_key secret/ssh/work | ssh-add -t 8h -
  doppel edit work --auth-key ~/.ssh/id_ed25519_work.pub --sign-with-auth-key
  ```

`doppel whoami` and `doppel doctor` say when such a key isn't loaded right now.

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
* **How it writes:** every write goes through a plan. `--dry-run` prints it as a diff. A real run writes atomically, keeps symlinks, and keeps a hidden `.<name>.doppel.bak` backup of every file it changes.
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
