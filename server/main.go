package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func projectRoot() string {
	if v := os.Getenv("DROIDTV_ROOT"); v != "" {
		return v
	}
	cwd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(cwd, "client")); err == nil {
		return cwd
	}
	return filepath.Dir(cwd)
}
func readVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}
func serverPort(cfg Config) int {
	for _, k := range []string{"SERVER_PORT", "PORT"} {
		if v := os.Getenv(k); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
				return n
			}
		}
	}
	if cfg.ServerPort > 0 && cfg.ServerPort < 65536 {
		return cfg.ServerPort
	}
	return 7503
}

func main() {
	root := projectRoot()
	s, err := NewServer(root, readVersion(root))
	if err != nil {
		log.Fatal(err)
	}
	port := serverPort(s.config)
	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 75 * time.Second}
	go func() {
		log.Printf("Starting droidtv-remote %s on http://0.0.0.0:%d (MCP: /mcp)", s.version, port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	s.mu.RLock()
	ids := append([]string(nil), s.tvOrder...)
	s.mu.RUnlock()
	for _, id := range ids {
		s.disconnect(id)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdown)
}
