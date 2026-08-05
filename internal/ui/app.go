// Package ui is blip's interactive terminal explorer. It is the one part of blip
// written for a human, and everything it draws goes through bubbletea's renderer,
// which only runs once stdout is known to be a terminal.
package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

// DiscoverFunc answers "what API lives in this repo". The command layer binds it
// to detect.Discover so this package needs no config, client or socket source.
type DiscoverFunc func(context.Context) ([]detect.Target, detect.Ledger, error)

// LoadAPIFunc parses the operations of one discovered target. It takes a target
// and nothing else, so browsing cannot reach a vault even by mistake: the command
// layer binds it to read SpecPath from disk or fetch SpecURL, whichever the
// discovery rung set, with no credential source in reach.
type LoadAPIFunc func(context.Context, detect.Target) (*build.API, error)

// Options is everything Run needs from the command layer.
type Options struct {
	Stdout io.Writer

	// Stdin is left nil unless it is a terminal, so that bubbletea opens
	// /dev/tty itself rather than waiting forever on a pipe that never types.
	Stdin io.Reader

	StdoutIsTTY bool
	Theme       theme.Theme
	Discover    DiscoverFunc
	LoadAPI     LoadAPIFunc
}

// Run starts the explorer. It refuses rather than negotiating when stdout is not
// a terminal, because stdout carries only the response body.
func Run(ctx context.Context, opts Options) error {
	if !opts.StdoutIsTTY {
		return output.Usagef("ui needs a terminal: stdout is not one, so there is nowhere to draw")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	teaOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithAltScreen()}
	if opts.Stdout != nil {
		teaOpts = append(teaOpts, tea.WithOutput(opts.Stdout))
	}
	if opts.Stdin != nil {
		teaOpts = append(teaOpts, tea.WithInput(opts.Stdin))
	}

	_, err := tea.NewProgram(New(ctx, opts), teaOpts...).Run()
	return exitError(err)
}

// exitError maps bubbletea's outcome onto blip's exit-code contract. Being asked
// to stop is not a failure; every other way out is, including the ones bubbletea
// reports by wrapping their cause in ErrProgramKilled.
func exitError(err error) error {
	switch {
	case errors.Is(err, tea.ErrProgramPanic):
		return output.WithCode(err, output.ExitInternal)
	case err == nil,
		err == tea.ErrProgramKilled,
		errors.Is(err, tea.ErrInterrupted),
		errors.Is(err, context.Canceled):
		return nil
	default:
		return err
	}
}

// Model is the root of the explorer. Every state below it is updated by message,
// never by drawing, which is what makes the whole tree testable without a pty.
type Model struct {
	ctx      context.Context
	theme    theme.Theme
	discover DiscoverFunc
	loadAPI  LoadAPIFunc

	width  int
	height int

	discovering bool
	targets     []detect.Target
	ledger      detect.Ledger
	err         error

	mode         uiMode
	choiceCursor int
	chosen       detect.Target
	loadErr      error

	// stale and warnings ride in on a successful loadedMsg: stale marks a
	// spec served from the cache because the backend could not be reached,
	// and warnings is whatever the loader's Fetcher.Warnf collected instead
	// of writing to the real terminal.
	stale    bool
	warnings []string

	list   listModel
	detail detailModel
}

var _ tea.Model = Model{}

// New builds the root model without starting anything. ctx bounds the work it
// starts, so quitting does not leave discovery running.
func New(ctx context.Context, opts Options) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{
		ctx:         ctx,
		theme:       opts.Theme,
		discover:    opts.Discover,
		loadAPI:     opts.LoadAPI,
		discovering: opts.Discover != nil,
		list:        newListModel(opts.Theme),
		detail:      newDetailModel(opts.Theme),
	}
}

type discoveredMsg struct {
	targets []detect.Target
	ledger  detect.Ledger
	err     error
}

// loadedMsg is what a target's spec load resolves to, success or failure.
type loadedMsg struct {
	api *build.API
	err error
}

