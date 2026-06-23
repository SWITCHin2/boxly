// Package tui is the persistent full-screen dashboard launched by bare `ongo`.
// It lists the user's boxes live and lets them create / connect / delete
// without leaving the screen. Connecting hands the terminal to `ongo ssh` via
// Bubble Tea's ExecProcess and returns to the dashboard when the shell exits.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/devtron-labs/ongo/pkg/api"
	"github.com/devtron-labs/ongo/pkg/client"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4"))
	headerStyle = lipgloss.NewStyle().Faint(true)
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00D7AF"))
	footStyle   = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	woodStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#b9824a"))

	// Minecraft landscape palette: cream logo + grass green + sky blue + soft dirt.
	goldStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffe1a8")) // logo cream
	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffe1a8"))
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8fd35a")) // grass
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9fb0a8")) // soft sky-gray
	rowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e6e8df")) // readable parchment
	selRow      = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("#3f7a2a")).Foreground(lipgloss.Color("#fff3d6"))
	footGreen   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")) // sky blue
)

// boxlyBanner is the big pixel-block BOXLY wordmark for the TUI header.
const boxlyBanner = `██████   ██████  ██   ██ ██      ██   ██
██   ██ ██    ██  ██ ██  ██       ██ ██
██████  ██    ██   ███   ██        ███
██   ██ ██    ██  ██ ██  ██         ██
██████   ██████  ██   ██ ███████    ██`

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

// bannerShades give the wordmark a top-lit, bottom-shadowed 3D extrude.
var bannerShades = []string{"#fff0c8", "#ffe7b0", "#ffe1a8", "#e3bd76", "#c4944e"}

func renderBanner() string {
	rows := strings.Split(boxlyBanner, "\n")
	for i := range rows {
		c := bannerShades[len(bannerShades)-1]
		if i < len(bannerShades) {
			c = bannerShades[i]
		}
		rows[i] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c)).Render(rows[i])
	}
	return strings.Join(rows, "\n")
}

type (
	boxesMsg   []api.VM
	tmplsMsg   []api.TemplateInfo
	createdMsg struct {
		vm  *api.VM
		err error
	}
	axeMsg  struct{}
	errMsg  struct{ err error }
	doneMsg struct{ err error } // an ExecProcess / delete finished
	tickMsg struct{}
	meMsg   struct {
		owner string
		days  int
	}
)

// mode is the current screen: list, pick (choose template), kind (disposable vs
// persistent), or build (creating, with the axe animation).
type mode int

const (
	modeList mode = iota
	modePick
	modeKind
	modeName
	modeBuild
)

type model struct {
	client *client.Client
	self   string // path to this binary, for ExecProcess subprocesses
	server string
	token  string

	boxes  []api.VM
	cursor int
	status string
	loaded bool
	spin   spinner.Model
	owner  string
	days   int // -2 unknown, -1 unlimited, >=0 days left

	mode   mode
	tmpls  []api.TemplateInfo
	pcur   int              // picker cursor
	kind   int              // 0 disposable, 1 persistent
	name   string           // optional box name being typed
	chosen api.TemplateInfo // template being launched
	axe    int              // axe animation frame
}

// Run starts the dashboard and blocks until the user quits.
func Run(c *client.Client, server, token string) error {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7AF"))
	m := model{client: c, self: self, server: server, token: token, status: "loading…", spin: sp, days: -2}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd { return tea.Batch(m.loadBoxes(), tick(), m.spin.Tick, m.fetchMe()) }

func (m model) fetchMe() tea.Cmd {
	return func() tea.Msg {
		id, err := m.client.Me(context.Background())
		if err != nil {
			return meMsg{days: -2}
		}
		return meMsg{owner: id.Owner, days: id.DaysRemaining}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modePick:
			return m.updatePick(msg)
		case modeKind:
			return m.updateKind(msg)
		case modeName:
			return m.updateName(msg)
		}
		return m, nil // build: ignore keys

	case boxesMsg:
		m.boxes = msg
		m.loaded = true
		if m.cursor >= len(m.boxes) {
			m.cursor = max(0, len(m.boxes)-1)
		}
		if m.mode == modeList {
			m.status = ""
		}
		return m, nil

	case tmplsMsg:
		m.tmpls = msg
		m.pcur = 0
		m.mode = modePick
		m.status = ""
		return m, nil

	case createdMsg:
		m.mode = modeList
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			return m, m.loadBoxes()
		}
		// Box is up — hand the terminal to the shell, then return.
		return m, m.runSub("ssh", msg.vm.ID)

	case axeMsg:
		if m.mode == modeBuild {
			m.axe++
			return m, axeTick()
		}
		return m, nil

	case errMsg:
		m.status = "error: " + msg.err.Error()
		if m.mode != modeList {
			m.mode = modeList
		}
		return m, nil

	case doneMsg:
		return m, m.loadBoxes()

	case tickMsg:
		return m, tea.Batch(m.loadBoxes(), tick())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case meMsg:
		m.owner, m.days = msg.owner, msg.days
		return m, nil
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.boxes)-1 {
			m.cursor++
		}
	case "r":
		m.status = "refreshing…"
		return m, m.loadBoxes()
	case "n":
		m.status = "loading box types…"
		return m, m.fetchTemplates()
	case "enter":
		if len(m.boxes) > 0 {
			return m, m.runSub("ssh", m.boxes[m.cursor].ID)
		}
	case "d":
		if len(m.boxes) > 0 {
			id := m.boxes[m.cursor].ID
			m.boxes = append(m.boxes[:m.cursor], m.boxes[m.cursor+1:]...)
			if m.cursor >= len(m.boxes) {
				m.cursor = max(0, len(m.boxes)-1)
			}
			m.status = "deleting " + id + "…"
			return m, m.deleteBox(id)
		}
	}
	return m, nil
}

