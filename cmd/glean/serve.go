package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/navingamage/glean/internal/store"
	"github.com/navingamage/glean/internal/web"
)

func cmdServe(ctx context.Context, g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("serve")
	addr := fs.String("addr", "127.0.0.1:8477", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}

	srv, err := web.New(st)
	if err != nil {
		return err
	}
	// Localhost by default and deliberately: the profile is a list of every bank
	// and shop a person has used, which is exactly the sort of thing that should
	// not be reachable from the network without someone deciding it should.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("glean dashboard on http://%s\n", ln.Addr())
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
