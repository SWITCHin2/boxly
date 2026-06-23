package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// runWithSpinner animates a colorful braille spinner with rotating step
// messages on stderr while work runs in the background, then clears the line.
// Terminal-only eye-candy for the "creating your box" moment.
func runWithSpinner(steps []string, work func() error) error {
	done := make(chan error, 1)
	go func() { done <- work() }()

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	colors := []string{"#7D56F4", "#00D7AF", "#FF87D7", "#FFD787", "#5FD7FF"}
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#C8C8C8"))

	t := time.NewTicker(90 * time.Millisecond)
	defer t.Stop()

	i, stepIdx, stepTick := 0, 0, 0
	for {
		select {
		case err := <-done:
			fmt.Fprint(os.Stderr, "\r\033[K") // clear the spinner line
			return err
		case <-t.C:
			frame := lipgloss.NewStyle().
				Foreground(lipgloss.Color(colors[(i/2)%len(colors)])).
				Bold(true).
				Render(frames[i%len(frames)])
			fmt.Fprintf(os.Stderr, "\r\033[K  %s %s", frame, msgStyle.Render(steps[stepIdx%len(steps)]))
			i++
			if stepTick++; stepTick >= 12 { // advance the message every ~1.1s
				stepTick, stepIdx = 0, stepIdx+1
			}
		}
	}
}
