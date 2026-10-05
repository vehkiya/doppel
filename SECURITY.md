# Security Policy

## Supported Versions

I support the latest release with security updates. Please stay up to date.

| Version | Supported          |
| ------- | ------------------ |
| Latest  | :white_check_mark: |
| Older   | :x:                |

## Reporting a Vulnerability

If you find a security vulnerability in `doppel`, please report it privately:

1. **Don't open a public issue.** That could expose the problem before a fix is available.
2. Use **GitHub's private vulnerability reporting**: open the repository's **Security** tab, choose **Advisories**, then **Report a vulnerability**.
3. Describe the issue, how to reproduce it, its impact, and any mitigation you know of.

I aim to acknowledge reports within 48 hours, and will keep you posted until a fix is released.

## How doppel handles your files

- **Few changes outside its folder:** doppel changes only one block in your global Git config (its include) and one marked block in your `allowed_signers` file. Everything else lives in `~/.config/doppel`.
- **Safe writes:** every write is atomic (a temporary file, then a rename), keeps symlinks and the file's mode, and keeps a hidden `.<name>.doppel.bak` backup of the previous version.
- **Keys:** doppel never overwrites or deletes key files. When it generates a key, `ssh-keygen` asks for the passphrase; doppel never sees it.
- **Uploads:** `doppel upload` only talks to GitHub through `gh`, using the token `gh` keeps for the account's own GitHub user. It ignores `GH_TOKEN`, `GITHUB_TOKEN` and `GH_HOST` from the environment, so they can't send keys to another account or host.

## Verifying a release

Each release has SHA-256 checksums and GitHub build provenance:

```bash
sha256sum --check --ignore-missing checksums.txt
gh attestation verify doppel_linux_amd64.tar.gz --repo vehkiya/doppel
```

doppel doesn't update itself yet. Signed checksums, like sshx's, will come with self-update, since that's what will need to verify them.

## Good practice

- Protect private keys with a passphrase, and use an agent (or a hardware key) so you type it once.
- Give each account a key of its own. `doppel doctor` flags accounts that share one on the same host, since one would log in as the other.
