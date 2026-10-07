// Package tui is doppel's account browser: the accounts beside the selected
// one's details, in sshx's style. It only picks an action; the cli package
// carries it out and opens the browser again.
package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/ui"
)

// ActionKind is what the user asked the browser for.
type ActionKind string

// The actions the browser hands back.
const (
	Quit       ActionKind = ""
	Add        ActionKind = "add"
	Edit       ActionKind = "edit"
	Delete     ActionKind = "delete"
	Bind       ActionKind = "bind"
	SetDefault ActionKind = "default"
	Export     ActionKind = "export"
	Upload     ActionKind = "upload"
	Test       ActionKind = "test"
	Upgrade    ActionKind = "upgrade"
)

// Action is the browser's result: what to do, to which account.
type Action struct {
	Kind ActionKind
	ID   string
}

// KeyInfo describes how an account's keys are kept, such as
// "passphrase, in agent". Working it out runs ssh-keygen and ssh-add, so
// the browser can load it in the background (Options.LoadKeyInfo).
type KeyInfo struct {
	Auth, Signing []string
}

// Options sets up the browser.
type Options struct {
	Env      *paths.Env
	Accounts []*accounts.Account
	KeyInfo  map[string]KeyInfo
	// LoadKeyInfo works out KeyInfo in the background once the browser is
	// showing, so slow tools can't hold up the first screen.
	LoadKeyInfo func() map[string]KeyInfo
	Selected    string // the account to start on
	Status      string // a message to show briefly, such as the last action's result

	// CheckUpdate looks for a newer doppel release in the background. It
	// returns the latest version and whether it's newer; nil skips the check.
	CheckUpdate func() (latest string, newer bool)

	// StaleIndex reports whether doppel's index is missing or out of date.
	StaleIndex bool
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(ui.ColorWhite).Background(ui.ColorPurple).Padding(0, 1)
	paneStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorPurple).Padding(1, 2)
	detailStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorPurple).Padding(0, 1).MarginTop(1)
	headStyle   = lipgloss.NewStyle().Bold(true).Foreground(ui.ColorCoral).MarginBottom(1)
	labelStyle  = lipgloss.NewStyle().Bold(true).Foreground(ui.ColorCyan).Width(11)
	valueStyle  = lipgloss.NewStyle().Foreground(ui.ColorLightGray)
	hintStyle   = lipgloss.NewStyle().Bold(true).Foreground(ui.ColorGray)
)

// item is one account in the list.
type item struct{ acc *accounts.Account }

func (i item) Title() string {
	if i.acc.Default {
		return i.acc.ID + " ★"
	}
	return i.acc.ID
}
func (i item) Description() string { return i.acc.Email }
func (i item) FilterValue() string { return i.acc.ID + " " + i.acc.Email }

type keyMap struct {
	edit, add, del, bind, def, export, upload, test, details, upgrade key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		edit:    key.NewBinding(key.WithKeys("enter", "e"), key.WithHelp("enter", "edit")),
		add:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
		del:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		bind:    key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "bind folder")),
		def:     key.NewBinding(key.WithKeys("*"), key.WithHelp("*", "default")),
		export:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "export key")),
		upload:  key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "upload to GitHub")),
		test:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test")),
		details: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "details")),
		// Enabled once the update check finds a newer release.
		upgrade: key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "update doppel"), key.WithDisabled()),
	}
}

// Model is the browser's Bubble Tea model.
type Model struct {
	list          list.Model
	keys          keyMap
	env           *paths.Env
	info          map[string]KeyInfo
	action        Action
	width, height int
	showDetails   bool
	confirmDelete bool
	status        string
	quitting      bool
	checkUpdate   func() (string, bool)
	newRelease    string // a newer doppel release, once the check finds one
	loadKeyInfo   func() map[string]KeyInfo
	checkingKeys  bool // loadKeyInfo hasn't answered yet
	staleIndex    bool
}

// keyInfoMsg carries the key details loaded in the background.
type keyInfoMsg map[string]KeyInfo

