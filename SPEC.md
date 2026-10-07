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
  - **Email** (required): the commit author email. It's also the principal in `allowed_signers`, which reads commas and `*`, `?`, `!` as patterns, so those, quotes and spaces are refused.
  - **Hosts:** defaults to `github.com`; there can be several
  - **GitHub username** (optional): used for uploads and to check that keys log in as the right user
  - **Auth key** (optional)
  - **Signing key** (optional)
  - **Sign commits / sign tags:** both default to on when there's a signing key
  - **Folders**
- **R1.3** Deleting an account removes its account file, its folders and its `allowed_signers` entries, retired ones (R4.3) included: doppel stops trusting its keys. To keep verifying its old commits, keep a copy of its entries outside doppel's block. It never deletes key files. If the account was the default, doppel asks in a terminal which account becomes the new default, or none. Without a terminal it leaves none and says how to pick one.
- **R1.4** Renaming an account (changing its ID) keeps everything else unchanged.
- **R1.5 First run.** On the first interactive run (with 0 accounts in doppel, when running `doppel` or `doppel add`), doppel checks the global Git config for existing identities.
  - **Multi-account import:** When multiple accounts are detected—via a top-level `[user]` base identity and/or conditional `[includeIf "gitdir:..."]` (and `gitdir/i:`) directives pointing to files with identity settings (`user.name`/`user.email`)—doppel displays a summary card of discovered accounts and prompts to import them.
    - Suggested IDs are derived from the included files' names (e.g. `.gitconfig-work` -> `work`) or bound folders, sanitized and deduplicated. The base identity defaults to `personal` (or `default`) and is marked as default.
    - Accepting the import creates doppel account files for each identity, creates a backup of the global config (`.<name>.doppel.bak`), cleans up the imported `includeIf` directives, generates `index.gitconfig`, and appends doppel's include block.
    - Declining leaves the global Git config untouched and proceeds to a fresh setup.
  - **Single identity:** If only a single identity is found, the add wizard offers to start from it (its name, email, SSH signing setup, and auth key from `core.sshCommand`), which can be reviewed and edited before saving.
  - Non-interactive invocations and scripts never prompt.
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
  - **Keys come last:** doppel stages the whole change with that placeholder before it generates anything. A change that can't be saved, such as one where another account's signing key can't be read (R4.4a), fails before the passphrase is asked, so no key is left behind and the same command works once the cause is fixed. If a save still fails after the key exists (the write lock, or a file changed meanwhile), the key is kept, and the error says to use it with `--auth-key` or `--signing-key`.
  - **After saving, so Git doesn't keep asking for the passphrase:**
    - On macOS with Apple's `ssh`, doppel offers (default yes; `--yes` accepts) to run `ssh-add --apple-use-keychain <key>` for each new key with a passphrase. ssh-add asks for it once more, keeps it in the login Keychain and loads the key into the agent. doppel then says that ssh reads it back for hosts with `UseKeychain yes` and `AddKeysToAgent yes` in `~/.ssh/config` (which `doctor` checks), and that signing only uses keys in the agent, which `ssh-add --apple-load-keychain` refills after logging in.
    - Elsewhere, or with another `ssh` (such as Homebrew's, which has no Keychain support), it suggests `ssh-add <key>`.
    - **Why:** OpenSSH asks in the terminal unless `SSH_ASKPASS` names a dialog. On Linux, a desktop session usually sets one (for example `ksshaskpass`, which keeps the passphrase in KWallet). macOS has none, so the Keychain is how it remembers.
- **R3.3** A key can be given as a `.pub` file only, when its private half lives in an agent (for example 1Password or a hardware key).
- **R3.4** Stored as `doppel.authKey` and applied as `core.sshCommand = ssh -i <key> -o IdentitiesOnly=yes`. `IdentitiesOnly` stops ssh-agent from offering another account's key first; GitHub logs you in as whichever account owns the first key that works. An account without an auth key sets `core.sshCommand = ssh`, so it never inherits another account's key.
- **R3.5** GitHub only lets a key belong to one account. doppel warns when two accounts that share a host use the same auth key.
- **R3.6** Login check: for each host, run `ssh -T git@<host>` with the account's key and look for the expected username in the greeting.
  - `doppel test` runs it, and lets ssh ask for a passphrase or whether to trust a new host. Without a terminal it asks nothing, as `whoami` does.
  - `whoami` runs it for the repo's remote host without letting ssh ask anything (`--offline` skips it). If the key has a passphrase and isn't loaded in the agent, it says so and suggests `ssh-add`, rather than reporting the key as rejected.
  - On macOS with Apple's `ssh`, the suggestion is `ssh-add --apple-use-keychain <key>`, and `doppel test` in a terminal also names it under each account for every key with a passphrase that still isn't in the agent afterwards.
  - On GitHub, a login as a different user than the account's GitHub username counts as a failure. For example, GitHub replies "Hi `<username>`!", GitLab "Welcome to GitLab, `@<username>`!", and Gitea/Forgejo "Hi there, `<username>`!".

### R4. Signing key

- **R4.1** Choice: none, the same key as the auth key, or a separate key (existing, custom path, or generated as `~/.ssh/id_ed25519_<account>_signing`).
- **R4.2** Sets `gpg.format = ssh`, `user.signingkey = <key>.pub`, `commit.gpgsign` and `tag.gpgsign`. With no signing key, both `gpgsign` settings are explicitly `false`.
  - The signing key may also be written inline, `user.signingkey = key::ssh-ed25519 AAAA…`, as Git allows: R1.5 brings one in as it is, and a hand edit is kept. Its private half then lives in an agent. `allowed_signers`, `test`, `export` and `upload` use it like a key in a file.
- **R4.3** `allowed_signers` has one entry per signing account (`<email> namespaces="git" <public key>`), kept inside a marked block that doppel owns. Entries outside the block, such as teammates' keys, are never touched. Changing an account's email or signing key updates its entry.
  - **Older commits keep verifying.** When an account changes its signing key or email, or stops signing, doppel keeps the old entry with `valid-before="<the time of the change>"`. A commit signed before then still shows "Good signature"; one signed with the old key afterwards isn't trusted.
  - The account file holds them (`doppel.retiredSigner = <YYYYMMDDHHMMSS> <email> <key type> <key>`, §6.2), so `doctor --fix` or any other rewrite of the block keeps them. They're checked like any other field before they're written.
  - doppel finds what to retire by comparing the block it wrote last with the accounts. An entry goes to the account that has its email or key now, or had them in the file it was loaded from, so changing both at once is covered, and so is a hand edit that `doctor --fix` then picks up.
  - The time is local, without the `Z` that marks UTC, which OpenSSH 8.9 can't read. Git gives ssh-keygen each commit's time in local time too.
  - Checking a signature at the commit's time needs Git 2.35 and OpenSSH 8.8. With older versions, retired entries are skipped and older commits stop verifying, as they did before retired entries existed.
  - The new entry gets no `valid-after`: OpenSSH before 8.8 would skip it, and signing would stop verifying altogether.
  - The index keeps pointing Git at the file while it holds only retired entries, after an account stops signing.
- **R4.4** Uses the file in `gpg.ssh.allowedSignersFile` if the user set one in the global config, or in a file it includes (`[include]`, not `[includeIf]`). Otherwise it uses `~/.ssh/allowed_signers` and sets that option in the generated index.
  - doppel follows the includes itself, letting Git parse each file, so it can skip its own: the value the index sets isn't mistaken for the user's, and a damaged index can't stop doppel from rewriting it.
- **R4.4a** If a signing key's public key can't be read when saving, doppel stops and names the fix (`doppel edit <id> --signing-key <key>` or `--no-signing`), rather than silently dropping that key from `allowed_signers`.
- **R4.5** Signing check: sign and verify a test message with `ssh-keygen -Y sign` and `ssh-keygen -Y verify`, against the same `allowed_signers` file Git uses. `doppel test` runs it.

### R5. Getting keys onto hosts

- **R5.1 `upload` (GitHub):** `gh ssh-key add - --type authentication|signing --title "doppel: <account> (<hostname>)"`, on every GitHub host the account uses. The public key goes to gh on stdin, so an inline key (R4.2) uploads like one in a file.
  - **Which hosts are GitHub:**
    - `github.com`, which GitHub Enterprise Cloud (including Enterprise Managed Users) shares
    - its port-443 SSH endpoint `ssh.github.com`
    - `*.ghe.com` (GitHub Enterprise Cloud with data residency)
    - any other host `gh` is signed in to. `gh` only signs in to GitHub, so this is how GitHub Enterprise Server is found. The check reads `gh auth status --hostname <host> --json hosts` (plain `gh auth status --hostname <host>` on a gh too old for `--json`), so no token is ever fetched just to answer it, and nothing connects. Each command asks about a host at most once; the browser asks again each time it opens.
  - **Pointing gh at a host:** `GH_HOST` targets each host.
  - **Unrecognized hosts:** if no host is recognized, doppel suggests `gh auth login -h <host>` for an Enterprise Server, or `export` for anything else.
  - Runs with the token from `gh auth token --user <username>`, so the key goes to the account's GitHub user without switching gh's active account. That needs gh 2.40 or newer: with an older gh, `upload` says so and falls back to `export`.
  - **The user's spelling:** gh matches user names exactly, so doppel first reads the signed-in accounts from `gh auth status --hostname <host> --json hosts` (which prints no tokens), finds the account's user ignoring case, and gives gh its spelling. A gh too old to have `--json` is given the name as stored. When the stored name differs from GitHub's only in capitals, `upload` offers to correct it in a terminal, and otherwise prints the `doppel edit` command that does.
- **R5.2** Before uploading, doppel checks:
  - **Already uploaded?** It compares against `gh api user/keys` and `user/ssh_signing_keys`, and reports "already on GitHub" instead of failing.
  - **Token scopes:** the token must have `admin:public_key` (or `write:public_key`) for auth keys, and `admin:ssh_signing_key` (or `write:ssh_signing_key`) for signing keys. If one is missing, doppel prints the exact command to add it. gh only refreshes its active account, so for another account the command switches to it, refreshes, and switches back.
  - **The right user:** doppel checks the token really belongs to the account's GitHub user. `GH_TOKEN`, `GITHUB_TOKEN`, their `_ENTERPRISE_` forms and `GH_HOST` in the environment are ignored, so they can't send keys to a different account or host.
  - **Signed in:** if gh isn't installed or isn't signed in as that user, doppel falls back to `export`. The warning carries gh's own reason and lists the accounts gh does have on that host.
- **R5.2a** `upload` needs a GitHub host and a GitHub user; otherwise it says what to do. A key that both logs in and signs is added once as each kind.
  - **One user everywhere:** the account's GitHub user applies to all its GitHub hosts. Separate identities, such as a personal github.com user and an Enterprise Server user, belong in separate accounts.
  - **Same detection elsewhere:** `export`'s GitHub steps (with that host's settings page), `test`'s GitHub-user check, the wizard's GitHub-username question and `doctor`'s note all use it.
