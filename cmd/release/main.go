// Generate the release manifest and immutable versioned Agent artifacts.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"log"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir := flag.String("dir", "dist/releases", "release directory")
	version := flag.String("version", model.Version, "published X.Y.Z version")
	flag.Parse()
	if !model.ValidVersion(*version) {
		log.Fatal("invalid version")
	}
	r := model.AgentRelease{Version: *version, MinimumVersion: model.MinimumUpdaterVersion, PublishedAt: time.Now().UTC(), Artifacts: map[string]model.AgentArtifact{}}
	versionDir := filepath.Join(*dir, "agents", *version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		log.Fatal(err)
	}
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "windows-amd64"} {
		name := model.AgentFilename(platform)
		raw, err := os.ReadFile(filepath.Join(*dir, name))
		if err != nil {
			log.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		path := filepath.Join(versionDir, name)
		if previous, err := os.ReadFile(path); err == nil && fmt.Sprintf("%x", sha256.Sum256(previous)) != hash {
			log.Fatalf("version %s already has different bytes; publish a new version", *version)
		}
		if err = os.WriteFile(path, raw, 0755); err != nil {
			log.Fatal(err)
		}
		r.Artifacts[platform] = model.AgentArtifact{Name: name, SHA256: hash, Size: int64(len(raw))}
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(*dir, "agent-release.json"), append(raw, '\n'), 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Published Agent manifest", r.Version)
}