type clearStatusMsg struct{}

// updateMsg carries the background update check's answer.
type updateMsg struct {
	latest string
	newer  bool
}

// New builds the browser's model.
func New(opts Options) Model {
	items := make([]list.Item, len(opts.Accounts))
	selected := 0
	for i, acc := range opts.Accounts {
		items[i] = item{acc}
		if acc.ID == opts.Selected {
			selected = i
		}
	}
	l := list.New(items, list.NewDefaultDelegate(), 80, 20)
	l.Title = "doppel"
	styleList(&l, true)
	l.SetStatusBarItemName("account", "accounts")
	keys := newKeyMap()
	// The short help line has to fit 80 columns; ? shows the rest.
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{keys.edit, keys.add}
	}
	l.AdditionalFullHelpKeys = func() []key.Binding {
		return []key.Binding{keys.edit, keys.add, keys.del, keys.bind, keys.def, keys.export, keys.upload, keys.test, keys.details, keys.upgrade}
	}
	l.KeyMap.Quit.SetKeys("q", "esc")
	l.KeyMap.Quit.SetHelp("q/esc", "quit")
	l.Select(selected)

	info := opts.KeyInfo
	if info == nil {
		info = map[string]KeyInfo{}
	}
	return Model{list: l, keys: keys, env: opts.Env, info: info, width: 80, height: 20, status: opts.Status, checkUpdate: opts.CheckUpdate,
		loadKeyInfo: opts.LoadKeyInfo, checkingKeys: opts.LoadKeyInfo != nil, staleIndex: opts.StaleIndex}
}

// styleList gives the list doppel's colors, over the defaults for a dark or
// light terminal. It starts dark and changes once the terminal says
// otherwise (tea.BackgroundColorMsg).
func styleList(l *list.Model, isDark bool) {
	delegate := list.NewDefaultDelegate()
	delegate.Styles = list.NewDefaultItemStyles(isDark)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(ui.ColorCoral).BorderLeftForeground(ui.ColorPurple).Bold(true)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(ui.ColorWhite).BorderLeftForeground(ui.ColorPurple)
	l.SetDelegate(delegate)
	l.Styles = list.DefaultStyles(isDark)
	l.Styles.Title = titleStyle
}

// Action returns what the user picked; a zero Action means quit.
func (m Model) Action() Action { return m.action }

// Init asks for the terminal's background color, starts loading key
// details and the update check, and clears the starting status message
// after a moment.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	if load := m.loadKeyInfo; load != nil {
		cmds = append(cmds, func() tea.Msg { return keyInfoMsg(load()) })
	}
	if m.status != "" {
		cmds = append(cmds, tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} }))
	}
	if check := m.checkUpdate; check != nil {
		cmds = append(cmds, func() tea.Msg {
			latest, newer := check()
			return updateMsg{latest, newer}
		})
	}
	return tea.Batch(cmds...)
}

