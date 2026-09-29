package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/agent"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("homefleet-agent enroll|run|inspect|user-worker [flags]")
		os.Exit(2)
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	config := flags.String("config", "agent.json", "agent configuration path")
	hub := flags.String("hub", "", "HTTPS hub URL")
	token := flags.String("token", os.Getenv("HOMEFLEET_ENROLL_TOKEN"), "single-use enrollment token")
	name := flags.String("name", "", "display name")
	runUser := flags.String("run-user", "", "normal project execution user")
	data := flags.String("data", "", "agent journal directory")
	ca := flags.String("ca", "", "CA certificate path")
	readOnly := flags.Bool("read-only", false, "collect metrics and inventory only")
	workerURL := flags.String("worker-url", "http://127.0.0.1:47691", "Windows user executor URL")
	workerToken := flags.String("worker-token-file", "", "local user executor credential")
	listen := flags.String("listen", "127.0.0.1:47691", "user executor listen address")
	updateID := flags.String("update-id", "", "internal update task ID")
	flags.Parse(os.Args[2:])
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch command {
	case "self-check", "mark-service", "apply-update":
		c, err := agent.LoadConfig(*config)
		if err != nil {
			log.Fatal("cannot read Agent configuration")
		}
		if c.Token == "" || c.DeviceID == "" || !filepath.IsAbs(c.DataDir) {
			log.Fatal("invalid enrolled Agent configuration")
		}
		if _, err = agent.NewClient(c); err != nil {
			log.Fatal("Agent HTTPS/CA configuration is invalid")
		}
		if command == "mark-service" {
			err = agent.MarkService(c)
		}
		if command == "apply-update" {
			err = agent.ApplyUpdate(c, *updateID)
		}
		if err != nil {
			log.Fatal(err)
		}
	case "user-worker":
		if err := agent.ServeWorker(ctx, *listen, *workerToken); err != nil {
			log.Fatal(err)
		}
	case "inspect":
		current, err := user.Current()
		if err != nil {
			log.Fatal(err)
		}
		if *runUser == "" {
			*runUser = current.Username
		}
		e, _ := agent.NewEngine(agent.Config{HubURL: "http://127.0.0.1:8080", RunUser: *runUser, ReadOnly: true})
		json.NewEncoder(os.Stdout).Encode(map[string]any{"device": e.Device(), "sample": agent.Collect(ctx)})
	case "enroll":
		if _, err := os.Stat(*config); err == nil {
			log.Fatal("config already exists; keep it to preserve the registered device identity")
		}
		if *hub == "" || *token == "" || *runUser == "" {
			log.Fatal("--hub, --token and --run-user are required")
		}
		path, err := filepath.Abs(*config)
		if err != nil {
			log.Fatal(err)
		}
		if *data == "" {
			*data = filepath.Join(filepath.Dir(path), "state")
		}
		*data, err = filepath.Abs(*data)
		if err != nil {
			log.Fatal(err)
		}
		c := agent.Config{HubURL: strings.TrimRight(*hub, "/"), Name: *name, RunUser: *runUser, DataDir: *data, CACert: *ca, ReadOnly: *readOnly, WorkerURL: *workerURL, WorkerTokenFile: *workerToken}
		engine, err := agent.NewEngine(c)
		if err != nil {
			log.Fatal(err)
		}
		device := engine.Device()
		if c.Name == "" {
			c.Name = device.Name
		}
		var registered struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		if _, err = engine.Client.Request(ctx, "POST", "/agent/v1/register", map[string]any{"token": *token, "device": device}, &registered); err != nil {
			log.Fatal(err)
		}
		c.DeviceID = registered.ID
		c.Token = registered.Token
		if err = agent.SaveJSON(path, c); err != nil {
			log.Fatalf("registration succeeded but configuration could not be saved: %v. Revoke this device before re-enrolling.", err)
		}
		fmt.Printf("Registered %s (%s). Configuration saved to %s\n", c.Name, c.DeviceID, path)
	case "run":
		c, err := agent.LoadConfig(*config)
		if err != nil {
			log.Fatal(err)
		}
		e, err := agent.NewEngine(c)
		if err != nil {
			log.Fatal(err)
		}
		if handled, err := runService(e); handled {
			if err != nil {
				log.Fatal(err)
			}
			return
		}
		if err = e.Run(ctx); err != nil {
			log.Fatal(err)
		}
	case "version":
		fmt.Println(model.Version)
	default:
		log.Fatalf("unknown command: %s", command)
	}
}
