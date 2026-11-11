package clientsetup

import (
	"bytes"
	_ "embed"
	"encoding/pem"
	"html/template"
	"net"
	"net/http"
	"strings"
)

//go:embed setup.html
var page string

var setupTemplate = template.Must(template.New("client-setup").Parse(page))

func Render(profile Profile) ([]byte, error) {
	var out bytes.Buffer
	err := setupTemplate.Execute(&out, profile)
	return out.Bytes(), err
}

// Handler exposes only public setup material. The router admits LAN sources on
// its web listener; the Host check here confines this exception to the exact
// site address. It never dispatches to a Hub identity or management route.
func Handler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		profile, err := loadPublic(root)
		if err != nil {
			http.Error(w, "Client trust is unavailable. On the server run: stackkit verify client-trust --export. For a public domain use the printed HTTPS Hub address.", http.StatusServiceUnavailable)
			return
		}
		host := strings.Trim(r.Host, "[]")
		if name, _, err := net.SplitHostPort(r.Host); err == nil {
			host = name
		}
		if host != profile.Address && !(host == "base."+profile.Domain && r.Header.Get("Stackkit-Access-Listener") == "lan") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		switch r.URL.Path {
		case Path, Path + "/":
			raw, err := Render(profile)
			if err != nil {
				http.Error(w, "Cannot render client setup", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(raw)
		case Path + "/root.cer":
			block, _ := pem.Decode([]byte(profile.RootPEM))
			w.Header().Set("Content-Type", "application/pkix-cert")
			w.Header().Set("Content-Disposition", `attachment; filename="root.cer"`)
			_, _ = w.Write(block.Bytes)
		case Path + "/enroll-linux.sh":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="enroll-linux.sh"`)
			_, _ = w.Write([]byte(profile.Linux))
		default:
			http.NotFound(w, r)
		}
	})
}
