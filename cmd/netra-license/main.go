// Command netra-license makes the vendor's signing key and issues the licenses netra-api accepts.
//
//	netra-license keygen  -out ~/netra-keys/ezra            # ezra.key (private) + ezra.pub
//	netra-license issue   -key ~/netra-keys/ezra.key -client "PLN UPT Bekasi" \
//	                      -edition substation -tier pro -robots 3 -days 365 \
//	                      -installation NETRA-7KQ3-M9XD-2WPA \
//	                      -name "NETRA Gardu" -color "#0f766e" -logo logo.svg -out bekasi.lic
//	netra-license inspect bekasi.lic                        # payload, signature check, status
//	netra-license presets                                   # editions, tiers and feature modules
//
// Run it on the machine that keeps the private key, never on a server.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "issue":
		err = issue(os.Args[2:], os.Stdout)
	case "inspect":
		err = inspect(os.Args[2:], os.Stdout)
	case "presets":
		presets(os.Stdout)
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "netra-license:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: netra-license <command> [flags]

  keygen   make a signing key pair: <out>.key (private, keep offline) and <out>.pub
  issue    sign a license for a client
  inspect  show a license, check its signature and status
  presets  list editions, tiers and feature modules

netra-license <command> -h shows the flags.`)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "path without extension for <out>.key and <out>.pub (required)")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("keygen: -out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	// O_EXCL: never overwrite a key that may already have signed licenses.
	keyFile, err := os.OpenFile(*out+".key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("keygen: %w", err)
	}
	if _, err := fmt.Fprintln(keyFile, base64.StdEncoding.EncodeToString(priv)); err != nil {
		_ = keyFile.Close()
		return err
	}
	if err := keyFile.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(*out+".pub", []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil { // #nosec G306 -- a public key
		return err
	}
	fmt.Printf("private key: %s.key (keep it offline, with a backup; never commit it)\n", *out)
	fmt.Printf("public key:  %s.pub (copy it to internal/license/keys/ in netra-fleet-be)\n", *out)
	return nil
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

var (
	hexColor       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	installationID = regexp.MustCompile(`^NETRA-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$`)
)

func issue(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("issue", flag.ExitOnError)
	keyPath := fs.String("key", "", "private key file from keygen (required)")
	client := fs.String("client", "", "client name, shown in the dashboard and reports (required)")
	edition := fs.String("edition", "custom", "edition: "+names(license.Editions))
	tier := fs.String("tier", "basic", "tier: "+names(license.Tiers))
	mapMode := fs.String("map", "", "map mode: "+strings.Join(license.MapModes, ", ")+" (default: the edition's)")
	installation := fs.String("installation", "", "bind to this installation ID (NETRA-XXXX-XXXX-XXXX, shown by the server); empty: any server")
	robots := fs.Int("robots", 0, "maximum number of robots, 0 for no limit")
	days := fs.Int("days", 365, "days of validity, 0 for no expiry")
	id := fs.String("id", "", "license id (default: random)")
	name := fs.String("name", "", "application name (branding), e.g. \"NETRA Gardu\"")
	color := fs.String("color", "", "primary colour #rrggbb (branding)")
	colorDark := fs.String("color-dark", "", "primary colour in the dark theme (default: -color)")
	logo := fs.String("logo", "", "logo file, SVG or PNG up to 256 KB (branding)")
	out := fs.String("out", "", "write the license to this file (default: stdout)")
	var add, remove listFlag
	fs.Var(&add, "add", "extra feature modules, comma-separated")
	fs.Var(&remove, "remove", "feature modules to leave out, comma-separated")
	_ = fs.Parse(args)

	if *keyPath == "" || *client == "" {
		return errors.New("issue: -key and -client are required")
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	priv, err := license.ParsePrivateKey(string(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", *keyPath, err)
	}
	features, err := license.Features(*edition, *tier, add, remove)
	if err != nil {
		return err
	}
	if *robots < 0 || *days < 0 {
		return errors.New("issue: -robots and -days cannot be negative")
	}
	for _, c := range []string{*color, *colorDark} {
		if c != "" && !hexColor.MatchString(c) {
			return fmt.Errorf("issue: colour %q is not #rrggbb", c)
		}
	}
	if *colorDark == "" {
		*colorDark = *color
	}
	if *mapMode == "" {
		*mapMode = license.DefaultMap(*edition)
	}
	if !license.ValidMap(*mapMode) {
		return fmt.Errorf("issue: map %q is not one of %s", *mapMode, strings.Join(license.MapModes, ", "))
	}
	bound := license.NormalizeInstallation(*installation)
	if bound != "" && !installationID.MatchString(bound) {
		return fmt.Errorf("issue: installation %q is not NETRA-XXXX-XXXX-XXXX", *installation)
	}
	l := license.License{
		Product: license.Product, ID: *id, Client: *client, Edition: *edition, Tier: *tier, Features: features,
		Map: *mapMode, MaxRobots: *robots, Installation: bound, IssuedAt: time.Now().UTC().Truncate(time.Second),
		Branding: license.Branding{Name: *name, Color: *color, ColorDark: *colorDark},
	}
	if l.ID == "" {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		l.ID = l.IssuedAt.Format("20060102") + "-" + hex.EncodeToString(b)
	}
	if *days > 0 {
		l.ExpiresAt = l.IssuedAt.AddDate(0, 0, *days)
	}
	if *logo != "" {
		if l.Branding.Logo, err = dataURL(*logo); err != nil {
			return err
		}
	}
	text, err := license.Sign(l, priv)
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = fmt.Fprintln(stdout, text)
		return err
	}
	if err := os.WriteFile(*out, []byte(text+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: %s, %s/%s, map %s, %s\n", *out, l.Client, l.Edition, l.Tier, l.Map, strings.Join(l.Features, " "))
	if l.Installation != "" {
		fmt.Fprintf(stdout, "bound to %s\n", l.Installation)
	}
	return nil
}

// dataURL embeds a small SVG or PNG logo in the license.
func dataURL(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > 256<<10 {
		return "", fmt.Errorf("%s: logo larger than 256 KB", path)
	}
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if i := strings.Index(typ, ";"); i >= 0 {
		typ = typ[:i]
	}
	if typ != "image/svg+xml" && typ != "image/png" {
		return "", fmt.Errorf("%s: logo must be .svg or .png", path)
	}
	return "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func inspect(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	pubPath := fs.String("pub", "", "also trust this public key file (default: the keys built in)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("inspect: give one license file (or - for stdin)")
	}
	var raw []byte
	var err error
	if fs.Arg(0) == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		return err
	}
	keys, err := license.TrustedKeys()
	if err != nil {
		return err
	}
	if *pubPath != "" {
		pub, err := os.ReadFile(*pubPath)
		if err != nil {
			return err
		}
		k, err := license.ParsePublicKey(string(pub))
		if err != nil {
			return fmt.Errorf("%s: %w", *pubPath, err)
		}
		keys = append(keys, k)
	}
	l, err := license.Verify(string(raw), keys)
	if err != nil {
		return err
	}
	if len(l.Branding.Logo) > 60 {
		l.Branding.Logo = l.Branding.Logo[:60] + "…"
	}
	out, _ := json.MarshalIndent(l, "", "  ")
	fmt.Fprintf(stdout, "%s\nsignature: trusted\nstatus:    %s\n", out, l.StatusAt(time.Now()))
	return nil
}

func presets(w io.Writer) {
	fmt.Fprintln(w, "Core (always on): robots, live map, commands, routes, patrols, schedules, floor plans, users, patrol reports")
	fmt.Fprintln(w, "\nFeature modules:")
	for _, c := range license.Catalog {
		fmt.Fprintf(w, "  %-16s %s\n", c.ID, c.Description)
	}
	fmt.Fprintln(w, "\nEditions (product line):")
	printSets(w, license.Editions)
	fmt.Fprintln(w, "\nTiers (premium modules):")
	printSets(w, license.Tiers)
	fmt.Fprintln(w, "\nMap modes (fixed by the license; -map):")
	fmt.Fprintln(w, "  floorplan    indoor floor plan (SVG, PNG, JPG, PDF) calibrated to the robot map")
	fmt.Fprintln(w, "  pgm          the robot's own map (map.yaml + map.pgm), aligned by its origin")
	fmt.Fprintln(w, "  gps          outdoor basemap in latitude and longitude")
	fmt.Fprintln(w, "  default per edition: mall floorplan, substation gps, custom floorplan")
}

func printSets(w io.Writer, sets map[string][]string) {
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.Join(sets[k], " ")
		if v == "" {
			v = "(core only)"
		}
		fmt.Fprintf(w, "  %-12s %s\n", k, v)
	}
}

func names(sets map[string][]string) string {
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