- **R5.3 `export` (any host):**
  - Prints the public key and copies it to the clipboard. When two different keys are shown, nothing is copied; `--auth` or `--signing` picks one.
  - Gives step-by-step instructions, including which key type to choose, for GitHub, GitLab (usage type *Authentication*, *Signing*, or *Authentication & Signing*), Gitea/Forgejo, and other hosts.
  - When the same key is both the auth and signing key, it says to add it once with both uses where the host allows that.

### R6. `whoami`

- **R6.1** Works in any folder, or on a path argument, and shows:
  - the account in effect, and the folder rule (or default) that selected it
  - the effective name, email, auth key and signing key, with whether signing is on
  - the login check result for the repo's remote host. For an HTTPS remote, which the SSH keys don't cover, it gives the `git remote set-url` command that switches it to SSH instead.
- **R6.2** It asks Git which account applies (each account file carries `doppel.account`), so the result always matches what Git will actually do. Where doppel has to match folders itself (R6.4, and to name the rule that applied), it uses the same ordered rules the index is generated from (`accounts.FolderRules` and `accounts.MatchFolder`), so the two can't disagree.
- **R6.3** It warns when a value in effect doesn't come from doppel, for example a `--local` override or a global setting placed after doppel's include. It shows where each value comes from, using `git config --show-origin`.
- **R6.4** Outside any repo, it shows which account a repo created there would get. Git can only answer this for an existing repo, so here doppel applies the same folder rules itself.
  - This also covers a path that doesn't exist yet, such as a clone target.
  - For a plain folder inside an enclosing repo, such as a home directory managed with yadm, it shows the enclosing repo's account, plus the account a new repo there would get when that differs. *(Verified: pointing `GIT_DIR` at a `.git` that doesn't exist yet makes Git skip the folder rules.)*
