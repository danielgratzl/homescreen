// homescreen — an iOS home-screen style dashboard for a NAS.
//
// Configuration is read from a YAML file on every request, so edits apply on
// browser refresh. Icons are resolved by naming convention:
//
//	<config dir>/icons/<slug>.{svg,png,webp,jpg,jpeg}   (user overrides)
//	built-in icons/<slug>.svg                            (embedded)
//	generated letter icon                                (fallback)
package main

import (
	"bytes"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

//go:embed web
var webFS embed.FS

//go:embed icons
var iconFS embed.FS

//go:embed config.example.yaml
var sampleConfig []byte

var (
	configPath = envOr("CONFIG_PATH", "/config/config.yaml")
	port       = envOr("PORT", "8080")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func configDir() string { return filepath.Dir(configPath) }

// ---------------------------------------------------------------------------
// Config model
// ---------------------------------------------------------------------------

type App struct {
	Name     string `yaml:"name" json:"name"`
	URL      string `yaml:"url" json:"url"`
	Icon     string `yaml:"icon" json:"-"`
	CheckURL string `yaml:"check_url" json:"-"`
	Check    *bool  `yaml:"check" json:"-"`
	NewTab   *bool  `yaml:"new_tab" json:"-"`

	// Computed fields sent to the browser.
	ID       string `yaml:"-" json:"id"`
	IconSrc  string `yaml:"-" json:"icon"`
	Fallback string `yaml:"-" json:"fallback"`
	Target   string `yaml:"-" json:"target"`
	Checked  bool   `yaml:"-" json:"checked"`
}

type Page struct {
	Name string `yaml:"name" json:"name"`
	Apps []*App `yaml:"apps" json:"apps"`
}

type Config struct {
	Title         string  `yaml:"title" json:"title"`
	Wallpaper     string  `yaml:"wallpaper" json:"-"`
	Clock         *bool   `yaml:"clock" json:"-"`
	Columns       int     `yaml:"columns" json:"columns"`
	NewTab        *bool   `yaml:"new_tab" json:"-"`
	StatusChecks  *bool   `yaml:"status_checks" json:"-"`
	CheckInterval int     `yaml:"check_interval" json:"checkInterval"`
	Apps          []*App  `yaml:"apps" json:"-"`
	Pages         []*Page `yaml:"pages" json:"pages"`
	Dock          []*App  `yaml:"dock" json:"dock"`

	// Computed fields sent to the browser.
	WallpaperOut Wallpaper `yaml:"-" json:"wallpaper"`
	ClockOut     bool      `yaml:"-" json:"clock"`
	StatusOut    bool      `yaml:"-" json:"statusChecks"`
	Warning      string    `yaml:"-" json:"warning,omitempty"`
}

type Wallpaper struct {
	Type  string `json:"type"` // "preset" or "image"
	Value string `json:"value"`
}

var wallpaperPresets = map[string]bool{
	"aurora": true, "sunset": true, "ocean": true, "forest": true, "graphite": true, "dusk": true,
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// loadConfig reads and normalizes the YAML config. A missing file yields the
// embedded sample config plus a warning; a malformed file yields an error.
func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath)
	warning := ""
	if errors.Is(err, fs.ErrNotExist) {
		data = sampleConfig
		warning = fmt.Sprintf("No config found at %s — showing the sample config. Mount your own config.yaml to customize.", configPath)
	} else if err != nil {
		return nil, fmt.Errorf("reading %s: %w", configPath, err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // catch typos like "wallpapper:"
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}
	cfg.Warning = warning
	normalize(&cfg)
	return &cfg, nil
}

func normalize(cfg *Config) {
	if cfg.Title == "" {
		cfg.Title = "Home"
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 30
	}
	if cfg.CheckInterval < 5 {
		cfg.CheckInterval = 5
	}
	cfg.ClockOut = boolOr(cfg.Clock, true)
	cfg.StatusOut = boolOr(cfg.StatusChecks, true)
	cfg.WallpaperOut = resolveWallpaper(cfg.Wallpaper)

	// Top-level "apps:" is shorthand for a single first page.
	if len(cfg.Apps) > 0 {
		cfg.Pages = append([]*Page{{Name: "", Apps: cfg.Apps}}, cfg.Pages...)
		cfg.Apps = nil
	}
	if cfg.Pages == nil {
		cfg.Pages = []*Page{}
	}
	if cfg.Dock == nil {
		cfg.Dock = []*App{}
	}

	newTab := boolOr(cfg.NewTab, true)
	prep := func(a *App, id string) {
		a.ID = id
		if a.Name == "" {
			a.Name = "Untitled"
		}
		a.Fallback = "icons/" + safeSlug(a.Name)
		switch {
		case strings.HasPrefix(a.Icon, "http://"), strings.HasPrefix(a.Icon, "https://"):
			a.IconSrc = a.Icon
		case a.Icon != "":
			a.IconSrc = "icons/" + safeSlug(a.Icon)
		default:
			a.IconSrc = a.Fallback
		}
		if boolOr(a.NewTab, newTab) {
			a.Target = "_blank"
		}
		a.Checked = cfg.StatusOut && boolOr(a.Check, true) && (a.CheckURL != "" || a.URL != "")
	}
	for pi, p := range cfg.Pages {
		if p.Apps == nil {
			p.Apps = []*App{}
		}
		for ai, a := range p.Apps {
			prep(a, fmt.Sprintf("p%d-%d", pi, ai))
		}
	}
	for ai, a := range cfg.Dock {
		prep(a, fmt.Sprintf("d%d", ai))
	}
}

func (cfg *Config) allApps() []*App {
	var out []*App
	for _, p := range cfg.Pages {
		out = append(out, p.Apps...)
	}
	return append(out, cfg.Dock...)
}

var imageFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(jpe?g|png|webp|avif|gif)$`)

func resolveWallpaper(w string) Wallpaper {
	w = strings.TrimSpace(w)
	switch {
	case w == "":
		return Wallpaper{"preset", "aurora"}
	case wallpaperPresets[strings.ToLower(w)]:
		return Wallpaper{"preset", strings.ToLower(w)}
	case strings.HasPrefix(w, "http://"), strings.HasPrefix(w, "https://"):
		return Wallpaper{"image", w}
	case imageFileRe.MatchString(w):
		return Wallpaper{"image", "wallpaper/" + w}
	}
	log.Printf("unknown wallpaper %q — falling back to aurora", w)
	return Wallpaper{"preset", "aurora"}
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

var (
	slugStrip = regexp.MustCompile(`[^a-z0-9-]+`)
	slugDash  = regexp.MustCompile(`-{2,}`)
	slugValid = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// slugify: "Calibre Web" -> "calibre-web", "SABnzbd+" -> "sabnzbd".
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "-", "_", "-", ".", "-").Replace(s)
	s = slugStrip.ReplaceAllString(s, "")
	s = slugDash.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func safeSlug(s string) string {
	if out := slugify(s); out != "" {
		return out
	}
	return "app"
}

// Common alternate names that should map to a built-in icon.
var iconAliases = map[string]string{
	"calibre-web":     "calibre",
	"calibreweb":      "calibre",
	"abs":             "audiobookshelf",
	"audio-bookshelf": "audiobookshelf",
	"sab":             "sabnzbd",
	"sabnzbd-plus":    "sabnzbd",
	"liddar":          "lidarr",
}

var iconExts = []string{".svg", ".png", ".webp", ".jpg", ".jpeg"}

func handleIcon(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSuffix(r.PathValue("name"), filepath.Ext(r.PathValue("name")))
	if !slugValid.MatchString(slug) || len(slug) > 64 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// User-supplied SVGs could contain scripts; neuter them if opened directly.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")

	candidates := []string{slug}
	if alias, ok := iconAliases[slug]; ok {
		candidates = append(candidates, alias)
	}

	// 1. User icons in <config dir>/icons
	userDir := filepath.Join(configDir(), "icons")
	for _, c := range candidates {
		for _, ext := range iconExts {
			p := filepath.Join(userDir, c+ext)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				http.ServeFile(w, r, p)
				return
			}
		}
	}
	// 2. Built-in icons
	for _, c := range candidates {
		if data, err := iconFS.ReadFile("icons/" + c + ".svg"); err == nil {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write(data)
			return
		}
	}
	// 3. Generated letter icon
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Write(letterIcon(slug))
}

func letterIcon(slug string) []byte {
	h := fnv.New32a()
	h.Write([]byte(slug))
	hue := h.Sum32() % 360
	letter := "?"
	for _, r := range slug {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letter = strings.ToUpper(string(r))
			break
		}
	}
	return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1">
<stop offset="0" stop-color="hsl(%d,72%%,62%%)"/><stop offset="1" stop-color="hsl(%d,68%%,42%%)"/>
</linearGradient></defs>
<rect width="100" height="100" fill="url(#g)"/>
<text x="50" y="50" dy=".35em" text-anchor="middle" font-family="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif" font-size="52" font-weight="600" fill="#fff">%s</text>
</svg>`, hue, (hue+25)%360, letter))
}

