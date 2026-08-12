package tui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The shell deliberately owns one set of visual tokens. Components use these
// styles directly, which keeps the interface legible when a terminal removes
// color and makes a theme change a one-file edit.
var (
	// The terminal owns the background. These colors are deliberately limited
	// to text, borders, and state accents so the shell can sit naturally inside
	// any terminal theme.
	colorBorder  = lipgloss.Color("#A957B5")
	colorText    = lipgloss.Color("#F5EFFA")
	colorMuted   = lipgloss.Color("#A99BB5")
	colorSubtle  = lipgloss.Color("#75677F")
	colorPurple  = lipgloss.Color("#C084FC")
	colorPink    = lipgloss.Color("#F0A0D0")
	colorMagenta = lipgloss.Color("#E879F9")
	colorSuccess = lipgloss.Color("#86EFAC")
	colorWarning = lipgloss.Color("#FDE68A")
	colorError   = lipgloss.Color("#FDA4AF")

	panelStyle    = lipgloss.NewStyle().Foreground(colorText).BorderForeground(colorBorder)
	focusedPanel  = panelStyle.Copy().BorderForeground(colorPink)
	headerStyle   = lipgloss.NewStyle().Foreground(colorText)
	sectionTitle  = lipgloss.NewStyle().Foreground(colorPink).Bold(true)
	lineStyle     = lipgloss.NewStyle().Foreground(colorBorder)
	mutedStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	subtleStyle   = lipgloss.NewStyle().Foreground(colorSubtle)
	keyHintStyle  = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	userMessage   = lipgloss.NewStyle().Foreground(colorPink).Bold(true)
	agentMessage  = lipgloss.NewStyle().Foreground(colorMagenta).Bold(true)
	toolRunning   = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	toolSuccess   = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	toolFailure   = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	approvalStyle = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	composerStyle = lipgloss.NewStyle().Foreground(colorText)
)

const (
	appName       = "PRAGMA"
	appVersion    = "1.0.0"
	defaultWidth  = 80
	defaultHeight = 24

	keyTogglePlan     = "alt+p"
	keyCommandPalette = "alt+k"
	keyInspectorPlan  = "alt+shift+p"
	keyInspectorFiles = "alt+f"
	keyInspectorTools = "alt+t"
	keyInspectorGit   = "alt+g"
	keyHelp           = "f1"
	keyHelpAlt        = "alt+h"
	keyQuit           = "alt+c"
	keyStop           = "alt+s"
	keyExpand         = "alt+o"
	keyFiles          = "alt+d"
	keyRetry          = "alt+r"
	keyApprove        = "alt+y"
	keyApproveSession = "alt+a"
	keyReject         = "alt+n"
	keyRejectReason   = "alt+r"
	keyEdit           = "alt+e"
	maxScroll         = 1 << 30
)

func (m *TUIModel) resize() {
	if m.width <= 0 {
		return
	}
	m.input.Width = max(8, m.width-8)
}

type layoutMode int

const (
	layoutNarrow layoutMode = iota
	layoutMedium
	layoutWide
)

func (m *TUIModel) layoutMode() layoutMode {
	switch {
	case m.width >= 120:
		return layoutWide
	case m.width >= 80:
		return layoutMedium
	default:
		return layoutNarrow
	}
}

// Panels use a fixed outer size. Keeping the border and padding math in one
// place prevents the columns from drifting apart as the terminal resizes.
func panelContentWidth(width int) int {
	return max(1, width-6) // two border cells + two cells of padding per side
}

func panelContentHeight(height int) int {
	return max(1, height-4) // two border rows + one row of padding per side
}

func cardContentWidth(width int) int {
	return max(8, width-6)
}