- **R6.5 Exit status:** `whoami` exits 0 whenever it successfully determines the identity in effect (or that no account applies). Non-zero exit codes are reserved for operational errors (such as an unreadable directory or Git failure).
- **R6.6 Machine-readable output:** `whoami --json` prints the identity and repository state as JSON:
  - `in_repo`: boolean, true if path is inside a Git repository
  - `repo`: string, top-level repository path (empty outside a repository)
  - `path`: string, path inspected
  - `exists`: boolean, whether the path exists
  - `account`: string, ID of the applying account (empty if none)
  - `rule`: string, rule selecting the account (`folder <path>`, `default account`, `matched by Git`, or empty)
  - `name`: string, effective Git `user.name`
  - `email`: string, effective Git `user.email`
  - `auth_key`: string, effective auth key reference (empty if using default SSH keys)
  - `ssh_command`: string, effective `core.sshCommand`
  - `signing_key`: string, effective `user.signingkey` (empty if none)
  - `sign_commits`: boolean, effective `commit.gpgsign`
  - `sign_tags`: boolean, effective `tag.gpgsign`
  - `overrides`: array of strings, warnings for config settings or environment variables overriding account settings
  - `new_repo_account`: string, account a new repo created here would get (empty if none)
  - `new_repo_rule`: string, rule selecting new repo account (empty if none)
  - `login`: object or null, result of remote login check (`ok`, `skipped`, `label`, `detail`, `fix`)
  - `stale_index`: boolean, true if doppel's index is missing or out of date
