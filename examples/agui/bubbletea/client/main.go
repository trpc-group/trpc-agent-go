package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sirupsen/logrus"
)

var (
	docStyle    = lipgloss.NewStyle().Margin(1, 2)
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	bodyStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	inputStyle  = lipgloss.NewStyle().MarginTop(1)
)

const headerText = "AG-UI Demo"

func main() {
	endpoint := flag.String("endpoint", "http://localhost:8080/agui/run", "AG-UI SSE endpoint")
	flag.Parse()

	if _, err := tea.NewProgram(initialModel(*endpoint), tea.WithAltScreen()).Run(); err != nil {
		log.Fatalf("bubbletea program failed: %v", err)
	}
}

type model struct {
	endpoint string
	history  []string
	input    textinput.Model
	viewport viewport.Model
	spinner  spinner.Model
	busy     bool
	ready    bool
}

func initialModel(endpoint string) model {
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
			m.history = append(m.history, fmt.Sprintf("You> %s", trimmed))
			m.refreshViewport()
			return m, tea.Batch(m.spinner.Tick, startChatCmd(trimmed, m.endpoint))
		default:
			if !m.busy {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			return m, nil
		}

	case chatResultMsg:
		m.history = append(m.history, msg.lines...)
		m.busy = false
		m.refreshViewport()
		m.input.Focus()
		return m, nil

	case errMsg:
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

type chatResultMsg struct{ lines []string }
type errMsg struct{ error }

func startChatCmd(prompt, endpoint string) tea.Cmd {
	return func() tea.Msg {
		lines, err := fetchResponse(prompt, endpoint)
		if err != nil {
			return errMsg{err}
		}
		return chatResultMsg{lines: lines}
	}
}

func fetchResponse(prompt, endpoint string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := sse.NewClient(sse.Config{
		Endpoint:       endpoint,
		ConnectTimeout: 30 * time.Second,
		ReadTimeout:    5 * time.Minute,
		BufferSize:     100,
		Logger:         logger,
	})
	defer client.Close()

	payload := map[string]any{
		"threadId": "demo-thread",
		"runId":    fmt.Sprintf("run-%d", time.Now().UnixNano()),
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	}

	frames, errCh, err := client.Stream(sse.StreamOptions{Context: ctx, Payload: payload})
	if err != nil {
		return nil, fmt.Errorf("failed to start SSE stream: %w", err)
	}

	var collected []events.Event
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return renderEvents(collected), nil
			}
			evt, err := events.EventFromJSON(frame.Data)
			if err != nil {
				return nil, fmt.Errorf("parse event: %w", err)
			}
			collected = append(collected, evt)
		case err, ok := <-errCh:
			if ok && err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func renderEvents(evts []events.Event) []string {
	var output []string
	for _, evt := range evts {
		output = append(output, formatEvent(evt)...)
	}
	if len(output) == 0 {
		output = append(output, "Bot> (no response)")
	}
	return output
}

func formatEvent(evt events.Event) []string {
	label := fmt.Sprintf("[%s]", evt.Type())

	switch e := evt.(type) {
	case *events.RunStartedEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.RunFinishedEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.RunErrorEvent:
		return []string{fmt.Sprintf("Agent> %s: %s", label, e.Message)}
	case *events.TextMessageStartEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.TextMessageContentEvent:
		if strings.TrimSpace(e.Delta) == "" {
			return nil
		}
		return []string{fmt.Sprintf("Agent> %s %s", label, e.Delta)}
	case *events.TextMessageEndEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.ToolCallStartEvent:
		return []string{fmt.Sprintf("Agent> %s tool call '%s' started, id: %s", label, e.ToolCallName, e.ToolCallID)}
	case *events.ToolCallArgsEvent:
		return []string{fmt.Sprintf("Agent> %s tool args: %s", label, e.Delta)}
	case *events.ToolCallEndEvent:
		return []string{fmt.Sprintf("Agent> %s tool call completed, id: %s", label, e.ToolCallID)}
	case *events.ToolCallResultEvent:
		return []string{fmt.Sprintf("Agent> %s tool result: %s", label, e.Content)}
	default:
		return nil
	}
}