func (m model) updatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
	case "up", "k":
		if m.pcur > 0 {
			m.pcur--
		}
	case "down", "j":
		if m.pcur < len(m.tmpls)-1 {
			m.pcur++
		}
	case "enter":
		if len(m.tmpls) > 0 {
			m.chosen = m.tmpls[m.pcur]
			m.kind = 0
			m.mode = modeKind
		}
	}
	return m, nil
}

func (m model) updateKind(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modePick
	case "up", "k", "down", "j":
		m.kind = 1 - m.kind
	case "enter":
		m.name = ""
		m.mode = modeName
	}
	return m, nil
}

func (m model) updateName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeKind
	case tea.KeyEnter:
		m.mode = modeBuild
		m.axe = 0
		return m, tea.Batch(m.createBox(m.chosen.ID, m.kind == 1, strings.TrimSpace(m.name)), axeTick())
	case tea.KeyBackspace:
		if r := []rune(m.name); len(r) > 0 {
			m.name = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.name += string(msg.Runes)
	}
	return m, nil
}

func (m model) renderHeader() string {
	who := "you"
	if m.owner != "" {
		who = m.owner
		if m.days >= 0 {
			who += fmt.Sprintf("  ·  %d days left", m.days)
		}
	}
	left := lipgloss.JoinVertical(lipgloss.Left,
		renderBanner(), "",
		promptStyle.Render("> terminal dashboard"),
		dimStyle.Render(who),
	)
	right := lipgloss.JoinVertical(lipgloss.Left,
		goldStyle.Render("LIVE POOLS"),
		promptStyle.Render(m.server+"/pools"),
		dimStyle.Render("watch warm pools & demand live"), "",
		dimStyle.Render("server  "+m.server),
		dimStyle.Render(fmt.Sprintf("%s live · %d boxes", m.spin.View(), len(m.boxes))),
	)
	inner := lipgloss.JoinHorizontal(lipgloss.Top, left, dimStyle.Render("   │   "), right)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#8a6135")).Padding(0, 2).Render(inner)
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.renderHeader() + "\n\n")

	switch m.mode {
	case modePick:
		b.WriteString(m.viewPick())
	case modeKind:
		b.WriteString(m.viewKind())
	case modeName:
		b.WriteString(m.viewName())
	case modeBuild:
		b.WriteString(m.viewBuild())
	default:
		b.WriteString(m.viewList())
	}
	return b.String()
}

func (m model) viewName() string {
	var b strings.Builder
	b.WriteString(goldStyle.Render("  Name your "+m.chosen.Title+" box") + dimStyle.Render("  (optional — press ↵ to skip)") + "\n\n")
	b.WriteString("  " + promptStyle.Render("> ") + rowStyle.Render(m.name) + selRow.Render(" ") + "\n")
	b.WriteString("\n" + footGreen.Render("» type a name · [↵] launch · [esc] back"))
	return b.String()
}

func (m model) viewList() string {
	var b strings.Builder
	const width = 56
	if !m.loaded {
		b.WriteString(dimStyle.Render("  loading…") + "\n")
	} else if len(m.boxes) == 0 {
		b.WriteString(dimStyle.Render("  no boxes yet — press [n] to launch one") + "\n")
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %-12s %-10s %-12s %-10s %s", "NAME", "ID", "TYPE", "STATUS", "AGE")) + "\n")
		for i, v := range m.boxes {
			age := "-"
			if !v.CreatedAt.IsZero() {
				age = time.Since(v.CreatedAt).Round(time.Second).String()
			}
			line := fmt.Sprintf("%-12s %-10s %-12s %-10s %s", trunc(dash(v.Name), 12), v.ID, trunc(dash(v.Template), 12), v.Status, age)
			if i == m.cursor {
				b.WriteString(selRow.Render(fmt.Sprintf("▸ %-*s", width, line)) + "\n")
			} else {
				b.WriteString(rowStyle.Render("  "+line) + "\n")
			}
		}
	}
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString("  " + errOrStatus(m.status) + "\n")
	}
	b.WriteString(footGreen.Render("» [n] new · [↵] open · [d] delete · [r] refresh · [q] quit"))
	return b.String()
}