- **R6.7 Stale index detection:** If doppel's index is missing or out of date (older than `accounts/` or any account file, or mismatched rules/default), `whoami` emits a warning suggesting `doppel doctor --fix`.

### R7. `doctor`

Checks everything that could make Git use the wrong account, and prints a one-line fix under each finding.
- A **problem** means Git may use the wrong account or fail, and makes `doctor` exit with status 1. A **warning** works but is worth knowing.
- Without `--fix`, doctor writes nothing.
- **Output streams:** doctor's diagnostic findings and summary are printed to stdout, as the command's primary output. stderr is reserved for command errors (such as invalid arguments or unreadable config).

**Git and SSH**
- Git is 2.34 or newer (checked before any command runs), and OpenSSH is 8.2 or newer, as Git needs to verify SSH signatures. *(problem)*
- `GIT_SSH_COMMAND` and `GIT_SSH` aren't set, because they override every account. *(problem)*
- On macOS, the `ssh` on `PATH` is Apple's, the only one that can keep passphrases in the Keychain. *(warning)*
  - doppel asks `ssh -G -F /dev/null -o UseKeychain=yes` about a host that doesn't exist: other builds refuse the option, and nothing connects.

**Git config**
- The global Git config includes doppel's index. *(problem)*
- Nothing after the include sets an identity setting (`user.name`, `user.email`, `user.signingkey`, `gpg.format`, `commit.gpgsign`, `tag.gpgsign`, `core.sshCommand`), which would override every account. *(problem)*
- doppel's files say what the accounts say: account files, the index, the `allowed_signers` block and the include. A hand edit to `core.sshCommand` in an account file shows up here. *(warning)*
- Git itself can read the global config and everything it includes (`git config --global --includes --list`, run with the user's own config, unlike doppel's other Git calls). A bad line in a file doppel wrote, or in any file it includes, makes every Git command fail, and only this check sees it. *(problem)*
- `--fix` rewrites exactly these files, through the same plan and the same validation as any save (R8.2a), so they match the accounts again. It refuses, naming the file and the value, when an account file holds a folder doppel wouldn't write (R2.5). It also restores a missing include. It doesn't touch anything else.

**Each account**
- **Keys:**
  - its auth key exists *(problem)*
  - its keys have a passphrase *(warning)*
  - an agent-held key is unlocked in the agent right now *(warning)*
  - a signing key with a passphrase is in the agent, the auth key included when the account signs with it *(warning)*. `ssh-keygen` signs with the agent's copy and never reads the macOS Keychain, so otherwise every signed commit asks for the passphrase. The fix is `ssh-add <key>` (`ssh-add --apple-use-keychain <key>` on a Mac with Apple's `ssh`), or starting an agent when none is running.
  - the signing key's public key can be read *(problem)*
- **Shared keys:** two accounts on the same host don't use the same auth key, or both rely on ssh's own keys. Either way, one would log in as the other. *(problem)*
- **Folders:** every bound folder exists. *(warning)*
- **SSH config:** `~/.ssh/config` doesn't offer another `IdentityFile` for one of the account's hosts. If the account's key were rejected, ssh would fall back to that key and could log in as someone else. *(warning)*
  - doppel finds these by comparing `ssh -G -F ~/.ssh/config <host>` with a host that doesn't exist, so `Include`, `Match` and wildcards count as ssh counts them, and nothing connects.