// ---------------------------------------------------------------------------
// Status checks
// ---------------------------------------------------------------------------

var checkClient = &http.Client{
	Timeout: 4 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // self-signed certs are the norm on a NAS
		MaxIdleConnsPerHost: 2,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type cached struct {
	up bool
	at time.Time
}

var (
	statusMu    sync.Mutex
	statusCache = map[string]cached{}
)

// isUp treats any HTTP response except 502/503/504 (including 401/403 from
// apps with auth on) as "up". Connection failures and timeouts are "down".
func isUp(url string) bool {
	statusMu.Lock()
	if c, ok := statusCache[url]; ok && time.Since(c.at) < 10*time.Second {
		statusMu.Unlock()
		return c.up
	}
	statusMu.Unlock()

	up := false
	if req, err := http.NewRequest(http.MethodGet, url, nil); err == nil {
		req.Header.Set("User-Agent", "homescreen-dashboard/1.0")
		if resp, err := checkClient.Do(req); err == nil {
			resp.Body.Close()
			// 502/503/504 usually means a reverse proxy whose backend is down.
			switch resp.StatusCode {
			case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			default:
				up = true
			}
		}
	}

	statusMu.Lock()
	statusCache[url] = cached{up, time.Now()}
	statusMu.Unlock()
	return up
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfig()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	result := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range cfg.allApps() {
		if !a.Checked {
			continue
		}
		target := a.CheckURL
		if target == "" {
			target = a.URL
		}
		wg.Add(1)
		go func(id, target string) {
			defer wg.Done()
			up := isUp(target)
			mu.Lock()
			result[id] = up
			mu.Unlock()
		}(a.ID, target)
	}
	wg.Wait()
	writeJSON(w, result)
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfig()
	if err != nil {
		log.Printf("config error: %v", err)
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, cfg)
}

func handleWallpaper(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !imageFileRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(configDir(), name)
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, p)
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local server and exit (for Docker HEALTHCHECK)")
	flag.Parse()

	if *healthcheck {
		resp, err := http.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(static))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /api/config", handleConfig)
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /icons/{name}", handleIcon)
	mux.HandleFunc("GET /wallpaper/{name}", handleWallpaper)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})

	if _, err := loadConfig(); err != nil {
		log.Printf("warning: %v", err)
	}
	log.Printf("homescreen listening on :%s (config: %s)", port, configPath)
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
