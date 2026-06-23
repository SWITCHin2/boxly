package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	var open bool
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Open the admin web console",
		RunE: func(_ *cobra.Command, _ []string) error {
			url := strings.TrimRight(flagServer, "/") + "/admin"
			fmt.Println("OnGo admin console:", url)
			fmt.Println("Sign in with your admin token (BOXLY_ADMIN_TOKEN).")
			if open {
				_ = openBrowser(url)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&open, "open", true, "open the console in your browser")
	return cmd
}

func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		name = "xdg-open"
	}
	return exec.Command(name, append(args, url)...).Start()
}