// Update handles keys and window sizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clearStatusMsg:
		m.status = ""
		return m, nil

	case tea.BackgroundColorMsg:
		styleList(&m.list, msg.IsDark())
		return m, nil

	case keyInfoMsg:
		m.info, m.checkingKeys = msg, false
		return m, nil

	case updateMsg:
		if msg.newer {
			m.newRelease = msg.latest
			m.keys.upgrade.SetEnabled(true)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		listWidth := m.width - 2
		if m.width >= 100 {
			listWidth = m.width * 45 / 100
		}
		m.list.SetSize(listWidth, max(m.height-3, 5))
		return m, nil

	case tea.KeyPressMsg:
		if m.confirmDelete {
			m.confirmDelete = false
			if msg.String() == "y" || msg.String() == "Y" {
				return m.pick(Delete)
			}
			return m, nil
		}
		// While filtering, keys belong to the filter.
		if m.list.FilterState() == list.Filtering {
			break
		}
		selected := m.selected() != nil
		switch {
		case key.Matches(msg, m.keys.upgrade):
			return m.pick(Upgrade)
		case key.Matches(msg, m.keys.add):
			return m.pick(Add)
		case !selected:
		case key.Matches(msg, m.keys.edit):
			return m.pick(Edit)
		case key.Matches(msg, m.keys.del):
			m.confirmDelete = true
			return m, nil
		case key.Matches(msg, m.keys.bind):
			return m.pick(Bind)
		case key.Matches(msg, m.keys.def):
			return m.pick(SetDefault)
		case key.Matches(msg, m.keys.export):
			return m.pick(Export)
		case key.Matches(msg, m.keys.upload):
			return m.pick(Upload)
		case key.Matches(msg, m.keys.test):
			return m.pick(Test)
		case key.Matches(msg, m.keys.details):
			m.showDetails = !m.showDetails
			return m, nil
		}
		if key.Matches(msg, m.list.KeyMap.Quit) || msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// pick ends the browser with an action on the selected account.
func (m Model) pick(kind ActionKind) (tea.Model, tea.Cmd) {
	m.action = Action{Kind: kind}
	if acc := m.selected(); acc != nil {
		m.action.ID = acc.ID
	}
	m.quitting = true
	return m, tea.Quit
}

func (m Model) selected() *accounts.Account {
	if it, ok := m.list.SelectedItem().(item); ok {
		return it.acc
	}
	return nil
}

// View draws the browser on the alternate screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render draws the list and the selected account's details: side by side on
// wide terminals, one at a time (Tab switches) on narrow ones.
func (m Model) render() string {
	if m.quitting {
		return ""
	}
	if len(m.list.Items()) == 0 {
		card := headStyle.Render("No accounts yet") + "\n" +
			valueStyle.Render("An account is a Git identity, its SSH keys, and the folders where it applies.") + "\n\n" +
			lipgloss.NewStyle().Foreground(ui.ColorCyan).Render("[a] Add your first account") + "\n" +
			ui.Dim.Render("[q] Quit")
		if m.staleIndex {
			card += "\n\n" + m.staleNotice()
		}
		if m.newRelease != "" {
			card += "\n\n" + m.updateNotice()
		}
		if m.status != "" {
			card = statusLine(m.status) + "\n\n" + card
		}
		box := paneStyle.Padding(1, 3).Width(min(64, max(m.width-4, 30))).Render(card)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}

	acc := m.selected()
	if acc == nil {
		return m.list.View()
	}
	if m.confirmDelete {
		prompt := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorWhite).Background(ui.ColorRed).Padding(0, 2).
			Render(fmt.Sprintf("Delete account %s? Its key files are kept. [y/N]", acc.ID))
		return lipgloss.JoinVertical(lipgloss.Left, m.list.View(), "\n"+prompt)
	}

	details := m.details(acc)
	if m.staleIndex {
		details = m.staleNotice() + "\n\n" + details
	}
	if m.newRelease != "" {
		details = m.updateNotice() + "\n\n" + details
	}
	if m.status != "" {
		details = statusLine(m.status) + "\n\n" + details
	}
	height := max(m.height-3, 5)
	if m.width >= 100 {
		right := paneStyle.Width(max(m.width-m.width*45/100-6, 35)).MaxHeight(height).Render(details)
		return lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), right)
	}
	if m.showDetails {
		head := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorCyan).Render("⇥ Tab to return to the list") + "\n\n"
		return detailStyle.Width(m.width - 4).MaxHeight(height).Render(head + details)
	}
	var notes []string
	if m.status != "" {
		notes = append(notes, "  "+statusLine(m.status))
	}
	if m.staleIndex {
		notes = append(notes, "  "+m.staleNotice())
	}
	if m.newRelease != "" {
		notes = append(notes, "  "+m.updateNotice())
	}
	return lipgloss.JoinVertical(lipgloss.Left, append([]string{m.list.View()}, notes...)...)
}

// staleNotice tells the user that the index is out of date.
func (m Model) staleNotice() string {
	return ui.BadgeWarn.Render("STALE") + " " + lipgloss.NewStyle().Foreground(ui.ColorAmber).Render("index out of date · run doppel doctor --fix")
}

