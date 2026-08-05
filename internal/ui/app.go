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

	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

// DiscoverFunc answers "what API lives in this repo". The command layer binds it
// to detect.Discover so this package needs no config, client or socket source.
type DiscoverFunc func(context.Context) ([]detect.Target, detect.Ledger, error)

// Options is everything Run needs from the command layer.
type Options struct {
	Stdout io.Writer

	// Stdin is left nil unless it is a terminal, so that bubbletea opens
	// /dev/tty itself rather than waiting forever on a pipe that never types.
	Stdin io.Reader

	StdoutIsTTY bool
	Theme       theme.Theme
	Discover    DiscoverFunc
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

	width  int
	height int

	discovering bool
	targets     []detect.Target
	ledger      detect.Ledger
	err         error
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
		discovering: opts.Discover != nil,
	}
}

type discoveredMsg struct {
	targets []detect.Target
	ledger  detect.Ledger
	err     error
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
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case discoveredMsg:
		m.discovering = false
		m.targets, m.ledger, m.err = msg.targets, msg.ledger, msg.err
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(m.theme.Primary).Render("blip"))
	b.WriteString("\n\n")

	switch {
	case m.discovering:
		b.WriteString("looking for an API in this repo")
	case m.err != nil:
		b.WriteString(m.styled(m.theme.Error).Render(m.err.Error()))
		b.WriteString(m.ledgerLines())
	case len(m.targets) == 0:
		b.WriteString("no API found")
		b.WriteString(m.ledgerLines())
	default:
		for _, t := range m.targets {
			b.WriteString(m.wrapped().Render(fmt.Sprintf("%s  %s", t.Title, m.reachability(t))) + "\n")
		}
	}

	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(m.theme.Muted).Render("q quit"))
	return b.String()
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
