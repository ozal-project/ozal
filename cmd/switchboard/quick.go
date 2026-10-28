package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

type versionInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func currentVersionInfo() versionInfo {
	info := versionInfo{Name: "switchboard", Version: buildVersion, Commit: buildCommit, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
		if info.Commit == "unknown" {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" && setting.Value != "" {
					info.Commit = setting.Value
					if len(info.Commit) > 12 {
						info.Commit = info.Commit[:12]
					}
					break
				}
			}
		}
	}
	return info
}

func runVersion(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(out)
	jsonOutput := fs.Bool("json", false, "print build information as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: switchboard version [-json]")
	}
	info := currentVersionInfo()
	if *jsonOutput {
		return json.NewEncoder(out).Encode(info)
	}
	fmt.Fprintf(out, "%s %s\ncommit: %s\ngo: %s\nplatform: %s/%s\n", info.Name, info.Version, info.Commit, info.Go, info.OS, info.Arch)
	return nil
}

func runInit(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("config", "config.json", "new config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: switchboard init [-config PATH]")
	}

	candidate := config.Config{
		Listen: "127.0.0.1:8888",
		Admin:  config.Admin{Listen: config.DefaultAdminListen},
		Routes: []config.Route{{Host: "app.localhost:8888", Target: "http://127.0.0.1:3000"}},
	}
	validated, err := config.Validate(candidate)
	if err != nil {
		return fmt.Errorf("validate starter config: %w", err)
	}
	data, err := json.MarshalIndent(validated, "", "  ")
	if err != nil {
		return fmt.Errorf("encode starter config: %w", err)
	}
	data = append(data, '\n')
	if err := writeNewPrivateFile(*path, data); err != nil {
		return fmt.Errorf("create config: %w", err)
	}
	fmt.Fprintf(out, "created %s (mode 0600)\n", *path)
	fmt.Fprintf(out, "next: edit its routes, then run `switchboard validate -config %s`\n", *path)
	return nil
}

func writeNewPrivateFile(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if f != nil {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		f = nil
		_ = os.Remove(path)
		return err
	}
	f = nil
	return nil
}

func runSchema(_ context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: switchboard schema")
	}
	schema, err := config.JSONSchema()
	if err != nil {
		return err
	}
	_, err = out.Write(schema)
	return err
}

func runCompletion(_ context.Context, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: switchboard completion bash|zsh|fish|powershell")
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		return fmt.Errorf("unsupported shell %q (choose bash, zsh, fish or powershell)", args[0])
	}
	_, err := io.WriteString(out, script)
	return err
}

var completionScripts = map[string]string{
	"bash": `# Add to ~/.bashrc: eval "$(switchboard completion bash)"
_switchboard_complete() {
  local current="${COMP_WORDS[COMP_CWORD]}"
  local commands="audit completion console db doctor github init migrate notify policy runtime schema setup site tunnel tunnel-spike upgrade user validate version connector"
  if (( COMP_CWORD == 1 )); then
    COMPREPLY=( $(compgen -W "$commands" -- "$current") )
    return
  fi
  if (( COMP_CWORD == 2 )); then
    case "${COMP_WORDS[1]}" in
      audit) COMPREPLY=( $(compgen -W "keygen checkpoint repair verify" -- "$current") ) ;;
      console) COMPREPLY=( $(compgen -W "login list revoke" -- "$current") ) ;;
      connector) COMPREPLY=( $(compgen -W "prepare sign check enroll rotate current revoke list" -- "$current") ) ;;
      github) COMPREPLY=( $(compgen -W "connect disconnect" -- "$current") ) ;;
      runtime) COMPREPLY=( $(compgen -W "init" -- "$current") ) ;;
      db) COMPREPLY=( $(compgen -W "backup restore" -- "$current") ) ;;
      migrate) COMPREPLY=( $(compgen -W "up status" -- "$current") ) ;;
      policy) COMPREPLY=( $(compgen -W "explain replay who-can review" -- "$current") ) ;;
      site) COMPREPLY=( $(compgen -W "deploy list rollback" -- "$current") ) ;;
      tunnel) COMPREPLY=( $(compgen -W "edge connect renew" -- "$current") ) ;;
      tunnel-spike) COMPREPLY=( $(compgen -W "init edge connect" -- "$current") ) ;;
    esac
  fi
}
complete -F _switchboard_complete switchboard
`,
	"zsh": `# Add to ~/.zshrc: eval "$(switchboard completion zsh)"
#compdef switchboard
_switchboard() {
  local -a commands
  commands=(
    'audit:durable audit commands'
    'completion:print shell completion'
    'console:host-issued private operator sessions'
    'github:configure optional repository discovery'
    'db:backup and restore state'
    'doctor:check config and state'
    'init:create a starter config'
    'migrate:inspect or apply database migrations'
    'notify:notification commands'
    'policy:policy commands'
    'runtime:initialize operational policy'
    'schema:print JSON Schema'
    'setup:create a protected local installation'
    'site:site deployment commands'
    'tunnel:tunnel commands'
    'tunnel-spike:local tunnel experiment'
    'upgrade:upgrade a systemd service'
    'user:user and passkey commands'
    'validate:validate config'
    'version:print build information'
    'connector:connector commands'
  )
  _describe 'switchboard command' commands
  case "$words[2]" in
    audit) _values 'audit action' keygen checkpoint repair verify ;;
    console) _values 'console action' login list revoke ;;
    connector) _values 'connector action' prepare sign check enroll rotate current revoke list ;;
    github) _values 'github action' connect disconnect ;;
    db) _values 'db action' backup restore ;;
    migrate) _values 'migration action' up status ;;
    runtime) _values 'runtime action' init ;;
    site) _values 'site action' deploy list rollback ;;
    tunnel) _values 'tunnel action' edge connect renew ;;
    tunnel-spike) _values 'tunnel-spike action' init edge connect ;;
    policy) _values 'policy action' explain replay who-can review ;;
  esac
}
compdef _switchboard switchboard
`,
	"fish": `# Save as ~/.config/fish/completions/switchboard.fish:
complete -c switchboard -f -n '__fish_use_subcommand' -a 'audit completion console db doctor github init migrate notify policy runtime schema setup site tunnel tunnel-spike upgrade user validate version connector'
complete -c switchboard -f -n '__fish_seen_subcommand_from console' -a 'login list revoke'
complete -c switchboard -f -n '__fish_seen_subcommand_from github' -a 'connect disconnect'
complete -c switchboard -f -n '__fish_seen_subcommand_from runtime' -a 'init'
complete -c switchboard -f -n '__fish_seen_subcommand_from connector' -a 'prepare sign check enroll rotate current revoke list'
complete -c switchboard -f -n '__fish_seen_subcommand_from audit' -a 'keygen checkpoint repair verify'
complete -c switchboard -f -n '__fish_seen_subcommand_from db' -a 'backup restore'
complete -c switchboard -f -n '__fish_seen_subcommand_from migrate' -a 'up status'
complete -c switchboard -f -n '__fish_seen_subcommand_from policy' -a 'explain replay who-can review'
complete -c switchboard -f -n '__fish_seen_subcommand_from site' -a 'deploy list rollback'
complete -c switchboard -f -n '__fish_seen_subcommand_from tunnel' -a 'edge connect renew'
complete -c switchboard -f -n '__fish_seen_subcommand_from tunnel-spike' -a 'init edge connect'
`,
	"powershell": `# Add to $PROFILE:
Register-ArgumentCompleter -Native -CommandName switchboard -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = @('audit','completion','console','db','doctor','github','init','migrate','notify','policy','runtime','schema','setup','site','tunnel','tunnel-spike','upgrade','user','validate','version','connector')
  $tokens = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
  if ($tokens.Count -gt 1) {
    switch ($tokens[1]) {
      'audit' { $words = @('keygen','checkpoint','repair','verify') }
      'console' { $words = @('login','list','revoke') }
      'github' { $words = @('connect','disconnect') }
      'db' { $words = @('backup','restore') }
      'migrate' { $words = @('up','status') }
      'runtime' { $words = @('init') }
      'connector' { $words = @('prepare','sign','check','enroll','rotate','current','revoke','list') }
      'policy' { $words = @('explain','replay','who-can','review') }
      'site' { $words = @('deploy','list','rollback') }
      'tunnel' { $words = @('edge','connect','renew') }
      'tunnel-spike' { $words = @('init','edge','connect') }
      default { $words = @() }
    }
  }
  $words | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`,
}

