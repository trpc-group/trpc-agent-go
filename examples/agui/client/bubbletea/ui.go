package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	docStyle    = lipgloss.NewStyle().Margin(1, 2)
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	bodyStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	inputStyle  = lipgloss.NewStyle().MarginTop(1)
)

const headerText = "AG-UI Demo"

type model struct {
	endpoint        string
	history         []string
	input           textinput.Model
	viewport        viewport.Model
	spinner         spinner.Model
	busy            bool
	ready           bool
	stream          *chatStream
	streamHasOutput bool
}

func initialModel(endpoint string) tea.Model {
	input := textinput.New()
	input.Placeholder = "Ask something..."
	input.Prompt = "You> "
	input.Focus()

	spin := spinner.New()
	spin.Spinner = spinner.Dot

	m := model{
		endpoint: endpoint,
		history:  []string{"Simple AG-UI Client. Press Ctrl+C to quit."},
		input:    input,
		spinner:  spin,
	}
	return m
}

func (m model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ready = true
		m.configureViewport(msg.Width, msg.Height)
		m.refreshViewport()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			trimmed := strings.TrimSpace(m.input.Value())
			if trimmed == "" || m.busy {
				return m, nil
			}
			m.input.Reset()
			m.busy = true
			if m.stream != nil {
				m.stream.Close()
			}
			m.stream = nil
			m.streamHasOutput = false
			m.history = append(m.history, fmt.Sprintf("You> %s", trimmed))
			m.refreshViewport()
			return m, tea.Batch(m.spinner.Tick, startChatCmd(trimmed, m.endpoint))
		default:
			var cmds []tea.Cmd
			var viewportCmd tea.Cmd
			m.viewport, viewportCmd = m.viewport.Update(msg)
			if viewportCmd != nil {
				cmds = append(cmds, viewportCmd)
			}
			if !m.busy {
				var inputCmd tea.Cmd
				m.input, inputCmd = m.input.Update(msg)
				if inputCmd != nil {
					cmds = append(cmds, inputCmd)
				}
			}
			if len(cmds) == 0 {
				return m, nil
			}
			return m, tea.Batch(cmds...)
		}

	case chatStreamReadyMsg:
		if msg.stream == nil {
			return m, nil
		}
		m.stream = msg.stream
		m.streamHasOutput = false
		return m, readNextEventCmd(msg.stream)

	case chatStreamEventMsg:
		if msg.stream == nil || m.stream != msg.stream {
			return m, nil
		}
		if len(msg.lines) > 0 {
			m.history = append(m.history, msg.lines...)
			m.streamHasOutput = true
			m.refreshViewport()
		}
		return m, readNextEventCmd(msg.stream)

	case chatStreamFinishedMsg:
		if msg.stream == nil || m.stream != msg.stream {
			return m, nil
		}
		msg.stream.Close()
		m.stream = nil
		m.busy = false
		if !m.streamHasOutput {
			m.history = append(m.history, "Bot> (no response)")
		}
		m.streamHasOutput = false
		m.refreshViewport()
		m.input.Focus()
		return m, nil

	case errMsg:
		if m.stream != nil {
			m.stream.Close()
			m.stream = nil
		}
		m.streamHasOutput = false
		m.history = append(m.history, fmt.Sprintf("Error: %v", msg.error))
		m.busy = false
		m.refreshViewport()
		m.input.Focus()
		return m, nil

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	if !m.busy {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return "Loading..."
	}

	header := headerStyle.Render(headerText)
	bodyFrameWidth, bodyFrameHeight := bodyStyle.GetFrameSize()
	bodyWidth := m.viewport.Width + bodyFrameWidth
	bodyHeight := m.viewport.Height + bodyFrameHeight
	body := bodyStyle.Width(bodyWidth).Height(bodyHeight).Render(m.viewport.View())

	inputView := m.input.View()
	if m.busy {
		spin := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Render(m.spinner.View())
		inputView += " " + spin
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, body, inputStyle.Render(inputView))
	return docStyle.Render(content)
}

func (m *model) refreshViewport() {
	if !m.ready {
		return
	}
	content := strings.Join(m.history, "\n")
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *model) configureViewport(width, height int) {
	hFrameDoc, vFrameDoc := docStyle.GetFrameSize()
	hFrameBody, vFrameBody := bodyStyle.GetFrameSize()
	_, vFrameInput := inputStyle.GetFrameSize()
	headerHeight := lipgloss.Height(headerStyle.Render(headerText))
	inputHeight := 1 + vFrameInput

	viewportWidth := width - hFrameDoc - hFrameBody
	if viewportWidth < 20 {
		viewportWidth = 20
	}

	viewportHeight := height - vFrameDoc - vFrameBody - headerHeight - inputHeight
	if viewportHeight < 5 {
		viewportHeight = 5
	}

	if m.viewport.Width != viewportWidth || m.viewport.Height != viewportHeight {
		m.viewport = viewport.New(viewportWidth, viewportHeight)
	} else {
		m.viewport.Width = viewportWidth
		m.viewport.Height = viewportHeight
	}
	m.input.Width = viewportWidth
}