func renderBoundedCard(lines []string, width, maxHeight int, border color.Color) string {
	contentHeight := max(1, maxHeight-4) // border and one row of padding on each side
	lines = windowLines(lines, contentHeight, 0)
	style := lipgloss.NewStyle().Foreground(colorText).BorderForeground(border).BorderStyle(lipgloss.NormalBorder()).Width(width).Padding(1, 2)
	return style.Render(strings.Join(lines, "\n"))
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = truncateCells(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func inspectorTabLines(active InspectorTab, width int) []string {
	labels := []string{"[Alt+P] PLAN", "[Alt+F] FILES", "[Alt+T] TOOLS", "[Alt+G] GIT"}
	tabs := []InspectorTab{InspectorPlan, InspectorFiles, InspectorTools, InspectorGit}
	for i, tab := range tabs {
		if active == tab {
			labels[i] = keyHintStyle.Render(labels[i])
		} else {
			labels[i] = subtleStyle.Render(labels[i])
		}
	}
	if width >= 20 {
		return []string{
			labels[0] + "  " + labels[1],
			labels[2] + "  " + labels[3],
		}
	}
	return labels
}

func (m *TUIModel) View() tea.View {
	var view tea.View
	if m.width <= 0 || m.height <= 0 {
		view.SetContent("")
		return view
	}
	if m.state == StateOnboarding {
		view.SetContent(m.viewOnboarding())
	} else {
		view.SetContent(m.viewChat())
	}
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	// Leave BackgroundColor unset so the terminal's own background shows
	// through. In particular, do not replace a user's light/dark theme here.
	view.BackgroundColor = nil
	view.WindowTitle = "Pragma"
	return view
}

func (m *TUIModel) viewChat() string {
	banner := m.renderBanner()
	header := m.renderHeader()
	footer := m.renderFooter()
	composer := m.renderComposer()
	bottom := composer
	fixedHeight := max(1, m.height-lipgloss.Height(banner)-lipgloss.Height(header)-lipgloss.Height(composer)-lipgloss.Height(footer))
	// Panels have two border rows and one padding row on each side, so leave
	// enough room for their five-row minimum while a modal is open.
	cardHeight := max(1, fixedHeight-5)
	if m.confirming {
		bottom = lipgloss.JoinVertical(lipgloss.Left, m.renderApprovalCard(cardHeight), composer)
	}
	if m.planApproval {
		bottom = lipgloss.JoinVertical(lipgloss.Left, m.renderPlanApprovalCard(cardHeight), composer)
	}
	if m.menu != nil {
		bottom = lipgloss.JoinVertical(lipgloss.Left, m.renderMenuCard(cardHeight), bottom)
	}
	mainHeight := max(5, m.height-lipgloss.Height(banner)-lipgloss.Height(header)-lipgloss.Height(bottom)-lipgloss.Height(footer))
	main := m.renderMain(mainHeight)
	sections := []string{header, main, bottom, footer}
	if banner != "" {
		sections = append([]string{banner}, sections...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m *TUIModel) renderBanner() string {
	if len(m.events) > 0 || m.confirming || m.planApproval || m.menu != nil {
		return ""
	}
	content := bannerForLayout(m.layoutMode())

	lines := strings.Split(strings.Trim(content, "\n"), "\n")
	artWidth := 0
	for _, line := range lines {
		artWidth = max(artWidth, lipgloss.Width(line))
	}
	leftPadding := max(0, (m.width-artWidth)/2)
	for i, line := range lines {
		available := max(1, m.width-leftPadding)
		lines[i] = strings.Repeat(" ", leftPadding) + truncateCells(line, available)
	}
	return agentMessage.Render(strings.Join(lines, "\n"))
}

func (m *TUIModel) renderHeader() string {
	width := max(20, m.width)
	path := abbreviatePath(m.projectPath)
	branch := m.branch
	if branch == "" {
		branch = "no branch"
	}
	provider := "offline"
	if m.agent != nil && m.agent.CurrentModel != nil {
		provider = m.agent.CurrentModel.Name
	}
	contextText := "Context: —"
	if m.contextLimit > 0 {
		contextText = fmt.Sprintf("Context: %d%%", min(100, m.contextUsed*100/m.contextLimit))
	}
	statusSymbol, statusText := "○", "Ready"
	switch m.runState {
	case RunWorking:
		statusSymbol, statusText = "◉", "Working"
	case RunApproval:
		statusSymbol, statusText = "!", "Approval"
	case RunError:
		statusSymbol, statusText = "✕", "Error"
	}
	fileText := fmt.Sprintf("Changes: %d files", len(m.changedFiles))
	if m.planMode {
		modeLabel := "PLAN"
		if m.planRunning {
			modeLabel = "PLAN (executing)"
		}
		statusText = modeLabel + " · " + statusText
	}
	left := fmt.Sprintf("  %s %s", appName, appVersion)
	right := fmt.Sprintf("%s  ·  %s", branch, path)
	top := fitLine(left+strings.Repeat(" ", max(2, width-lipgloss.Width(left)-lipgloss.Width(right)))+right, width)
	status := fitLine(fmt.Sprintf("  %s %s    %s    %s    %s", statusSymbol, statusText, provider, contextText, fileText), width)
	rule := strings.Repeat("─", width)
	return headerStyle.Bold(true).Render(top) + "\n" + mutedStyle.Render(status) + "\n" + lineStyle.Render(rule)
}

func (m *TUIModel) renderMain(height int) string {
	width := max(20, m.width)
	if m.layoutMode() == layoutNarrow && m.showNarrow {
		return m.renderPanel("INSPECTOR · "+strings.ToUpper(string(m.inspectorTab)), m.renderInspectorLines(panelContentWidth(width)), width, height, m.focus == FocusInspector)
	}
	switch m.layoutMode() {
	case layoutWide:
		planWidth, inspectorWidth := 27, 32
		conversationWidth := width - planWidth - inspectorWidth - 2 // one breathing cell between panels
		if conversationWidth < 40 {
			return m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(width), height), width, height, m.focus == FocusTimeline)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderPanel("RUN / PLAN", m.renderPlanLines(), planWidth, height, m.focus == FocusPlan),
			" ",
			m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(conversationWidth), height), conversationWidth, height, m.focus == FocusTimeline),
			" ",
			m.renderPanel("INSPECTOR", m.renderInspectorLines(panelContentWidth(inspectorWidth)), inspectorWidth, height, m.focus == FocusInspector),
		)
	case layoutMedium:
		inspectorWidth := 32
		conversationWidth := width - inspectorWidth - 1
		if conversationWidth < 42 {
			return m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(width), height), width, height, m.focus == FocusTimeline)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(conversationWidth), height), conversationWidth, height, m.focus == FocusTimeline),
			" ",
			m.renderPanel("INSPECTOR · "+strings.ToUpper(string(m.inspectorTab)), m.renderInspectorLines(panelContentWidth(inspectorWidth)), inspectorWidth, height, m.focus == FocusInspector),
		)
	default:
		return m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(width), height), width, height, m.focus == FocusTimeline)
	}
}

