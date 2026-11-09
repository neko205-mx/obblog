package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"obblog/internal/web"
)

type startOptions struct {
	Vault, Data, Addr                 string
	Writable, Git, SecureCookie, Test bool
}

func main() {
	options := startOptions{}
	flag.StringVar(&options.Vault, "vault", "", "Legacy vault directory; in test mode, preselect a read-only import source")
	flag.StringVar(&options.Data, "data", "./data", "Persistent installation/configuration directory")
	flag.StringVar(&options.Addr, "addr", "127.0.0.1:8080", "HTTP listen address")
	flag.BoolVar(&options.Writable, "write", true, "Legacy vault mode: enable Markdown editing (enabled by default)")
	flag.BoolVar(&options.Git, "git", true, "Legacy vault mode: enable Git sync when the directory is a checkout (enabled by default)")
	flag.BoolVar(&options.SecureCookie, "secure-cookie", false, "Legacy vault mode: force secure cookies (HTTPS is detected automatically)")
	flag.BoolVar(&options.Test, "test", false, "Use a disposable test installation and remove all its data on shutdown")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, options); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(parent context.Context, options startOptions) (result error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var handler http.Handler
	if options.Vault != "" && !options.Test {
		app, err := web.NewWithOptions(options.Vault, web.Options{Writable: options.Writable, Password: os.Getenv("OBBLOG_ADMIN_PASSWORD"), SecureCookie: options.SecureCookie, Git: options.Git, AutoGit: true, DataDir: options.Data})
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, app.Close()) }()
		handler = app.Handler()
		log.Printf("Legacy vault mode (%s). Start without -vault to use persistent web installation.", app.VaultPath())
	} else {
		var runtime *web.Runtime
		var err error
		if options.Test {
			runtime, err = web.OpenTestRuntime(options.Vault)
		} else {
			runtime, err = web.OpenRuntime(options.Data)
		}
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, runtime.Close()) }()
		handler = runtime.Handler()
		if options.Test {
			log.Print("测试模式")
			go func() {
				select {
				case <-runtime.Done():
					cancel()
				case <-ctx.Done():
				}
			}()
		}
	}
	server := &http.Server{
		Addr: options.Addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	var shutdownErr error
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			shutdownErr = errors.Join(err, server.Close())
		}
	}()
	log.Printf("OB Vault: http://%s/ (admin: /admin/; installer: /install)", options.Addr)
	serveErr := server.ListenAndServe()
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	cancel()
	<-shutdownDone
	if serveErr != nil {
		serveErr = fmt.Errorf("serve: %w", serveErr)
	}
	return errors.Join(serveErr, shutdownErr)
}
