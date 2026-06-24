package cli

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The AI sandbox welcome reuses the TUI's Minecraft landscape palette: cream
// logo, grass green, sky blue, soft dirt. Kept small and self-contained so it
// renders the same whether shown by the launcher or by `boxly ssh`.
var (
	aiCream = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffe1a8"))
	aiGrass = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8fd35a"))
	aiSky   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3"))
	aiDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9fb0a8"))
	aiChip  = lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#fff3d6")).
		Background(lipgloss.Color("#3f7a2a")).
		Padding(0, 1)
)

// aiRobot is a tiny pixel mascot drawn with box characters so it stays aligned
// in any monospace terminal.
const aiRobot = `  ╷
┌─┴────┐
│ ●  ● │
│  ──  │
└──────┘`

// renderAIWelcome returns the themed banner printed when a user enters an AI
// sandbox box. It ends with a newline so the shell prompt starts on a clean row.
func renderAIWelcome(id string) string {
	art := aiCream.Render(aiRobot)

	head := lipgloss.JoinVertical(lipgloss.Left,
		"",
		aiCream.Render("B O X L Y"),
		aiGrass.Render("A I   S A N D B O X"),
		aiDim.Render("quick AI tasks · powered by Claude"),
	)

	banner := lipgloss.JoinHorizontal(lipgloss.Top, art, "   ", head)

	var b strings.Builder
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(banner, "\n", "\r\n"))
	b.WriteString("\r\n\r\n")
	b.WriteString("  " + aiDim.Render("Activate Claude") + "   " + aiChip.Render(" claude ") + "\r\n")
	b.WriteString("  " + aiDim.Render("One-shot      ") + "   " + aiSky.Render(`claude -p "summarize the files in /work"`) + "\r\n")
	b.WriteString("  " + aiDim.Render("Your box      ") + "   " + aiSky.Render(id) + aiDim.Render("  · /work is your home") + "\r\n")
	b.WriteString("  " + aiDim.Render("Your key is loaded for this session only — never stored.") + "\r\n\r\n")
	return b.String()
}
