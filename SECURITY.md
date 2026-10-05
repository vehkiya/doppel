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
- **Safe writes:** every write is atomic (a temporary file, then a rename), keeps symlinks and the file's mode, and keeps hidden backups of the last three versions (`.<name>.doppel.bak`, then `.bak.1` and `.bak.2`). If any step fails, doppel puts back what it already wrote.
- **No copies in shared places:** changes are staged in `~/.config/doppel/.staging` (private to you), never in the shared temp directory, because a copy of your global Git config can hold secrets such as tokens. The copies are deleted when the command ends, and leftovers from a killed command are deleted by the next one.
- **Keys:** doppel never overwrites or deletes key files. When it generates a key, `ssh-keygen` asks for the passphrase; doppel never sees it.
- **Updates:** `doppel update` only installs a release whose `checksums.txt` carries a valid Ed25519 signature (`checksums.txt.sig`) from a key built into doppel, and whose archive matches that file's SHA-256 checksum. Anything else is refused. The browser's update notice checks GitHub at most every 6 hours; set `DOPPEL_NO_UPDATE_CHECK=1` to turn it off.
- **Uploads:** `doppel upload` only talks to GitHub through `gh`, using the token `gh` keeps for the account's own GitHub user. It ignores `GH_TOKEN`, `GITHUB_TOKEN` and `GH_HOST` from the environment, so they can't send keys to another account or host.

## Verifying a release

Each release's `checksums.txt` is signed with the doppel release key:

```text
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA8X0lpZPfUxaAOalCYMGbqNR3GqRayJqlm2Wnnw9m1C8=
-----END PUBLIC KEY-----
```

To verify a download by hand, save the key above as `doppel-release.pub.pem`, then:

```bash
base64 -d checksums.txt.sig > checksums.sig
openssl pkeyutl -verify -pubin -inkey doppel-release.pub.pem -rawin -in checksums.txt -sigfile checksums.sig
sha256sum --check --ignore-missing checksums.txt
gh attestation verify doppel_linux_amd64.tar.gz --repo vehkiya/doppel
```

The last command checks GitHub build provenance: that the archive was built by this repository's release workflow on `main`.

Releases before v0.5.0 have no `checksums.txt.sig`.

**Key rotation (maintainers):**
1. Add the new public key to `TrustedKeys` (`internal/update/update.go`), and ship a release that's still signed with the old key. Installed binaries only trust the keys they were built with.
2. Once users have that release, switch the `DOPPEL_SIGNING_KEY` secret in the `release` environment to the new key, and remove the old one from the list.

The release workflow refuses to sign with a key that isn't listed. The private key exists only as that secret.

## Good practice

- Treat account files like `~/.gitconfig`: Git reads them as config, so a setting in one (for example `core.sshCommand` or `core.fsmonitor`) runs commands in every repo that account covers. If you sync `~/.config/doppel/accounts` between machines, only sync from places you trust.
- Protect private keys with a passphrase, and use an agent (or a hardware key) so you type it once.
- Give each account a key of its own. `doppel doctor` flags accounts that share one on the same host, since one would log in as the other.
