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

### 2.0 Package layout
* Code lives in packages under `internal/`; `main.go` only calls `cli.Run`. See SPEC.md §6.6 for what each package holds.
* Dependencies point one way, and `TestDependenciesPointOneWay` (`architecture_test.go`) checks them in CI. When you add a package or an import across packages, update its `layers` map in the same change.
  * `cli` → `ops`, `doctor` → `store` → `accounts` → `paths`, `git`, `keys`.
  * `ops` and `doctor` also use `hosts`, which wraps `github`. `cli` also uses `update`, and runs `tui`, which only reads accounts and returns an action: it never writes files or imports `store`.
  * `plan`, `keys`, `github` and `update` are leaves apart from two shared helpers: `atomicfile` (`plan`, `update`) and `proc` (`git`, `keys`, `github`, `cli`). `ui` only uses `plan.Change`. Nothing imports `cli`.
* **`cli` only adapts and renders.** It turns flags, wizard answers and browser actions into `ops` requests, asks the questions an operation needs (through `ops.Context.Confirm` and callbacks such as `RemoveRequest.NewDefault`), and prints the results.
  * The work happens in `ops` and `doctor`, which return structured results (`ops.Change` and `ops.Result`, `ops.Identity`, `ops.Check`, `doctor.Finding`) and never print.
  * Don't put a decision in `cli` that another front-end would have to repeat.
* Export only what another package needs. The end-to-end tests in `cli` count as another package: they use `store`'s include helpers to check the global config. A package's unit tests sit next to it; end-to-end tests that run doppel and then ask real git live in `cli`, one file per topic.

