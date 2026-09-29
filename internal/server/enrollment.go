package server

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type enrollmentAddress struct {
	URL        string `json:"url"`
	Label      string `json:"label"`
	Interface  string `json:"interface,omitempty"`
	RequiresCA bool   `json:"requires_ca"`
}

// The Hub can run in Docker. Offer configured HTTPS entrypoints instead of
// exposing container interfaces or assuming that every host IP serves HTTPS.
func (a *Server) enrollmentAddresses(w http.ResponseWriter, r *http.Request) {
	devices, err := a.Store.Devices()
	if err != nil {
		fail(w, 500, "无法读取控制台网卡信息")
		return
	}
	origins := make([]string, 0, len(a.origins))
	for origin := range a.origins {
		origins = append(origins, origin)
	}
	sort.Slice(origins, func(i, j int) bool {
		if (origins[i] == a.PublicURL) != (origins[j] == a.PublicURL) {
			return origins[i] == a.PublicURL
		}
		return origins[i] < origins[j]
	})
	choices := []enrollmentAddress{}
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" {
			continue
		}
		ip := net.ParseIP(u.Hostname())
		choice := enrollmentAddress{URL: origin, Label: "备用域名", RequiresCA: a.requiresCA}
		if ip != nil {
			choice.Label = "网卡地址"
			choice.RequiresCA = true
			if ip.IsPrivate() {
				choice.Label = "局域网"
			}
			// Interface names are informational; agents cannot add download origins.
			matches := []string{}
			for _, d := range devices {
				if d.Kind != "agent" || d.Revoked {
					continue
				}
				for _, addr := range d.Addresses {
					address := net.ParseIP(strings.SplitN(addr.Address, "/", 2)[0])
					if address != nil && address.Equal(ip) {
						matches = append(matches, addr.Interface)
					}
				}
			}
			if len(matches) == 1 {
				choice.Interface = matches[0]
				if strings.HasPrefix(strings.ToLower(choice.Interface), "tailscale") {
					choice.Label = "Tailscale"
				}
			}
			if choice.Interface != "" {
				choice.Label += " · " + choice.Interface
			}
		}
		if origin == a.PublicURL {
			choice.RequiresCA = a.requiresCA
			if ip == nil {
				choice.Label = "控制台域名（推荐）"
			} else {
				choice.Label += "（默认）"
			}
		}
		choices = append(choices, choice)
	}
	write(w, 200, map[string]any{"default_url": a.PublicURL, "addresses": choices})
}
