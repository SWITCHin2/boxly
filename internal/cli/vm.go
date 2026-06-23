package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/SWITCHin2/boxly/pkg/api"
)

func newVMCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "vm", Short: "Manage VMs"}
	cmd.AddCommand(newCreateCmd(), newListCmd(), newRmCmd(), newInfoCmd())
	return cmd
}

func newCreateCmd() *cobra.Command {
	var (
		typ     string
		tmpl    string
		image   string
		ttl     time.Duration
		name    string
		connect bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a box (interactive when run with no flags)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// No flags → behave like the cloud-console launcher.
			anyFlag := false
			for _, f := range []string{"type", "template", "image", "ttl", "name"} {
				if cmd.Flags().Changed(f) {
					anyFlag = true
				}
			}
			if !anyFlag {
				return runWizard(cmd.Context())
			}

			start := time.Now()
			vmObj, err := newClient().Create(cmd.Context(), api.CreateRequest{
				Type:       api.VMType(typ),
				Template:   tmpl,
				Image:      image,
				TTLSeconds: int(ttl.Seconds()),
				Name:       name,
			})
			if err != nil {
				return err
			}
			fmt.Printf("created %s (%s/%s, %s) in %s\n", vmObj.ID, vmObj.Type, dash(vmObj.Template), vmObj.Status, time.Since(start).Round(time.Millisecond))
			if connect {
				if vmObj.Status != "Running" {
					if err := waitReadySilent(cmd.Context(), vmObj.ID); err != nil {
						return err
					}
				}
				return runShell(cmd.Context(), vmObj.ID)
			}
			fmt.Printf("connect with: boxly ssh %s\n", vmObj.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "sandbox", "sandbox | persistent")
	cmd.Flags().StringVar(&tmpl, "template", "", "box type id (e.g. normal, learn-git, learn-sql, dev-python)")
	cmd.Flags().StringVar(&image, "image", "", "container image (advanced; default: template's image)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "sandbox time-to-live, e.g. 1h (default: server default)")
	cmd.Flags().StringVar(&name, "name", "", "friendly name")
	cmd.Flags().BoolVar(&connect, "connect", false, "drop into the shell after creating")
	return cmd
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List VMs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			vms, err := newClient().List(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tTYPE\tTEMPLATE\tSTATUS\tEXPIRES")
			for _, v := range vms {
				exp := "-"
				if v.ExpiresAt != nil {
					exp = time.Until(*v.ExpiresAt).Round(time.Second).String()
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, dash(v.Name), v.Type, dash(v.Template), v.Status, exp)
			}
			return tw.Flush()
		},
	}
}

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("deleted %s\n", args[0])
			return nil
		},
	}
}

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <id>",
		Short: "Show VM details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := newClient().Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("ID:      %s\nName:    %s\nType:    %s\nStatus:  %s\nImage:   %s\nOwner:   %s\nCreated: %s\n",
				v.ID, dash(v.Name), v.Type, v.Status, v.Image, v.Owner, v.CreatedAt.Format(time.RFC3339))
			if v.ExpiresAt != nil {
				fmt.Printf("Expires: %s (%s)\n", v.ExpiresAt.Format(time.RFC3339), time.Until(*v.ExpiresAt).Round(time.Second))
			}
			return nil
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