func (m *TUIModel) renderPanel(title string, lines []string, width, height int, focused bool) string {
	width = max(10, width)
	height = max(5, height)
	innerWidth := panelContentWidth(width)
	innerHeight := panelContentHeight(height)
	content := []string{sectionTitle.Render(title)}
	if strings.Contains(strings.ToLower(title), "plan") && (m.focus == FocusPlan || (m.focus == FocusInspector && m.inspectorTab == InspectorPlan)) {
		lines = windowLines(lines, max(1, innerHeight-1), m.planScroll)
	}
	for _, line := range lines {
		// A logical item may contain newlines (for example, the plan hint).
		// Expand those before applying the fixed panel height so joined panels
		// always have identical physical rows.
		content = append(content, strings.Split(line, "\n")...)
	}
	if len(content) > innerHeight {
		content = content[:innerHeight]
	}
	for len(content) < innerHeight {
		content = append(content, "")
	}
	for i := range content {
		content[i] = truncateCells(content[i], innerWidth)
	}
	style := panelStyle
	if focused {
		style = focusedPanel
	}
	return style.Width(width).BorderStyle(lipgloss.NormalBorder()).Padding(1, 2).Render(strings.Join(content, "\n"))
}

func (m *TUIModel) renderPlanLines() []string {
	lines := []string{}
	for i, step := range m.plan {
		symbol := "○"
		style := subtleStyle
		switch step.Status {
		case StatusDone:
			symbol, style = "✓", toolSuccess
		case StatusRunning:
			symbol, style = "◉", keyHintStyle
		}
		prefix := "  "
		if i == m.planCursor {
			prefix = "› "
		}
		lines = append(lines, style.Render(prefix+symbol+" "+step.Title))
		if i < len(m.plan)-1 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, "", subtleStyle.Render("The active step stays"), subtleStyle.Render("visible while work runs."))
	return lines
}

