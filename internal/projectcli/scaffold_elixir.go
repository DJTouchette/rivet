package projectcli

import (
	"fmt"
	"strings"
)

// ScaffoldElixir generates Mix task files for an Elixir project CLI.
// The tasks are placed directly in the project's lib/mix/tasks/ directory.
// cliName is used as the task namespace (e.g. "project" → mix project.query.status).
func ScaffoldElixir(dir, cliName string) (*ScaffoldResult, error) {
	if cliName == "" {
		cliName = "project"
	}

	files := scaffoldElixirFiles(cliName)

	result := &ScaffoldResult{Dir: dir}

	for _, f := range files {
		path := f.relPath
		if dir != "" {
			path = dir + "/" + f.relPath
		}

		if err := mkdirForFile(path); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", f.relPath, err)
		}

		if fileExists(path) {
			result.Skipped = append(result.Skipped, f.relPath)
			continue
		}

		if err := writeFile(path, f.content); err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.relPath, err)
		}
		result.Files = append(result.Files, f.relPath)
	}

	return result, nil
}

// DiscoverElixirCapabilities returns capability definitions that the
// Elixir scaffold would register.
func DiscoverElixirCapabilities(cliName string) []DiscoveredCapability {
	return []DiscoveredCapability{
		{
			Name:        cliName + ".status",
			Description: "Show project status summary",
			Command:     []string{"mix", cliName + ".query.status", "--json"},
			Output:      "json",
			Safety:      "safe",
		},
		{
			Name:        cliName + ".health",
			Description: "Run health checks",
			Command:     []string{"mix", cliName + ".check.health", "--json"},
			Output:      "json",
			Safety:      "safe",
		},
		{
			Name:        cliName + ".seed",
			Description: "Seed development data",
			Command:     []string{"mix", cliName + ".task.seed", "--json"},
			Output:      "json",
			Safety:      "guarded",
		},
	}
}

func scaffoldElixirFiles(cliName string) []scaffoldFile {
	ns := cliName
	modPrefix := elixirModName(cliName)

	return []scaffoldFile{
		{
			relPath: fmt.Sprintf("lib/mix/tasks/%s/query/status.ex", ns),
			content: tmplElixirQueryStatus(ns, modPrefix),
		},
		{
			relPath: fmt.Sprintf("lib/mix/tasks/%s/check/health.ex", ns),
			content: tmplElixirCheckHealth(ns, modPrefix),
		},
		{
			relPath: fmt.Sprintf("lib/mix/tasks/%s/task/seed.ex", ns),
			content: tmplElixirTaskSeed(ns, modPrefix),
		},
		{
			relPath: fmt.Sprintf("lib/mix/tasks/%s/rivet_discover.ex", ns),
			content: tmplElixirDiscover(ns, modPrefix),
		},
	}
}

// elixirModName converts a CLI name to an Elixir module name.
// "project" → "Project", "my_cli" → "MyCli", "my-cli" → "MyCli"
func elixirModName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	var result []string
	for _, p := range parts {
		if len(p) > 0 {
			result = append(result, strings.ToUpper(p[:1])+p[1:])
		}
	}
	if len(result) == 0 {
		return "Project"
	}
	return strings.Join(result, "")
}

func tmplElixirQueryStatus(ns, modPrefix string) string {
	return fmt.Sprintf(`defmodule Mix.Tasks.%s.Query.Status do
  @moduledoc "Show project status summary"
  @shortdoc "Show project status summary"

  use Mix.Task

  @impl true
  def run(args) do
    {opts, _, _} = OptionParser.parse(args, switches: [json: :boolean])
    json? = Keyword.get(opts, :json, false)

    # TODO: Replace with real project status logic.
    status = %%{
      status: "ok",
      elixir: System.version(),
      otp: :erlang.system_info(:otp_release) |> List.to_string()
    }

    if json? do
      IO.puts(Jason.encode!(status, pretty: true))
    else
      IO.puts("Status:  #{status.status}")
      IO.puts("Elixir:  #{status.elixir}")
      IO.puts("OTP:     #{status.otp}")
    end
  end
end
`, modPrefix)
}

func tmplElixirCheckHealth(ns, modPrefix string) string {
	return fmt.Sprintf(`defmodule Mix.Tasks.%s.Check.Health do
  @moduledoc "Run health checks"
  @shortdoc "Run health checks"

  use Mix.Task

  @impl true
  def run(args) do
    {opts, _, _} = OptionParser.parse(args, switches: [json: :boolean])
    json? = Keyword.get(opts, :json, false)

    # TODO: Replace with real health checks.
    checks = [
      %%{name: "config", status: "pass", detail: "configuration loaded"},
      %%{name: "dependencies", status: "pass", detail: "all dependencies available"}
    ]

    healthy = Enum.all?(checks, &(&1.status == "pass"))

    if json? do
      IO.puts(Jason.encode!(%%{healthy: healthy, checks: checks}, pretty: true))
    else
      Enum.each(checks, fn c ->
        icon = if c.status == "pass", do: "OK", else: "FAIL"
        IO.puts("  [#{icon}] #{c.name} — #{c.detail}")
      end)

      if healthy do
        IO.puts("\nAll checks passed.")
      else
        IO.puts("\nSome checks failed.")
      end
    end
  end
end
`, modPrefix)
}

func tmplElixirTaskSeed(ns, modPrefix string) string {
	return fmt.Sprintf(`defmodule Mix.Tasks.%s.Task.Seed do
  @moduledoc "Seed development data"
  @shortdoc "Seed development data"

  use Mix.Task

  @impl true
  def run(args) do
    {opts, _, _} = OptionParser.parse(args, switches: [json: :boolean])
    json? = Keyword.get(opts, :json, false)

    # TODO: Replace with real seed logic.
    result = %%{
      seeded: true,
      records: %%{users: 5, items: 20}
    }

    if json? do
      IO.puts(Jason.encode!(result, pretty: true))
    else
      IO.puts("Seeded development data:")
      IO.puts("  users:  5")
      IO.puts("  items:  20")
    end
  end
end
`, modPrefix)
}

func tmplElixirDiscover(ns, modPrefix string) string {
	return fmt.Sprintf(`defmodule Mix.Tasks.%s.RivetDiscover do
  @moduledoc false
  @shortdoc "Output capability definitions for Rivet registration"

  use Mix.Task

  @impl true
  def run(_args) do
    result = %%{
      capabilities: [
        %%{
          name: "%s.status",
          kind: "project_command",
          description: "Show project status summary",
          command: ["mix", "%s.query.status"],
          output: "json",
          safety: "safe"
        },
        %%{
          name: "%s.health",
          kind: "project_command",
          description: "Run health checks",
          command: ["mix", "%s.check.health"],
          output: "json",
          safety: "safe"
        },
        %%{
          name: "%s.seed",
          kind: "project_command",
          description: "Seed development data",
          command: ["mix", "%s.task.seed"],
          output: "json",
          safety: "guarded"
        }
      ]
    }

    IO.puts(Jason.encode!(result, pretty: true))
  end
end
`, modPrefix, ns, ns, ns, ns, ns, ns)
}
