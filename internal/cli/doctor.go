package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate the Rivet environment",
		Long:  "Checks that .rivet/ is properly set up: config parseable, project CLI exists, capabilities valid, context well-formed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			result := doctor.Run()

			for _, c := range result.Checks {
				icon := statusIcon(c.Status)
				fmt.Printf("  %s  %-22s %s\n", icon, c.Name, c.Message)
			}

			fmt.Println()
			if result.HasFailures() {
				fmt.Println("Some checks failed. Fix the issues above and re-run 'rivet doctor'.")
				return fmt.Errorf("doctor found failures")
			}
			fmt.Println("All checks passed.")
			return nil
		},
	}
}

func statusIcon(s doctor.Status) string {
	switch s {
	case doctor.StatusOK:
		return "OK  "
	case doctor.StatusWarn:
		return "WARN"
	case doctor.StatusFail:
		return "FAIL"
	case doctor.StatusSkip:
		return "SKIP"
	default:
		return "????"
	}
}