func (m Model) Init() tea.Cmd {
	if m.discover == nil {
		return nil
	}
	ctx, discover := m.ctx, m.discover
	return func() tea.Msg {
		targets, ledger, err := discover(ctx)
		return discoveredMsg{targets: targets, ledger: ledger, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applyLayout()
	case tea.KeyMsg:
		// ctrl+c is the only key that always quits. A plain "q" quits
		// everywhere except while a pane is reading free text, where it must
		// type a letter instead: see textEntryActive.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.String() == "q" && !m.textEntryActive() {
			return m, tea.Quit
		}
		switch m.mode {
		case modeChoosing:
			cmd := m.updateChoosing(msg)
			return m, cmd
		case modeBrowsing:
			cmd := m.updateBrowsing(msg)
			return m, cmd
		}
	case discoveredMsg:
		m.discovering = false
		m.targets, m.ledger, m.err = msg.targets, msg.ledger, msg.err
		if msg.err == nil && m.loadAPI != nil {
			if len(msg.targets) == 1 {
				m.mode = modeLoading
				m.chosen = msg.targets[0]
				return m, m.loadCmd()
			}
			if len(msg.targets) > 1 {
				m.mode = modeChoosing
				m.choiceCursor = 0
			}
		}
	case loadedMsg:
		if msg.err != nil {
			m.mode = modeFailed
			m.loadErr = msg.err
			return m, nil
		}
		m.mode = modeBrowsing
		m.stale = msg.api.Stale
		m.warnings = msg.api.Warnings
		m.list.SetAPI(msg.api)
		m.applyLayout()
		m.detail.SetOperation(m.list.Selected())
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(m.theme.Primary).Render("blip"))
	b.WriteString("\n\n")

	switch m.mode {
	case modeChoosing:
		b.WriteString(m.viewChoosing())
	case modeLoading:
		b.WriteString(m.wrapped().Render("loading the spec for " + m.chosen.Title))
	case modeFailed:
		b.WriteString(m.styled(m.theme.Error).Render(m.loadErr.Error()))
		b.WriteString(m.ledgerLines())
	case modeBrowsing:
		b.WriteString(m.viewBrowsing())
	default: // modeTargets: discovery is still running, failed, empty, or a plain listing.
		b.WriteString(m.viewTargets())
	}

	b.WriteString("\n\n")
	b.WriteString(m.styled(m.theme.Muted).Render(m.footer()))
	return b.String()
}

// viewTargets is what the screen shows before a target has been committed to:
// discovery in progress, discovery failed outright, nothing found, or (with a
// nil LoadAPI, the only way modeTargets survives past discovery) the plain
// reachability listing that existed before browsing did.
func (m Model) viewTargets() string {
	switch {
	case m.discovering:
		return "looking for an API in this repo"
	case m.err != nil:
		return m.styled(m.theme.Error).Render(m.err.Error()) + m.ledgerLines()
	case len(m.targets) == 0:
		return "no API found" + m.ledgerLines()
	default:
		var b strings.Builder
		for _, t := range m.targets {
			b.WriteString(m.wrapped().Render(fmt.Sprintf("%s  %s", t.Title, m.reachability(t))) + "\n")
		}
		return b.String()
	}
}

// reachability keeps detect's reason for an uncallable target on screen, rather
// than rendering it as a blank where the base URL would be.
func (m Model) reachability(t detect.Target) string {
	if t.Unsendable != "" {
		return lipgloss.NewStyle().Foreground(m.theme.Warning).Render(t.Unsendable)
	}
	return t.BaseURL
}

// ledgerLines gives each rung its own bullet. The ladder's own String joins them
// into one sentence, which is unreadable once it wraps.
func (m Model) ledgerLines() string {
	var b strings.Builder
	for _, a := range m.ledger.Attempts {
		b.WriteString("\n" + m.bulleted(a.Detail+": "+a.Outcome))
	}
	return b.String()
}

// bulleted indents a rung under the line it explains, and hangs the wrapped
// remainder under the text rather than under the bullet.
func (m Model) bulleted(line string) string {
	const prefix = "  · "
	muted := lipgloss.NewStyle().Foreground(m.theme.Muted)
	text := muted
	if indent := lipgloss.Width(prefix); m.width > indent {
		text = text.Width(m.width - indent)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, muted.Render(prefix), text.Render(line))
}

// wrapped folds text at the screen edge. The renderer truncates anything wider,
// and the ledger explaining a failed discovery is the longest line blip draws.
func (m Model) wrapped() lipgloss.Style {
	return wrapped(m.theme, m.width)
}

func (m Model) styled(c lipgloss.Color) lipgloss.Style {
	return styled(m.theme, m.width, c)
}

// wrapped folds text at the screen edge. The renderer truncates anything wider,
// so every state in this package that draws free text goes through this rather
// than a bare lipgloss.NewStyle().
func wrapped(_ theme.Theme, width int) lipgloss.Style {
	s := lipgloss.NewStyle()
	if width > 0 {
		s = s.Width(width)
	}
	return s
}

func styled(t theme.Theme, width int, c lipgloss.Color) lipgloss.Style {
	return wrapped(t, width).Foreground(c)
}