// updateNotice tells the user a newer doppel is out.
func (m Model) updateNotice() string {
	return ui.BadgeAccent.Render("UPDATE") + " " + lipgloss.NewStyle().Foreground(ui.ColorCyan).Render("doppel "+m.newRelease+" is available · U to update")
}

// details renders one account's settings. Long values get lines of their
// own, so nothing wraps in the narrowest pane.
func (m Model) details(acc *accounts.Account) string {
	var b strings.Builder
	head := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorCoral).Render("👤 " + acc.ID)
	if acc.Default {
		head += "  " + ui.BadgeInfo.Render("DEFAULT")
	}
	b.WriteString(head + "\n\n")

	row := func(label string, lines ...string) {
		for i, line := range lines {
			if i > 0 {
				label = ""
			}
			fmt.Fprintf(&b, "%s%s\n", labelStyle.Render(label), valueStyle.Render(line))
		}
	}
	row("Name", acc.Name)
	row("Email", acc.Email)
	row("Hosts", strings.Join(acc.Hosts, ", "))
	if acc.GitHubUser != "" {
		row("GitHub", acc.GitHubUser)
	}
	switch {
	case len(acc.Folders) > 0:
		row("Folders", acc.Folders...)
	case acc.Default:
		row("Folders", "none: used for repos", "outside every folder")
	default:
		row("Folders", "none yet: press b to bind one")
	}

	info := m.info[acc.ID]
	if acc.AuthKey == "" {
		row("Auth key", "ssh's own keys")
	} else {
		row("Auth key", acc.AuthKey.Display())
		m.keyBadges(&b, info.Auth)
	}
	if acc.SigningKey == "" {
		row("Signing", "off")
	} else {
		scope := "commits and tags"
		switch {
		case acc.SignCommits && !acc.SignTags:
			scope = "commits"
		case !acc.SignCommits && acc.SignTags:
			scope = "tags"
		case !acc.SignCommits && !acc.SignTags:
			scope = "off (key kept)"
		}
		row("Signing", scope, acc.SigningKey.Display())
		if !acc.SigningKey.SameKey(acc.AuthKey) {
			m.keyBadges(&b, info.Signing) // a signing key that's the auth key shows its badges once
		}
	}
	if m.env != nil {
		row("File", m.env.Shorten(m.env.AccountPath(acc.ID)))
	}

	b.WriteString("\n" + hintStyle.Render("e edit · b bind folder · * make default\nx export · u upload · t test · d delete"))
	return b.String()
}

// keyBadges adds a key's status badges below it, or a note while they're
// still being worked out.
func (m Model) keyBadges(b *strings.Builder, words []string) {
	switch {
	case len(words) > 0:
		fmt.Fprintf(b, "%s%s\n", labelStyle.Render(""), badges(words))
	case m.checkingKeys:
		fmt.Fprintf(b, "%s%s\n", labelStyle.Render(""), hintStyle.Render("checking…"))
	}
}

// badges renders key status words as badges, colored by what they mean.
func badges(words []string) string {
	var out []string
	for _, w := range words {
		style := ui.BadgeInfo
		switch w {
		case "passphrase", "security key", "in agent":
			style = ui.BadgeOK
		case "no passphrase", "unknown":
			style = ui.BadgeWarn
		}
		out = append(out, style.Render(w))
	}
	return strings.Join(out, " ")
}

func statusLine(status string) string {
	style := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorGreen)
	if strings.HasPrefix(status, "✗") {
		style = style.Foreground(ui.ColorRed)
	}
	return style.Render(status)
}

// Run opens the browser and returns the action the user picked.
func Run(opts Options) (Action, error) {
	final, err := tea.NewProgram(New(opts)).Run()
	if err != nil {
		return Action{}, err
	}
	m, ok := final.(Model)
	if !ok {
		return Action{}, nil
	}
	return m.Action(), nil
}