### 2.1 Dependencies
* doppel is a single static binary: the Go standard library plus the Charm libraries (`bubbletea`, `bubbles`, `huh` and `lipgloss`, all v2 from `charm.land`, and the `charmbracelet/x` and `colorprofile` helpers they're built on). No CGO.
* At runtime it calls `git`, `ssh`, `ssh-keygen`, `ssh-add` and, optionally, `gh` (2.40 or newer). Nothing else.
* **Run tools through `proc.Command`** with a time limit (`proc.Local` or `proc.Network`), so a hung agent or a silent server can't freeze doppel. Only a command that may ask the user something, such as `ssh-keygen` asking for a passphrase, runs without one.
* **Don't fetch a token to answer a question:** whether a host is GitHub comes from `gh auth status`, which never prints one, and `hosts.GitHub` remembers the answer for the rest of the command (`app.github`, which the browser makes forget each time it opens).

### 2.2 Git config integrity
* **Git is the authority on its config format.** Read and write Git config through `git config --file` (`git.ReadConfigFile`, `git.ConfigFile`, and `reconcile` in `store`). The only text doppel writes itself is the generated index and the include block, which it fully controls.
* **Read the user's settings with their includes** (`git.ReadConfigFileIncludes`), skipping doppel's own files (`Env.InDoppelDir`): dotfile setups often keep `user.*` or `gpg.ssh.allowedSignersFile` in an included file. Git still parses every file; doppel only follows the `include.path` values, so a damaged index can't block the read.
* **doppel's own Git calls ignore the user's config.**
  * Calls that touch a single named file go through `git.RunAlone`, which switches the global and system config off. A broken user config can then never stop doppel from repairing its files.
  * Use `git.Run` only where the user's config is the point, such as `whoami` and `rev-parse`.
* **Validate every folder before it's written into a rule** (`paths.ValidateFolder`), including folders read back from hand-edited account files.
* **Every account file sets every managed key** (`Account.Settings`), including "reset" values such as `commit.gpgsign = false` and `core.sshCommand = ssh`. Git applies the default account first and the folder account on top, so a key one account leaves out leaks in from another.
  * The managed keys are listed once, in `accounts.Managed`: each key's value, whether it's read back from the file, and whether it's an identity setting (`IdentityKeys`, which `whoami`, `doctor` and the first-run import use). Add a managed key there and nowhere else.
* **Key references are `keys.Ref`s.** An account names a key by its private path, by a `.pub` whose private half an agent holds, or (for signing) inline as `key::<public key>`. Ask the `Ref` (`PublicPath`, `PrivatePath`, `Public`, `IsLiteral`, `SameKey`) rather than looking at `.pub` or `key::` yourself, and expand `~/` with `ref.Map(env.Expand)`.
* **Only managed keys are touched.** Settings a user adds to an account file, such as `pull.rebase`, must survive every command, including rename.
* **The include stays last.**
  * doppel appends its include block to the end of the global file Git reads last, as text: `git config --add` would put it inside an existing `[include]` section.
  * An include found in another global file is moved.
  * When removing the block, check whether other keys share its section: Git adds new `include.path` lines to the last `[include]` section, which is doppel's.
* **Folder rules go from broad to specific** (`accounts.FolderRules`), because Git lets the last match win. The index and `whoami` both use that one list (`accounts.MatchFolder` picks the rule as Git would), so they can't disagree.
* **Folders are stored by their real path**, `~/`-shortened and ending in `/` (`Env.NormalizeFolder`). Git matches repos by their real path, and the trailing `/` stops `~/projects/work` from matching `~/projects/workshop`.
* **Ask Git which account applies** (`doppel.account`). Only `whoami` applies the folder rules itself, and only where Git can't answer, because the repo doesn't exist yet: a clone target, or a new repo inside a folder that belongs to an enclosing repo.

### 2.3 Single write path
* Every file change goes through a `Plan`: stage, then `Apply`.
* `Apply` keeps hidden backups of the last three versions of each file it replaces or removes (`.<name>.doppel.bak`, `.bak.1`, `.bak.2`), and writes atomically, preserving the file's mode and any symlink, even a dangling one.
* A plan stages its working copies in `~/.config/doppel/.staging` (`Env.StagingDir`), never the shared temp directory: they can hold secrets from the global config. Don't write other temporary copies of user config; pass text to Git on stdin instead (`git.ReadConfig`).
* `Apply` takes every backup first, writes next, and removes files last, so a failure partway never leaves a folder rule pointing at a missing file. If a step fails it rolls back what it already did, so a failed command leaves every file as it was. `Apply` also fails with `plan.StaleError` when a file no longer has the content it was staged from.
* **Change accounts through `ops`.** Each operation (`ops.Add`, `Edit`, `Remove`, `Rename`, `Bind`, `Unbind`, `SetDefault`) takes a typed request and returns an `ops.Change`, and `ops.Save` writes it. In `cli`, `app.change` loads, runs the operation and saves, so flags, wizards and the browser share one implementation.
* **Stage accounts only through `store.Save`**, which validates them (`accounts.ValidateAll`) and fails when an account file changed since the command loaded it. There is no other exported way, so `doctor --fix` can't write what a command would refuse. Say which accounts you delete (`store.Options.Removed`, or a rename); nothing else is ever deleted.
* **Take the write lock** (`app.lockWrites`, from `store.Lock`) around the load, save and apply of anything that writes. A command that may prompt takes it only when `ops.Save` runs (`SaveOptions.Lock`), never while a prompt is open.
* Because commands only stage changes, `--dry-run` shows exactly what a real run would write. Never write a file outside a plan.
* doppel never overwrites or deletes key files.

### 2.3b Self-update
* Never install anything that hasn't passed both checks: a valid signature on `checksums.txt` from a key in `TrustedKeys`, and a matching SHA-256 for the archive. Don't add a way around them, not even a flag or an environment variable.
* Rotate keys as `SECURITY.md` describes. The release workflow refuses keys that aren't in `TrustedKeys`.
* Tests swap `releasesAPIURL` and `executablePath`, so they never contact GitHub or replace the real binary.

### 2.3a Interactive flows
* A wizard is a list of `step`s run as **one** Huh form (`wizardForm`), so Shift+Tab goes back to any earlier page and Esc cancels from any page.
  * **Hidden pages:** a page that only sometimes applies has a `hide` func.
  * **Answers that change other pages:** descriptions and the review follow earlier answers (`liveDescription`, `liveNote`).
  * **A select's choices never change** while the wizard runs, so don't use `OptionsFunc`. When the options change, Huh keeps the cursor where it was, so hiding the chosen option would quietly answer with whichever one took its place. A choice that doesn't fit the other answers, such as signing with an auth key there isn't, is refused by the page's check.
  * **Path inputs complete** (`completePaths`): Huh runs the suggestions func again whenever its binding changes, so it's bound to the input's own answer. Huh accepts a suggestion with Ctrl+E, and Tab both accepts and leaves the field, so the form's filter turns Tab into Ctrl+E only while a suggestion shows (`completeOnTab`). The filter can't reach the form through its model argument (Huh wraps the form), so `wizardForm` keeps it in `answers`.
  * **Checks apply going forward** (`forward`): Huh checks a field again as it loses focus and won't leave a page with an error in either direction, so without it a half-typed answer would trap the user on its page. The Save button checks every shown page once more (`checkAnswers`), and a hidden page's answer doesn't count.
  * **Note text is escaped** (`liveNote`, `escapeNote`): Huh reads `_`, `*` and `` ` `` in a note as formatting, which would swallow them from paths and names. Accessible mode prints notes as they are.
  * **Accessible mode:** Huh's accessible mode ignores hidden pages and doesn't load live text, so there `runSteps` asks each visible page in turn. Pages are built by functions, so each one sees the answers before it.
* Run every form through `app.runForm`. It applies the theme and `formKeyMap` (Esc and Ctrl+C cancel; select filtering is off so Esc means one thing), and switches to accessible mode for `$ACCESSIBLE` and for tests.
* Validate a prefilled field with `keepIfEmpty`. In accessible mode an empty answer means "keep the value", and Huh validates the typed text before falling back to it.
* Commands only ask when they have no flags and a terminal (`onlyWriteFlags`). Scripts must never get a question.
* **Every command parses its flags first**, through `app.parseCommand`, before it reads or writes anything. Shell completion gets a command's flags by calling it with `collectFlags` set, which makes `parseCommand` hand over the flag set and stop (`flagsOf`), so the completion can't drift from the real flags. `TestEveryCommandGivesItsFlags` checks it. What a positional argument or flag value is (an account, a folder, a key file) lives in `completion.go`.
* The browser only picks an action. `cli` carries it out with the same code as the matching command, so the browser and the command line can't drift apart. Its status line comes from the action's `ops.Result` (or its error), never from the wording of what was printed.

### 2.4 Design system
Keep styling consistent with sshx's palette (`internal/ui/palette.go`):
* **Brand / Accent:** Charm Purple (`#7D56F4`)
* **Headers / Selections:** Coral Pink (`#FF5F87`)
* **Prompts / Cursors / Keys:** Vibrant Cyan (`#00D7D7`)
* **Success / Badges:** Spring Green (`#5FD787`)
* **Warnings:** Amber (`#FFAF00`)
* **Errors / Destructive actions:** Red (`#FF4672`)

Print through `app.stdout` and `app.stderr`, which `ui.Writer` wraps. Lip Gloss v2 always styles text for a full-color terminal and leaves it to the writer to drop what the output can't show, so styled text written straight to `os.Stdout` would put escape codes into pipes and files. Tests that capture output wrap their buffers the same way.

### 2.5 Testing
* Tests never touch the real home directory, Git config or ssh-agent. Use `testenv.New` (wrapped by `newSandbox` in `cli` tests), which points `HOME` and `XDG_CONFIG_HOME` at a temp directory and clears every `GIT_*` and `SSH_*` variable that could leak in a developer's own setup or open a passphrase dialog.
* Make test keys with `Sandbox.Key` (real `ssh-keygen`, so signatures really verify). Never contact a real host: stand in for `ssh` with `Sandbox.FakeSSH`. Stand in for an agent with `Sandbox.FakeAgent`; the sandbox clears `SSH_AUTH_SOCK`, so otherwise none is running.
* Never call the real `gh`. `testenv.New` puts a `gh` signed in nowhere first on `PATH` and clears every `GH_*` and `GITHUB_*` variable. Upload tests install the `fakeGH` script instead, whose state (signed-in users, scopes, keys) lives in files in the sandbox. To test a missing tool, narrow `PATH` with `Sandbox.OnlyCommands`.
* `doctor` must stay read-only without `--fix`: it stages its checks in a plan and only applies the plan with `--fix`.
* Test wizards by setting `s.tty = true` and scripting `s.stdin` with `script(...)`, one line per prompt (empty for the default). The sandbox runs forms in accessible mode.
* Test the full-screen wizard's navigation with `formDriver`, which sends keys to the real form and feeds its commands back as a terminal would.
* Test the browser through its model (`New`, `Update`, `View`), not a real terminal. Every view must fit 60, 80 and 120 columns.
* Prefer end-to-end tests that run doppel and then ask real `git` what applies in a repo, over tests of internal functions.
