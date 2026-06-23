package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/SWITCHin2/boxly/internal/template"
	"github.com/SWITCHin2/boxly/pkg/api"
)

func newLaunchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "launch",
		Aliases: []string{"new"},
		Short:   "Interactively pick a box type and jump in",
		RunE:    func(cmd *cobra.Command, _ []string) error { return runWizard(cmd.Context()) },
	}
}

// runWizard presents the cloud-console-style menu, creates the chosen box, and
// drops the user straight into a shell.
func runWizard(ctx context.Context) error {
	tmplID := template.Default
	vmType := string(api.TypeSandbox)
	name := ""

	cat := template.All()
	opts := make([]huh.Option[string], 0, len(cat))
	for _, t := range cat {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-22s %s", t.Title, "· "+t.Desc), t.ID))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What do you want to launch?").
				Options(opts...).
				Value(&tmplID),
			huh.NewSelect[string]().
				Title("Keep it after you're done?").
				Options(
					huh.NewOption("Disposable — auto-expires (recommended)", string(api.TypeSandbox)),
					huh.NewOption("Persistent — remembers your files", string(api.TypePersistent)),
				).
				Value(&vmType),
			huh.NewInput().
				Title("Name (optional)").
				Value(&name),
		),
	)
	if err := form.Run(); err != nil {
		return err // includes user abort (Ctrl-C)
	}

	return provisionAndConnect(ctx, api.CreateRequest{Template: tmplID, Type: api.VMType(vmType), Name: name})
}

var readyStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00D7AF"))

// provisionAndConnect creates a box behind a colorful spinner, then drops the
// user into its shell. Shared by the wizard and `vm create --connect`.
func provisionAndConnect(ctx context.Context, req api.CreateRequest) error {
	steps := []string{
		"spinning up your box…",
		"unpacking the goodies…",
		"warming things up…",
		"almost there…",
	}
	var vm *api.VM
	err := runWithSpinner(steps, func() error {
		v, e := newClient().Create(ctx, req)
		if e != nil {
			return e
		}
		vm = v
		return waitReadySilent(ctx, v.ID)
	})
	if err != nil {
		return err
	}

	tmpl, _ := template.Get(vm.Template)
	fmt.Fprintf(os.Stderr, "%s  %s\n", readyStyle.Render("  "+vm.ID+" ready"), tmpl.WelcomeMsg)
	return runShell(ctx, vm.ID)
}

// waitReadySilent polls until the box reports Running (no output of its own —
// the spinner provides the visuals).
func waitReadySilent(ctx context.Context, id string) error {
	deadline := time.Now().Add(120 * time.Second)
	for {
		vm, err := newClient().Get(ctx, id)
		if err == nil && vm.Status == "Running" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("box %s did not become ready in time", id)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