func (m model) viewPick() string {
	var b strings.Builder
	b.WriteString(goldStyle.Render("  What do you want to launch?") + "\n\n")
	for i, t := range m.tmpls {
		label := fmt.Sprintf("%-22s %s", t.Title, dimStyle.Render("· "+t.Desc))
		if i == m.pcur {
			b.WriteString(selRow.Render("  ▸ "+fmt.Sprintf("%-22s", t.Title)) + " " + dimStyle.Render("· "+t.Desc) + "\n")
		} else {
			b.WriteString(rowStyle.Render("    "+label) + "\n")
		}
	}
	b.WriteString("\n" + footGreen.Render("» ↑/↓ move · [↵] select · [esc] back"))
	return b.String()
}

func (m model) viewKind() string {
	opts := []struct{ t, d string }{
		{"Disposable", "auto-expires when you're done (recommended)"},
		{"Persistent", "remembers your files between sessions"},
	}
	var b strings.Builder
	b.WriteString(goldStyle.Render("  Launching ") + promptStyle.Render(m.chosen.Title) + goldStyle.Render(" — keep it after you're done?") + "\n\n")
	for i, o := range opts {
		if i == m.kind {
			b.WriteString(selRow.Render("  ▸ "+fmt.Sprintf("%-12s", o.t)) + " " + dimStyle.Render("· "+o.d) + "\n")
		} else {
			b.WriteString(rowStyle.Render("    "+fmt.Sprintf("%-12s", o.t)) + " " + dimStyle.Render("· "+o.d) + "\n")
		}
	}
	b.WriteString("\n" + footGreen.Render("» ↑/↓ choose · [↵] launch · [esc] back"))
	return b.String()
}

func (m model) viewBuild() string {
	steps := []string{"chopping out your box…", "pulling the tools…", "stacking blocks…", "almost there…"}
	frame := axeFrames[m.axe%len(axeFrames)]
	body := lipgloss.JoinVertical(lipgloss.Left,
		"",
		goldStyle.Render(frame),
		"",
		promptStyle.Render("  building "+m.chosen.Title)+dimStyle.Render("  "+steps[(m.axe/4)%len(steps)]),
	)
	return body
}

// axeFrames is a tiny Minecraft-style axe chopping a wooden block; the block
// chips away (█ → ▓ → ░) as it loops.
var axeFrames = []string{
	"      __/\n     /=='\n    //\n  ████████",
	"     __/\n    /=='\n   //\n  ███████▓",
	"    __/\n   /=='\n  //\n  ██████▓░   *",
	"   __/\n  /=='\n //\n  █████▓░░   *",
}

// --- commands ---

func (m model) loadBoxes() tea.Cmd {
	return func() tea.Msg {
		vms, err := m.client.List(context.Background())
		if err != nil {
			return errMsg{err}
		}
		return boxesMsg(vms)
	}
}

func (m model) deleteBox(id string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.Delete(context.Background(), id); err != nil {
			return errMsg{err}
		}
		return doneMsg{}
	}
}

func (m model) fetchTemplates() tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.Templates(context.Background())
		if err != nil {
			return errMsg{err}
		}
		return tmplsMsg(ts)
	}
}

// createBox creates the box and waits until it is Running, so the follow-up
// shell connect doesn't race a still-starting box.
func (m model) createBox(templateID string, persistent bool, name string) tea.Cmd {
	return func() tea.Msg {
		typ := api.TypeSandbox
		if persistent {
			typ = api.TypePersistent
		}
		vm, err := m.client.Create(context.Background(), api.CreateRequest{Template: templateID, Type: typ, Name: name})
		if err != nil {
			return createdMsg{err: err}
		}
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) {
			if g, e := m.client.Get(context.Background(), vm.ID); e == nil && g.Status == "Running" {
				return createdMsg{vm: g}
			}
			time.Sleep(700 * time.Millisecond)
		}
		return createdMsg{vm: vm}
	}
}

func axeTick() tea.Cmd {
	return tea.Tick(130*time.Millisecond, func(time.Time) tea.Msg { return axeMsg{} })
}

// runSub suspends the TUI, runs `ongo <args>` attached to the terminal (so the
// raw-mode shell / picker works), then resumes and refreshes.
func (m model) runSub(args ...string) tea.Cmd {
	full := append(args, "--server", m.server, "--token", m.token)
	c := exec.Command(m.self, full...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return doneMsg{err} })
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func errOrStatus(s string) string {
	if strings.HasPrefix(s, "error:") {
		return errStyle.Render(s)
	}
	return footStyle.Render(s)
}
