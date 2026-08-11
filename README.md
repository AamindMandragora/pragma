# `pragma`

## Install

On macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/AamindMandragora/pragma/main/install.sh | sh
```

On Windows:

```PowerShell
irm https://raw.githubusercontent.com/AamindMandragora/pragma/main/install.ps1 | iex
```

## Headless mode

Run a single task without the interactive TUI. The response is written to
stdout, so the command can be used from scripts or another agent:

```bash
pragma --headless "Inspect the failing tests and fix the bug"
# equivalent subcommand form:
pragma headless "Summarize the current architecture"
```

When no prompt argument is supplied, Pragma reads the task from stdin:

```bash
printf '%s\n' "Run the focused test suite and report failures" | pragma --headless
```

In headless mode confirmations are accepted automatically and `ask_user`
returns a non-interactive response instead of blocking. A top-level agent can
also delegate an isolated task to the `subagent` tool; child agents cannot
delegate again.

## TUI controls

- `Alt+P` toggles plan mode. The next prompt is turned into a plan for approval.
- `Alt+Shift+P`, `Alt+F`, `Alt+T`, and `Alt+G` open the plan, files, tools, and git inspectors.
- `Alt+K` opens the command palette; `Alt+H` or `F1` opens keyboard help.
- `Esc` stops the active run or clears the composer; `Alt+C` cancels and then quits.
- Type `#help` in the composer for the complete shortcut list and all hash commands.
- In help, plan, and conversation views, use `↑/↓`, `PageUp/PageDown`, `Home/End`, or the mouse wheel to scroll.
