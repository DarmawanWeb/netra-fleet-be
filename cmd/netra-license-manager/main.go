// Command netra-license-manager is EZRA Robotics' web tool to issue NETRA licenses: clients and
// their deployments, a passphrase-protected signing key, issuing, renewing and checking licenses,
// and an audit log. It is vendor-only: never shipped to clients and never part of an image.
//
//	netra-license-manager -data ~/netra-licenses -addr 127.0.0.1:8099
//
// The data folder holds everything as JSON files (the signing key encrypted), so a backup is a
// copy of the folder. See docs/license-manager.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/licensemgr"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

func main() {
	home, _ := os.UserHomeDir()
	data := flag.String("data", filepath.Join(home, "netra-licenses"), "data folder: clients, licenses, operators, the encrypted signing key, the audit log")
	addr := flag.String("addr", "127.0.0.1:8099", "listen address; keep it on this machine unless it is served over TLS")
	certFile := flag.String("tls-cert", "", "TLS certificate (PEM): serve HTTPS, with -tls-key")
	keyFile := flag.String("tls-key", "", "TLS private key (PEM) of -tls-cert")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}
	if (*certFile == "") != (*keyFile == "") {
		fmt.Fprintln(os.Stderr, "netra-license-manager: -tls-cert and -tls-key go together")
		os.Exit(2)
	}
	if err := run(expandHome(*data, home), *addr, *certFile, *keyFile); err != nil {
		slog.Error("netra-license-manager", "err", err)
		os.Exit(1)
	}
}

func run(dir, addr, certFile, keyFile string) error {
	srv, err := licensemgr.New(dir)
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }() // locks the signing key
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute,
		WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	scheme := "http"
	if certFile != "" {
		scheme = "https"
	}
	slog.Info("NETRA License Manager", "url", scheme+"://"+browserAddr(ln.Addr()), "data", dir, "version", version.String())
	if !loopback(ln.Addr()) && certFile == "" {
		slog.Warn("listening beyond this machine without TLS: passwords and sessions travel in clear; use -tls-cert and -tls-key")
	}
	if certFile != "" {
		err = httpServer.ServeTLS(ln, certFile, keyFile)
	} else {
		err = httpServer.Serve(ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// expandHome expands a leading ~ the shell left alone (as in -data=~/netra-licenses).
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok && home != "" {
		return filepath.Join(home, rest)
	}
	return path
}

func loopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// browserAddr is the address to open: localhost for a server on every interface.
func browserAddr(a net.Addr) string {
	tcp, ok := a.(*net.TCPAddr)
	if !ok || tcp.IP.IsUnspecified() {
		if ok {
			return fmt.Sprintf("localhost:%d", tcp.Port)
		}
		return a.String()
	}
	return a.String()
}
