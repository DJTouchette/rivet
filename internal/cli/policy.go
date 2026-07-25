package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/config"
	"github.com/djtouchette/rivet/internal/policy"
	"github.com/spf13/cobra"
)

func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Inspect and check policy rules",
		Long: `Policy rules gate capability execution based on environment conditions.

Rules are declared in .rivet/config.yaml under the 'policies' key.
Each rule matches capabilities by safety level, kind, or name,
and enforces environment constraints (require_env, deny_env).`,
	}

	cmd.AddCommand(
		newPolicyStatusCmd(),
		newPolicyCheckCmd(),
	)

	return cmd
}

func newPolicyStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show all policy rules and their current state",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			rules := buildPolicies(cfg)
			if len(rules) == 0 {
				fmt.Println("No policy rules configured.")
				fmt.Println("Add rules to the 'policies' section of .rivet/config.yaml")
				return nil
			}

			for _, rule := range rules {
				fmt.Printf("Rule: %s\n", rule.Name)
				if rule.Description != "" {
					fmt.Printf("  Description: %s\n", rule.Description)
				}
				fmt.Printf("  Match:       %s\n", formatMatch(&rule.Match))
				if len(rule.RequireEnv) > 0 {
					fmt.Printf("  Require env: %v\n", rule.RequireEnv)
				}
				if len(rule.DenyEnv) > 0 {
					fmt.Printf("  Deny env:    %v\n", rule.DenyEnv)
				}

				// Show current env state for this rule.
				violations := policy.CheckEnv(&rule, nil)
				if len(violations) > 0 {
					fmt.Printf("  Status:      WOULD BLOCK (%s)\n", policy.FormatViolations(violations))
				} else {
					fmt.Printf("  Status:      env OK\n")
				}
				fmt.Println()
			}
			return nil
		},
	}
}

func newPolicyCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <capability>",
		Short: "Check if a capability would be blocked by policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			capName := args[0]

			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			reg := buildRegistry(cfg)
			cap := reg.Get(capName)
			if cap == nil {
				return fmt.Errorf("capability %q not found; run 'rivet inspect capabilities' to see available capabilities", capName)
			}

			rules := buildPolicies(cfg)
			if len(rules) == 0 {
				fmt.Printf("No policy rules configured. %q is allowed.\n", capName)
				return nil
			}

			violations := policy.Check(rules, cap, nil)
			if len(violations) == 0 {
				fmt.Printf("%q passes all policy checks.\n", capName)
				return nil
			}

			fmt.Printf("%q is blocked by policy:\n", capName)
			for _, v := range violations {
				fmt.Printf("  - %s\n", v)
			}
			return fmt.Errorf("policy check failed")
		},
	}
}

func formatMatch(m *policy.Match) string {
	var parts []string
	if m.Safety != "" {
		parts = append(parts, fmt.Sprintf("safety=%s", m.Safety))
	}
	if m.Kind != "" {
		parts = append(parts, fmt.Sprintf("kind=%s", m.Kind))
	}
	if len(m.Capabilities) > 0 {
		parts = append(parts, fmt.Sprintf("capabilities=%v", m.Capabilities))
	}
	if len(parts) == 0 {
		return "(all capabilities)"
	}
	return fmt.Sprintf("%v", parts)
}
