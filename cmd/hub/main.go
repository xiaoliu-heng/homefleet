package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/server"
	"github.com/xiaoliu-heng/homefleet/internal/store"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	listen := flag.String("listen", env("HOMEFLEET_LISTEN", "127.0.0.1:8080"), "HTTP listen address")
	db := flag.String("db", env("HOMEFLEET_DB", "data/homefleet.db"), "SQLite file")
	web := flag.String("web", env("HOMEFLEET_WEB", "web/dist"), "built web directory")
	publicURL := flag.String("public-url", env("HOMEFLEET_PUBLIC_URL", "http://127.0.0.1:8080"), "browser-visible URL")
	dev := flag.Bool("dev", false, "allow HTTP cookies; loopback listener only")
	backup := flag.String("backup", "", "create a consistent database backup and exit")
	reset := flag.Bool("reset-password", false, "replace password from HOMEFLEET_ADMIN_PASSWORD and exit")
	flag.Parse()
	if *dev {
		h, _, e := net.SplitHostPort(*listen)
		if e != nil || (h != "127.0.0.1" && h != "::1") {
			log.Fatal("-dev requires loopback listen address")
		}
	}
	s, e := store.Open(*db)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	if *backup != "" {
		if e = s.Backup(*backup); e != nil {
			log.Fatal(e)
		}
		fmt.Println("Database backup complete; preserve master.key separately.")
		return
	}
	if s.Meta("password") == "" || *reset {
		if e = setAdminPassword(s, os.Getenv("HOMEFLEET_ADMIN_PASSWORD")); e != nil {
			log.Fatal(e)
		}
	}
	if *reset {
		return
	}
	app := server.New(s, *publicURL, *web, *dev)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.Background(ctx)
	srv := &http.Server{Addr: *listen, Handler: app, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	log.Printf("HomeFleet listening on %s", *listen)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