func (m *TUIModel) renderTimelineLines(width, height int) []string {
	if len(m.events) == 0 {
		return []string{"", agentMessage.Render("◆ PRAGMA"), mutedStyle.Render("Ready when you are."), "", subtleStyle.Render("Type a prompt below to start a run.")}
	}
	var lines []string
	for index, event := range m.events {
		marker, style := eventMarker(event)
		title := fmt.Sprintf("%s %s", marker, event.Title)
		if index == m.conversation {
			title = "› " + title
		}
		lines = append(lines, style.Render(title))
		body := event.Body
		if event.Type == EventTool && event.Status == StatusDone && !event.Expanded && body != "" {
			body = "output hidden · press Space to expand"
		}
		if body != "" {
			wrapped := lipgloss.Wrap(body, max(8, width-2), " ")
			for _, line := range strings.Split(wrapped, "\n") {
				lines = append(lines, "  "+line)
			}
		}
		lines = append(lines, "")
	}
	visible := max(1, height-4)
	if len(lines) > visible {
		maxOffset := len(lines) - visible
		m.timelineMaxOffset = maxOffset
		start := maxOffset
		if !m.followTail {
			start = min(maxOffset, max(0, m.timelineScroll))
		}
		lines = lines[start:]
	} else {
		m.timelineMaxOffset = 0
	}
	return lines
}

func eventMarker(event TimelineEvent) (string, lipgloss.Style) {
	switch event.Type {
	case EventUser:
		return "▶", userMessage
	case EventAgent:
		return "◆", agentMessage
	case EventTool, EventToolOutput:
		switch event.Status {
		case StatusRunning:
			return "◈ RUNNING", toolRunning
		case StatusFailed:
			return "◈ FAILED", toolFailure
		default:
			return "◈ DONE", toolSuccess
		}
	case EventApproval:
		return "! APPROVAL", approvalStyle
	case EventError:
		return "✕ FAILED", toolFailure
	case EventPlan:
		return "▸ PLAN", keyHintStyle
	case EventDiff, EventFile:
		return "▣ FILE", userMessage
	default:
		return "·", mutedStyle
	}
}

func (m *TUIModel) renderInspectorLines(width int) []string {
	lines := append(inspectorTabLines(m.inspectorTab, width), "")
	switch m.inspectorTab {
	case InspectorPlan:
		lines = append(lines, m.renderPlanLines()...)
	case InspectorFiles:
		if len(m.changedFiles) == 0 {
			lines = append(lines, mutedStyle.Render("No changed files."))
		} else {
			for _, file := range m.changedFiles {
				stats := fmt.Sprintf("+%d -%d", file.Added, file.Removed)
				prefix := fmt.Sprintf("%-2s ", truncateCells(file.Status, 2))
				pathWidth := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(stats)-1)
				path := fitLine(truncateCells(file.Path, pathWidth), pathWidth)
				lines = append(lines, prefix+path+" "+stats)
			}
		}
	case InspectorTools:
		found := false
		for _, event := range m.events {
			if event.Type == EventTool {
				found = true
				marker, _ := eventMarker(event)
				lines = append(lines, fmt.Sprintf("%s %s", marker, truncateCells(event.Title, max(8, width-12))))
			}
		}
		if !found {
			lines = append(lines, mutedStyle.Render("No tools have run."))
		}
	case InspectorGit:
		lines = append(lines, fmt.Sprintf("Branch: %s", fallback(m.branch, "not detected")), fmt.Sprintf("Path: %s", abbreviatePath(m.projectPath)), "")
		if len(m.changedFiles) > 0 {
			lines = append(lines, fmt.Sprintf("%d changed files", len(m.changedFiles)))
		} else {
			lines = append(lines, mutedStyle.Render("Working tree clean."))
		}
	}
	return lines
}

func (m *TUIModel) renderApprovalCard(maxHeight int) string {
	width := max(20, m.width)
	lines := []string{
		approvalStyle.Render("! APPROVAL REQUIRED"),
		fmt.Sprintf("Run: %s", truncateCells(m.confirmInfo.Command, width-8)),
		fmt.Sprintf("Directory: %s", abbreviatePath(fallback(m.confirmInfo.Directory, m.projectPath))),
		fmt.Sprintf("Risk: %s", fallback(m.confirmInfo.Risk, "review required")),
	}
	if len(m.confirmInfo.Files) > 0 {
		lines = append(lines, "Files: "+strings.Join(m.confirmInfo.Files, ", "))
	}
	if m.confirmInfo.Network {
		lines = append(lines, "Network: access requested")
	}
	if m.confirmInfo.Reason != "" {
		lines = append(lines, "Reason: "+m.confirmInfo.Reason)
	}
	if m.confirmAwaitReason {
		lines = append(lines, "", keyHintStyle.Render("Type a rejection reason, Enter to submit, Esc to cancel"))
	} else {
		lines = append(lines, "", keyHintStyle.Render("[Alt+Y] approve once  [Alt+A] approve session  [Alt+E] edit  [Alt+N] reject  [Alt+R] reject with reason"))
	}
	innerWidth := cardContentWidth(width)
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(lipgloss.Wrap(line, innerWidth, " "), "\n")...)
	}
	return renderBoundedCard(wrapped, width, maxHeight, colorWarning)
}

