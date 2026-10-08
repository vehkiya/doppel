# IDE & Editor Integration Guide (`IDE.md`)

This guide explains how `doppel` integrates with graphical editors and IDEs like **Visual Studio Code** and **JetBrains IDEs (IntelliJ IDEA, WebStorm, PyCharm, GoLand, Rider, etc.)**, and how to configure them to prevent common multi-account friction points.

---

## 1. How Doppel Works with IDEs

`doppel` is built around **native Git configuration**:
- It writes pure Git config files (`~/.config/doppel/index.gitconfig` and account files).
- Git includes these files automatically using conditional directory matching (`includeIf "gitdir:..."`):
  ```gitconfig
  [includeIf "gitdir:~/projects/work/"]
      path = ~/.config/doppel/accounts/work.gitconfig
  ```
- Each account configures Git's native identity and protocol settings:
  - `user.name`, `user.email`, `user.signingkey`
  - `commit.gpgsign`, `tag.gpgsign`, `gpg.format = ssh`
  - `core.sshCommand = ssh -i ~/.ssh/id_work -o IdentitiesOnly=yes`
  - `credential.username` and `credential.helper`

Because IDEs like VS Code and IntelliJ IDEA execute the system `git` CLI inside your repository root (`cwd`), **Git evaluates doppel's accounts automatically**. You don't need any custom wrappers or background daemons.

However, GUI IDEs run background Git processes (such as auto-fetch and status polling) without an interactive terminal (`tty`), and bundle their own credential providers or SSH clients. Configuring them properly ensures seamless authentication and prevents credentials or keys from conflicting across accounts.

---

## 2. Visual Studio Code & Derivatives (Cursor, VSCodium)

### 2.1 Recommended Settings
In your User Settings (`settings.json`) or Workspace Settings (`.vscode/settings.json`):

```json
{
  // 1. Disable VS Code's single-account credential provider for GitHub.
  //    This allows Git to use doppel's account-specific credential.username
  //    and your system credential helper.
  "git.useBuiltinCredentialProvider": false,

  // 2. Enable commit signing UI in VS Code.
  //    doppel sets commit.gpgsign = true and gpg.format = ssh in Git config.
  "git.enableCommitSigning": true,

  // 3. Ensure Git uses standard terminal/askpass authentication.
  "git.terminalAuthentication": false
}
```

### 2.2 Why disable `git.useBuiltinCredentialProvider`?
By default, VS Code intercepts HTTPS Git requests to `github.com` and attempts to authenticate them using whichever GitHub user is signed into VS Code. When managing multiple GitHub accounts (e.g. personal and work), this causes Git to push or fetch using the wrong account.

Setting `"git.useBuiltinCredentialProvider": false` tells VS Code to delegate HTTPS authentication to native Git, respecting doppel's `credential.username` and Git credential helpers (`osxkeychain`, `manager`, `gh auth git-credential`, `libsecret`).

### 2.3 SSH Keys with Passphrases in Background Fetch
VS Code periodically runs `git fetch` in the background. If an account's SSH key is protected by a passphrase and is not currently loaded in an SSH agent, background fetches will fail or pop up askpass dialogs.
- **macOS:** Ensure keys are saved to the Apple Keychain (`ssh-add --apple-use-keychain ~/.ssh/id_key`) and `~/.ssh/config` contains `UseKeychain yes` and `AddKeysToAgent yes`. `doppel doctor` checks this.
- **Linux:** Ensure your desktop session runs an SSH agent (such as `gnome-keyring` or `ssh-agent`) with `SSH_AUTH_SOCK` exported.

---

## 3. JetBrains IDEs (IntelliJ IDEA, PyCharm, WebStorm, GoLand, Rider)

### 3.1 The "Native" SSH Setting (Crucial)
JetBrains IDEs provide two SSH executable options:
- **Native:** Uses the system OpenSSH client (`ssh`).
- **Built-in:** Uses an internal Java SSH implementation.

> [!CAUTION]
> If set to **Built-in**, IntelliJ **completely ignores `core.sshCommand`** in `.gitconfig`. As a result, isolated SSH keys configured by doppel (`-i ~/.ssh/id_work -o IdentitiesOnly=yes`) are bypassed, and Git may authenticate as the wrong user or fail.

**How to fix:**
1. Open **Settings / Preferences** (`Cmd+,` on macOS, `Ctrl+Alt+S` on Linux/Windows).
2. Navigate to **Version Control → Git**.
3. Set **SSH executable** to **Native**.

### 3.2 Commit & Tag Signing
1. Navigate to **Settings → Version Control → Git**.
2. Check **Sign commits with GPG/SSH key**.
3. Select **Use Git's config**.

This ensures IntelliJ uses doppel's `user.signingkey` and SSH signing configuration instead of hardcoding a single key in the IDE.

### 3.3 Passwords & HTTPS Credential Storage
IntelliJ's built-in Git integration uses its internal Password Safe along with Git's credential mechanism. Because doppel sets `credential.username = <user>` in the account file, IntelliJ properly keys saved tokens and passwords per account username.

---

## 4. Other Git Clients & Terminal Tools

### Lazygit
Lazygit executes the system `git` CLI directly in the repository directory. All of doppel's folder rules, SSH commands, commit signing, and credential settings work out of the box with zero configuration.

### Sublime Merge & GitKraken
- **Sublime Merge:** Respects system Git config and OpenSSH natively.
- **GitKraken:** Ensure "Use local Git installation" is selected in Preferences → Git.

---

## 5. Auditing with `doppel doctor`

You can verify your IDE setup at any time by running:

```bash
doppel doctor
```

`doppel doctor` automatically scans for local VS Code and JetBrains configurations and warns you if:
- IntelliJ IDEA is configured with the Built-in SSH client instead of Native.
- VS Code's built-in GitHub credential provider is enabled while you have multiple or HTTPS accounts configured.
- Commit signing is disabled in editor settings while active in doppel accounts.