- **Keychain (macOS with Apple's `ssh`):** when the auth key has a passphrase, `~/.ssh/config` sets `UseKeychain yes` and `AddKeysToAgent yes` for each of the account's hosts. Without the first, macOS asks for the passphrase in the terminal; without the second, ssh doesn't load the key into the agent, so signing with it asks. *(warning)*
  - `AddKeysToAgent` comes from `ssh -G`. `ssh -G` doesn't print `UseKeychain`, so doppel reads that one setting from the file itself, as ssh would: the first value that applies wins, and `Host` blocks (with wildcards and `!`) and `Include` count. A `Match` block is taken to apply, so an unusual setup isn't reported as missing it.
- **HTTPS remotes:** no repos in the account's folders fetch or push over HTTPS, which these keys don't cover. Reported only, never changed. *(warning)*
  - Each repo gets its own warning, up to five per account, then a count. The fix is the exact command, such as `git -C ~/projects/personal/configsh remote set-url origin git@github.com:vehkiya/configsh.git`, with `--push` for a push URL that's HTTPS on its own.
  - The SSH URL is `git@<host>:<path>`, as GitHub, GitLab, Gitea and most hosts take it. A user name or token in the HTTPS URL is dropped, so it's never printed. A URL with a port gets a generic example instead, since the host's SSH port can't be told from it.
  - To stay quick on big trees, doppel looks three levels deep and skips hidden, `node_modules` and `vendor` folders.

### R8. Safety

- **R8.1 Changes to files doppel doesn't own:**
  - one `[include]` block appended to the end of the global Git config (whichever of `~/.gitconfig` or `$XDG_CONFIG_HOME/git/config` Git uses)
  - the marked block in `allowed_signers`

  Everything else lives in doppel's own directory.
- **R8.1a** The include goes in the global file Git reads last.
  - If it's found in another global file, doppel moves it. For example, `~/.config/git/config` may hold the include from before `~/.gitconfig` existed.
  - With `GIT_CONFIG_GLOBAL` set, that file is used exactly as Git uses it, without expanding `~`.
- **R8.2** Every write is atomic (a temporary file in the same directory, then a rename), preserves the file's mode and symlinks (including a dangling symlink, whose target is created), and keeps hidden backups of the last three versions alongside it, like sshx: `.gitconfig.doppel.bak` is the newest, then `.gitconfig.doppel.bak.1` and `.gitconfig.doppel.bak.2`.
  - Changes are staged as copies in `~/.config/doppel/.staging` (private, 0700) before they're applied, not in the shared temp directory, since a copy of the global config can hold secrets. The copies are deleted when the command ends, and a command that was killed has its leftovers deleted by a later one.
  - A command takes all its backups before replacing any file, writes next, and removes files last, so a folder rule never points at a missing account file.
  - **A failed write is rolled back.** If any step fails, doppel puts back what it already did: replaced files get their old content, created files are deleted, and the backups return to how they were. The error says so, or, if a restore failed too, lists what to put back by hand. After a failure every file is as it was, and running the command again works once the cause (such as a read-only global config) is fixed.
- **R8.2a** Accounts reach disk only through `store.Save`, which validates first (R2.5, one default, unique IDs, each folder bound once) and is used by every command and by `doctor --fix`.
- **R8.2b** Concurrent and stale writes:
  - **Lock:** writes take an advisory lock on `~/.config/doppel/.lock` (`flock`). A command that can't ask anything (no terminal) takes it before loading the accounts, so concurrent commands run one after the other. One that may ask, such as a wizard or a confirmation, takes it only for the final save, so a prompt never keeps other commands waiting. A command that waits more than 15 seconds for the lock gives up and says so.
  - **Stale reads:** a command remembers what each account file held when it loaded it. Before writing, it fails with "… changed since it was read; run the command again" when a file was edited, deleted, or added since, and `Apply` makes the same check against the content each file was staged from. Nothing is overwritten.
  - **Explicit removals:** only `rm` (the account it was given) and `rename` (the old file) delete an account file. An account file the command didn't load is never deleted.
- **R8.3** Key files are never overwritten or deleted. Generating a key at an existing path is refused.
- **R8.4** `--dry-run` on any command that writes shows the file changes it would make, without writing.
- **R8.5** `doppel uninstall` removes the include block and the `allowed_signers` block from every global config file, leaving the account files and keys in place. If Git has since added other `include.path` lines to doppel's `[include]` section, only doppel's path is removed.

### R9. Interface

- **R9.1** `doppel` with no arguments opens an account browser in the style of sshx when both stdin and stdout are terminals and `ACCESSIBLE` is not set. Otherwise, or with `ACCESSIBLE` set, it prints `ls`.
  - **Left pane:** account IDs and emails, with the default marked ★; `/` filters.
  - **Right pane:** name, email, hosts, GitHub user, folders, keys with status badges (passphrase, in agent), signing, and the account file. On terminals narrower than 100 columns, Tab switches between the list and the details.
  - **Keys:** edit (`enter`/`e`), add (`a`), delete (`d`, then `y` to confirm), bind a folder (`b`), make default (`*`), export (`x`), upload to GitHub (`u`), test (`t`), quit (`q`/`esc`).
  - Each action leaves the browser, runs as its command would, and returns to the same account. A one-line result shows in the browser; output to read (`export`, `test`) and warnings or errors wait for Enter first.
- **R9.1a** `doppel add` and `doppel edit <id>` without flags, in a terminal, walk through a wizard instead:
  - identity, hosts and GitHub user
  - folders and whether it's the default
  - auth key: keep, generate, a key found in `~/.ssh` (including `.pub` files for agent-held keys), another file, or none
  - signing, and what to sign
  - a review before saving

  The wizard is a single form:
  - Shift+Tab goes back to any earlier page, keeping what was typed, even an answer that isn't finished or valid yet. Each page checks its answer when the user moves on, and Save checks every page once more. A page that's hidden by then, such as the GitHub username once no host is GitHub, doesn't count.
  - The key choices stay the same throughout. One that doesn't fit the other answers, such as signing with an auth key when there isn't one, or generating a key where a file already exists, is refused with the reason. The description says which file Generate creates.
  - Esc or Ctrl+C cancels from any page without saving; the same keys cancel every other prompt too.
  - The review page reflects the answers as they stand, including changes made after going back.
  - The path inputs complete what's typed, as ghost text that Tab accepts. Without a suggestion, Tab moves on as in every other input; Enter always moves on.
    - **Folders:** folders only, and only the last one after a comma, keeping the ones before it.
    - **Key files:** SSH keys and folders to look in; in the home folder, `~/.ssh/` comes first.
    - `~/` and paths relative to the current folder work. Hidden entries show once a `.` is typed. Names match case-sensitively, except on macOS, whose filesystem ignores case.
    - Accessible prompts don't suggest anything.

  With any account or key flag, or without a terminal, they never ask: scripts get errors, not questions.
- **R9.1b** With `ACCESSIBLE` set, as in other Charm tools, forms become plain line-by-line prompts for screen readers, and `doppel` with no arguments prints `ls` instead of opening the full-screen browser.
- **R9.2** Every action is also a subcommand, with flags for non-interactive use, so configsh or scripts can set up accounts.
- **R9.2b Machine-readable output:** `doppel ls --json` outputs an array of account objects with stable fields: `id`, `name`, `email`, `default`, `hosts`, `github_user`, `folders`, `auth_key`, `signing_key`, `sign_commits`, and `sign_tags`. An empty list produces `[]`.
- **R9.2a Shell completion:** `doppel completion zsh|bash|fish` prints a script that completes commands, flags, account IDs (with their emails), the folders bound to accounts for `unbind`, known hosts for `--host`, folders for `--folder`, `bind` and `whoami`, and files for `--auth-key` and `--signing-key`. It covers `dop` too.
  - The scripts are thin. They run the hidden `doppel __complete <words>`, which prints the candidates and whether the shell should complete paths itself, so `~` and quoting work as in the shell's own completion.
  - doppel learns each command's flags from the command itself, without running it, so completion can't fall behind the flags. `__complete` never writes, never asks anything, and doesn't fail: without Git or accounts it offers less.
- **R9.3** `dop` is an alias for `doppel` (a shell alias in configsh, like sshx's `fssh`), and doppel behaves identically under either name.

### R10. Self-update

- **R10.1** `doppel update` installs the latest release over the running binary. `--check` only says whether there's a newer one; `--force` reinstalls even when up to date. It doesn't need Git, so it works even when Git is the problem.
- **R10.2** It only installs a release whose `checksums.txt` has a valid Ed25519 signature (`checksums.txt.sig`) from a key built into doppel (`TrustedKeys`), and whose archive matches its checksum. An unsigned release, an unknown key, or a mismatched archive is refused.
- **R10.3** The new binary replaces the old one atomically (a temporary file in the same folder, then a rename). Without permission to write there, doppel says to update with the tool that installed it, or with `sudo`.
- **R10.4** Commands check for a newer release on startup, at most every 6 hours (cached in the user cache directory), and print a notice to stderr when an update is available. The browser checks in the background when it opens, shows a notice and enables `U` to update. Development builds don't check, and neither does anything with `DOPPEL_NO_UPDATE_CHECK` set. After updating, doppel displays an `UPDATED` badge and prompts to restart.

## 5. Command line

```
doppel                                   Browse accounts (prints ls when not in a terminal or under ACCESSIBLE)
doppel ls [--json]                       List accounts
doppel add [id]                          Add an account (wizard, or flags below)
doppel edit <id>                         Edit an account (wizard, or flags below)
doppel rm <id>                           Delete an account (keys are kept)
doppel rename <id> <new-id>              Change an account's ID
doppel bind <id> <folder>...             Bind folders to an account
doppel unbind <folder>...                Remove folder rules
doppel default [<id> | --none]           Show or set the default account
doppel whoami [path] [--offline] [--json]
                                         Show which account applies here, and why
doppel test [<id>]                       Log in to each host and sign a test message
doppel doctor [--fix]                    Check every account for problems; --fix brings doppel's files up to date
doppel export <id> [--auth|--signing] [--no-copy]
                                         Print and copy a public key, with host instructions
doppel upload <id> [--auth|--signing]    Upload keys to GitHub with gh
doppel update [--check] [--force]        Install the latest signed release (--check only looks)
doppel uninstall                         Remove doppel's changes to your Git and SSH files
doppel completion zsh|bash|fish          Print a shell completion script
doppel version                           (also -v, --version)
doppel help                              (also -h, --help)

Aliases: list (ls), remove and delete (rm), upgrade (update).

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
~/.config/doppel/.lock                        the write lock (R8.2b)
~/.config/doppel/.staging/                    working copies while a command runs (R8.2)
~/.ssh/allowed_signers                        + one marked block (R4.3)
```

**Account files are the source of truth.** `index.gitconfig` is generated entirely from them, so it can always be rebuilt. There's no hidden state file. Hand edits to account files are respected, and `git config --show-origin` explains any value.

- **Multi-machine setups and syncing:**
  - Account files (`accounts/<id>.gitconfig`) sync cleanly across machines (e.g. via dotfiles or Git).
  - The generated `index.gitconfig` (which uses `gitdir/i:` on macOS vs. `gitdir:` on Linux), the `~/.gitconfig` include block, and `~/.ssh/allowed_signers` are machine-local and should not be synced.
  - When account files arrive via sync, `doppel doctor --fix` regenerates `index.gitconfig` and updates `allowed_signers`. If forgotten, `whoami` and the browser detect the stale index and prompt the fix.
  - If syncing introduces conflicting defaults, write commands refuse to proceed and prompt resolving it with `doppel default <id>`.

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
  - One list in the code (`accounts.Managed`) names every managed key: its value, whether it's read back, and whether it's an identity setting (the ones R1.5, R6.3 and R7 look at). Adding a managed key means adding it there.
  - `core.sshCommand` combines several values, so it is always regenerated from `doppel.authKey`, and hand edits to it are overwritten.
  - doppel's own fields live under `[doppel]`. `doppel.retiredSigner` (R4.3) is read back too, and repeats.
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
| `ssh-add` | agent status; on macOS, keeping a new key's passphrase in the Keychain | no |
| `gh` ≥ 2.40 | GitHub uploads | no (falls back to `export`) |

- **Time limits:** every tool that can't be waiting for the user runs with a time limit (`proc.Command`): 15 seconds for local work (`git`, `ssh-keygen -y` and `-Y verify`, `ssh-add -l`, `ssh -G`, `ssh -V`, the clipboard tools), 60 seconds for anything that may use the network (`gh`, the batch-mode `ssh -T` login check). A tool that runs out is stopped, and the error names it. Generating a key, signing, `ssh-add --apple-use-keychain` and the interactive login check may ask for a passphrase, so they have no limit.
- **The browser draws first:** it works out each key's status (passphrase, in agent) in the background, showing "checking…" until it has it.

### 6.6 Code and conventions

- Go (the same version as sshx). Charm stack: Bubble Tea, Bubbles, Huh, Lip Gloss. Static binary, no CGO, macOS and Linux.
- Code is organized in packages under `internal/`. `main.go` stays at the repo root, so `go install github.com/vehkiya/doppel@latest` builds a `doppel` binary, as with sshx.
  ```
  main.go          calls cli.Run
  internal/
    cli/        flags, prompts, the wizards and the loop around the browser: it turns them into ops requests and prints the results
    ops/        what the commands do: account changes (typed requests in, a Change and a Result out), whoami, test, export, upload
    doctor/     doctor's checks, returned as findings
    tui/        the account browser (picks an action; cli carries it out)
    accounts/   the Account model, its managed settings (one registry), loading and validation; the folder rules and which one applies
    store/      writing accounts: validation, account files, the generated index, the global include, the write lock
    plan/       staged writes with backups and rollback (what --dry-run previews)
    atomicfile/ replacing a file atomically (plan and update)
    paths/      where files live; normalizing folders and comparing paths
    proc/       running external tools with a time limit
    git/        running git; reading and writing Git config files through it
    keys/       key references (keys.Ref); reading, generating and checking SSH keys; the login and signing checks
    hosts/      which hosts are GitHub (asked once per command), each host's steps for adding a key, remote URLs
    ui/         palette, styles and diff rendering
    version/    build version
    github/     adding keys to GitHub through gh
    update/     self-update: checking for releases, verifying and installing them
    testenv/    a sandboxed home directory and Git environment for tests
  ```
- Dependencies point one way, from `cli` down to the leaves; a test (`architecture_test.go`) checks every import against the allowed layers, so CI catches a shortcut.
- The palette, badges and Huh theme are copied from sshx. The quality checks follow sshx's `AGENTS.md`: `gofmt -s`, `go test -race`, `golangci-lint`, a tidy `go.mod`. A doppel `AGENTS.md` adds the rules from §6.2 to §6.4.

### 6.7 Testing

- Every test runs in a sandbox: a temporary `HOME`, `XDG_CONFIG_HOME` and `GIT_CONFIG_GLOBAL`, with `GIT_CONFIG_NOSYSTEM=1`.
- Fake `ssh` and `gh` executables on `PATH` record calls and return canned output. `ssh-keygen` is the real one, so generated keys, fingerprints and signatures are real and `git log --show-signature` really verifies. Every sandbox starts with a `gh` that is signed in nowhere, and with the `GH_*` and `GITHUB_*` variables cleared, so the real `gh` is never run; a test that needs a signed-in `gh` installs its own.
- Failure-injection tests cover `plan` (a failure at every write, backup and removal step, and a failed rollback), stale content, and `doctor --fix` with every kind of bad folder.
- Wizard tests run both ways: through the line-by-line accessible prompts, and through the single form with scripted keystrokes (hidden pages, choices that follow earlier answers, going back).
- Integration tests run real `git` against sandboxed repos:
  - folder precedence
  - clones into bound folders
  - worktrees
  - leak-proofing between the default and folder accounts
  - anything placed after the include block in the global config
- Table tests cover path normalization, rule ordering and matching. Generated files are compared with the expected text, written inline in the test.
- CI runs on Linux and macOS (macOS for `gitdir/i:` and its case-insensitive filesystem), and on Linux in an Ubuntu 22.04 container, whose Git 2.34.1 is the minimum doppel supports.

### 6.8 Distribution

- `go install github.com/vehkiya/doppel@latest`, plus GitHub release binaries for Linux and macOS (amd64 and arm64), adapted from sshx's workflows.
  - **Validation:** every PR is linted and tested on Linux, tested on macOS (for `gitdir/i:` and its case-insensitive filesystem), and tested against Git 2.34 (Ubuntu 22.04).
  - **Releases:** every merge to `main` that changes the version gets a release. The version comes from Conventional Commits: `feat` bumps the minor version, `!` or `BREAKING CHANGE` the major, `docs` nothing, and anything else the patch. A merge with only `docs` commits since the last release makes no release; its changes ship with the next one.
  - **Verification:** releases carry SHA-256 checksums signed with the release key, and GitHub build provenance (`gh attestation verify`).
  - **Signing:** the signing key exists only as the `DOPPEL_SIGNING_KEY` secret in the `release` environment, which only `main` can deploy to. The workflow refuses to publish unsigned, or to sign with a key that isn't in `TrustedKeys`.
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