func runDoctor(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", "config.json", "routing config path")
	dbPath := fs.String("db", "", "optional SQLite state database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: switchboard doctor [-config PATH] [-db PATH]")
	}

	problems := 0
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(out, "config: FAIL — cannot load or validate it; details are suppressed so config values cannot be echoed")
		fmt.Fprintln(out, "  action: inspect JSON syntax, field names and route requirements locally")
		problems++
	} else {
		fmt.Fprintln(out, "config: OK — parsed and passed Switchboard validation")
	}

	if *dbPath == "" {
		if configNeedsState(cfg) {
			fmt.Fprintln(out, "database: FAIL — this config needs persistent state; pass -db PATH and apply migrations with `switchboard migrate up -db PATH`")
			problems++
		} else {
			fmt.Fprintln(out, "database: not configured — sessions are process-local; pass -db PATH for persistent state")
		}
	} else if checkDatabase(ctx, *dbPath, out) != nil {
		problems++
	}

	if problems > 0 {
		return errors.New("doctor found action items")
	}
	return nil
}

func configNeedsState(cfg *config.Config) bool {
	return cfg != nil && (cfg.Pages != nil || cfg.Tunnel != nil || cfg.Auth.Passkeys != nil)
}

func checkDatabase(ctx context.Context, path string, out io.Writer) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		fmt.Fprintln(out, "database: FAIL — cannot access a regular database file; check the path and permissions")
		return errors.New("database unavailable")
	}
	db, err := sqlite.OpenReadOnly(ctx, path)
	if err != nil {
		fmt.Fprintln(out, "database: FAIL — cannot open read-only; check permissions and restore from a known-good backup if needed")
		return errors.New("database unavailable")
	}
	defer db.Close()
	if err := sqlite.IntegrityCheck(ctx, db); err != nil {
		fmt.Fprintln(out, "database: FAIL — integrity check did not pass; stop the service and restore a known-good backup")
		return errors.New("database integrity check failed")
	}
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		fmt.Fprintln(out, "database: FAIL — bundled migration metadata is invalid; use a verified binary")
		return errors.New("migration metadata invalid")
	}
	statuses, err := sqlite.ReadOnlyMigrationStatus(ctx, db, migrations)
	if err != nil {
		fmt.Fprintln(out, "database: FAIL — migration history does not match this binary; do not start the service with it")
		return errors.New("migration history invalid")
	}
	pending := 0
	for _, status := range statuses {
		if !status.Applied {
			pending++
		}
	}
	if pending > 0 {
		fmt.Fprintf(out, "database: WARN — %d migration(s) pending; apply them explicitly with `switchboard migrate up -db <path>`\n", pending)
		return errors.New("migrations pending")
	}
	fmt.Fprintln(out, "database: OK — integrity and migration history match this binary")
	return nil
}