func (m *TUIModel) renderPlanApprovalCard(maxHeight int) string {
	width := max(20, m.width)
	innerWidth := cardContentWidth(width)
	contentHeight := max(1, maxHeight-4)
	lines := []string{approvalStyle.Render("PLAN APPROVAL"), ""}
	var planLines []string
	if m.activePlan != nil {
		planLines = wrapPhysicalLines(strings.Split(m.activePlan.Format(), "\n"), innerWidth)
	}
	helpLines := wrapPhysicalLines([]string{keyHintStyle.Render("[Alt+Y] approve  [Alt+E] edit in $EDITOR  [Alt+N] reject  ·  ↑/↓ scroll")}, innerWidth)
	bodyHeight := max(1, contentHeight-len(lines)-len(helpLines)-1)
	planLines = windowLines(planLines, bodyHeight, m.planApprovalScroll)
	lines = append(lines, planLines...)
	lines = append(lines, "")
	lines = append(lines, helpLines...)
	return renderBoundedCard(lines, width, maxHeight, colorWarning)
}

func (m *TUIModel) renderMenuCard(maxHeight int) string {
	width := max(20, m.width)
	innerWidth := cardContentWidth(width)
	contentLimit := max(1, maxHeight-4)
	help := []string{subtleStyle.Render("↑/↓ select · PgUp/PgDn page · Enter confirm · Esc close")}
	if strings.HasPrefix(m.menu.Title, "Help") {
		// Keep a compact pointer to the lower section visible while the full
		// help list is paged. This also makes the scrollable nature of the
		// modal discoverable before the user reaches its final rows.
		help = append(help, subtleStyle.Render("More: HASH COMMANDS · #plan · #budget [amount]"))
	}
	helpLines := wrapPhysicalLines(help, innerWidth)
	optionRows := max(1, contentLimit-len(helpLines)-2)
	var body []string
	for {
		m.menu.SetVisibleRows(optionRows)
		body = wrapPhysicalLines(strings.Split(strings.TrimSuffix(m.menu.ViewWindow(optionRows), "\n"), "\n"), innerWidth)
		if len(body) <= max(1, contentLimit-len(helpLines)-1) || optionRows == 1 {
			break
		}
		optionRows--
	}
	lines := append(body, "")
	lines = append(lines, helpLines...)
	return renderBoundedCard(lines, width, maxHeight, colorPurple)
}

func (m *TUIModel) renderComposer() string {
	width := max(20, m.width)
	placeholder := "Ask the agent…"
	if m.asking {
		placeholder = "Answer the agent’s question…"
	}
	if m.streaming && !m.asking {
		placeholder = "Agent is working · press Esc to stop"
	}
	editor := m.input
	editor.Placeholder = placeholder
	editor.Width = max(8, width-8)
	content := "> " + composerStyle.Render(editor.View())
	style := composerStyle.Copy().BorderForeground(colorPink)
	return style.Width(width).Padding(0, 2).BorderStyle(lipgloss.NormalBorder()).Render(content)
}

func (m *TUIModel) renderFooter() string {
	focus := focusName(m.focus)
	shortcuts := "Enter send · Alt+P plan · Alt+K commands · Tab focus · F1 help · Alt+C quit"
	if m.focus == FocusTimeline {
		shortcuts = "↑/↓ scroll chat · PgUp/PgDn page · Home/End jump · Tab focus"
	}
	if m.focus == FocusPlan || (m.focus == FocusInspector && m.inspectorTab == InspectorPlan) {
		shortcuts = "↑/↓ scroll plan · PgUp/PgDn page · Tab focus · Alt+C quit"
	}
	if m.streaming {
		shortcuts = "Esc stop · Tab focus · Alt+C cancel"
	}
	if m.layoutMode() == layoutNarrow {
		shortcuts = "Alt+P plan · Alt+F files · Alt+T tools · Alt+G git · " + shortcuts
		if m.showNarrow {
			shortcuts = "Esc conversation · " + shortcuts
		}
	}
	footer := fmt.Sprintf("  FOCUS %s   %s", strings.ToUpper(focus), shortcuts)
	return subtleStyle.Render(truncateCells(footer, max(1, m.width)))
}
