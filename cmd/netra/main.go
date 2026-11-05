// Command netra is the NETRA application installed on a client's server: the service and its
// maintenance commands.
//
//	netra [-config netra.env] [serve]          run the service
//	netra serve -roles api,gateway             run some roles of a split installation
//	netra version                              the version
//	netra installation-id                      this server's installation ID (licenses are bound to it)
//	netra reset-password <user> [password]     set a new password (a generated one when none is given)
//	netra backup [-out dir]                    back up the database now
//	netra restore -yes <file.dump>             replace the database with a backup (stop the service first)
//	netra license <file.lic>                   check a license against this build and this server
//
// The configuration is the environment, completed by an environment file: -config, else
// NETRA_CONFIG, else netra.env next to the program, else the system one (/etc/netra/netra.env,
// %ProgramData%\NETRA\netra.env, /Library/Application Support/NETRA/netra.env).
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // time zones without the system's database (Windows)

	"github.com/DarmawanWeb/netra-fleet-be/internal/app"
	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/backup"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

func main() {
	global := flag.NewFlagSet("netra", flag.ExitOnError)
	configFile := global.String("config", "", "environment file with the configuration")
	global.Usage = func() { usage(os.Stderr) }
	_ = global.Parse(os.Args[1:])
	args := global.Args()
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if cmd == "version" || cmd == "-version" || cmd == "--version" {
		fmt.Println("netra", version.String())
		return
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		usage(os.Stdout)
		return
	}
	if file := findConfig(*configFile); file != "" {
		if err := config.LoadFile(file); err != nil {
			fatal(fmt.Errorf("config %s: %w", file, err))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config.Load()
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, cfg, args)
	case "installation-id":
		err = installationID(ctx, cfg)
	case "reset-password":
		err = resetPassword(ctx, cfg, args)
	case "backup":
		err = runBackup(ctx, cfg, args)
	case "restore":
		err = restore(ctx, cfg, args)
	case "license":
		err = checkLicense(ctx, cfg, args)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: netra [-config netra.env] <command>

  serve [-roles list]            run the service (the default); -roles api,gateway,worker runs
                                 only those roles (default NETRA_ROLES, else all)
  version                        print the version
  installation-id                print this server's installation ID
  reset-password <user> [pass]   set a new password; a generated one is printed when none is given
  backup [-out dir]              back up the database now
  restore -yes <file.dump>       replace the database with a backup; stop the service first
  license <file.lic>             check a license against this build and this server`)
}

// serve runs the service; -roles overrides NETRA_ROLES.
func serve(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	roles := fs.String("roles", "", "roles to run: all, or a comma list of api, gateway and worker (default NETRA_ROLES, else all)")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("serve: unexpected argument %q", fs.Arg(0))
	}
	if *roles != "" {
		cfg.Roles = *roles
	}
	return app.Serve(ctx, cfg)
}

func fatal(err error) {
	slog.Error("netra", "err", err)
	os.Exit(1)
}

// findConfig returns the environment file to read, or "" when there is none.
func findConfig(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("NETRA_CONFIG"); env != "" {
		return env
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "netra.env"))
	}
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			candidates = append(candidates, filepath.Join(pd, "NETRA", "netra.env"))
		}
	case "darwin":
		candidates = append(candidates, "/Library/Application Support/NETRA/netra.env")
	default:
		candidates = append(candidates, "/etc/netra/netra.env")
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func installationID(ctx context.Context, cfg config.Config) error {
	db, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	id, err := db.Store.EnsureSetting(ctx, store.SettingInstallation, license.NewInstallationID())
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// resetPassword sets a user's password from the server's console, for an administrator who
// cannot sign in; every session of the user ends.
func resetPassword(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: netra reset-password <user> [password]")
	}
	username, password := args[0], ""
	if len(args) == 2 {
		password = args[1]
	}
	generated := password == ""
	if generated {
		password = newPassword()
	}
	if len(password) < 8 {
		return errors.New("the password needs at least 8 characters")
	}
	db, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	id, _, role, disabled, err := db.Store.UserLogin(ctx, username)
	if err != nil {
		return fmt.Errorf("no user %q", username)
	}
	if err := db.Store.SetPassword(ctx, id, auth.HashPassword(password), ""); err != nil {
		return err
	}
	_ = db.Store.InsertAudit(ctx, store.AuditEntry{Username: "console", Action: "reset_password", Target: username,
		IP: "local"})
	fmt.Printf("password of %s (%s) changed", username, role)
	if generated {
		fmt.Printf(": %s", password)
	}
	fmt.Println()
	if disabled {
		fmt.Println("note: this account is disabled; another administrator can enable it in Users")
	}
	return nil
}

// newPassword returns 16 random characters without look-alikes.
func newPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	var sb strings.Builder
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		sb.WriteByte(alphabet[n.Int64()])
	}
	return sb.String()
}

func runBackup(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("out", filepath.Join(cfg.DataDir, "backups"), "directory for the backup")
	_ = fs.Parse(args)
	tool, err := app.PGTool(cfg, "pg_dump")
	if err != nil {
		return err
	}
	db, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	b := &backup.Backups{Dir: *out, PGDump: tool, URL: db.URL}
	info, err := b.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%d bytes)\n", filepath.Join(*out, info.Name), info.Size)
	fmt.Println("photos and maps are files in", cfg.DataDir, "- copy that folder as well for a full backup")
	return nil
}

// restore replaces the database with a backup. The service must be stopped: it would keep
// writing to the database being replaced.
func restore(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	yes := fs.Bool("yes", false, "confirm: the current database is replaced")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: netra restore -yes <file.dump>")
	}
	file := fs.Arg(0)
	if _, err := os.Stat(file); err != nil {
		return err
	}
	if !*yes {
		return errors.New("this replaces every record of the current database with the backup's; add -yes to go on")
	}
	tool, err := app.PGTool(cfg, "pg_restore")
	if err != nil {
		return err
	}
	db, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	cmd, err := backup.Command(ctx, tool, db.URL, "--clean", "--if-exists", "--no-owner", "--exit-on-error", file)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	fmt.Printf("restored %s in %s; start the service\n", file, time.Since(start).Round(time.Second))
	return nil
}

func checkLicense(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: netra license <file.lic>")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	keys, err := license.TrustedKeys()
	if err != nil {
		return err
	}
	l, err := license.Verify(string(raw), keys)
	if err != nil {
		return err
	}
	fmt.Printf("client %s, %s/%s, map %s, robots %d, modules %s\n", l.Client, l.Edition, l.Tier, l.MapMode(),
		l.MaxRobots, strings.Join(l.Features, " "))
	fmt.Printf("issued %s, status %s\n", l.IssuedAt.Format(time.DateOnly), l.StatusAt(time.Now()))
	if l.Installation == "" {
		fmt.Println("not bound: works on any server")
		return nil
	}
	db, err := app.OpenDatabase(ctx, cfg)
	if err != nil {
		fmt.Println("bound to", l.Installation, "(database unreachable, cannot compare)")
		return nil
	}
	defer db.Close()
	here, err := db.Store.Setting(ctx, store.SettingInstallation)
	if err != nil {
		return err
	}
	if license.NormalizeInstallation(l.Installation) != license.NormalizeInstallation(here) {
		return fmt.Errorf("bound to %s, but this server is %s", l.Installation, here)
	}
	fmt.Println("bound to this server:", here)
	return nil
}
