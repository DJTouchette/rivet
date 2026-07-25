package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/config"
	"github.com/djtouchette/rivet/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate the Rivet environment",
		Long:  "Checks that .rivet/ is properly set up: config parseable, project CLI exists, capabilities valid, context well-formed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the gated tool groups the same way buildRegistry does, so
			// doctor reports what Claude actually gets rather than a guess.
			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}
			result := doctor.Run(builtinGroupsFor(cfg))

			for _, c := range result.Checks {
				icon := statusIcon(c.Status)
				fmt.Printf("  %s  %-22s %s\n", icon, c.Name, c.Message)
			}

			fmt.Println()
			if result.HasFailures() {
				fmt.Println("Some checks failed. Fix the issues above and re-run 'rivet doctor'.")
				return fmt.Errorf("doctor found failures")
			}
			warns := 0
			for _, c := range result.Checks {
				if c.Status == doctor.StatusWarn {
					warns++
				}
			}
			if warns > 0 {
				// Saying "all checks passed" over visible warnings is the same
				// habit as reporting an empty list as "none": technically about
				// failures, read as "nothing to see".
				fmt.Printf("No failures, but %d warning(s) above are worth a look.\n", warns)
				return nil
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
