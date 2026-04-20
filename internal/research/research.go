package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"coldflow/internal/domain"
	"golang.org/x/net/html"
)

type Source interface {
	Fetch(context.Context, string) (string, error)
	Discover(context.Context, string, []string) ([]string, error)
	Papers(context.Context, domain.Contact, string) ([]domain.Paper, error)
}
type Web struct {
	Client   *http.Client
	CacheDir string
	mu       sync.Mutex
	last     map[string]time.Time
}

// New disallows private-network targets both before connecting and after redirects.
func New(cache string) *Web {
	w := &Web{CacheDir: cache, last: map[string]time.Time{}}
	tr := &http.Transport{MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !Public(ip.IP) {
				return nil, errors.New("research URL resolves to a non-public address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("no address")
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	w.Client = &http.Client{Transport: tr, Timeout: 40 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return ValidateURL(r.URL.String())
	}}
	return w
}
func Public(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() && !strings.HasPrefix(ip.String(), "100.100.") && !inCGNAT(ip)
}
func inCGNAT(ip net.IP) bool {
	v := ip.To4()
	return v != nil && v[0] == 100 && v[1] >= 64 && v[1] <= 127
}
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("only HTTP(S) research URLs are supported")
	}
	if u.Hostname() == "" || u.User != nil {
		return errors.New("invalid URL")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !Public(ip) {
		return errors.New("private research URL rejected")
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return errors.New("research URLs require port 80 or 443")
	}
	return nil
}
func (w *Web) wait(ctx context.Context, host string) error {
	w.mu.Lock()
	next := w.last[host].Add(time.Second)
	if next.Before(time.Now()) {
		next = time.Now()
	}
	w.last[host] = next
	w.mu.Unlock()
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (w *Web) request(ctx context.Context, raw string, limit int64) ([]byte, string, error) {
	if err := ValidateURL(raw); err != nil {
		return nil, "", err
	}
	u, _ := url.Parse(raw)
	if err := w.wait(ctx, u.Host); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Coldflow/1.0 (academic research; respects robots.txt)")
	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("research source HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("source exceeds download limit")
	}
	return b, resp.Header.Get("Content-Type"), nil
}

// Allowed applies the longest matching Allow/Disallow rule for our bot or *.
func Allowed(robots, path string) bool {
	type rule struct {
		path  string
		allow bool
	}
	groups := map[string][]rule{}
	var agents []string
	hasRules := false
	for _, line := range strings.Split(robots, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "user-agent" {
			if hasRules {
				agents = nil
				hasRules = false
			}
			agents = append(agents, strings.ToLower(v))
			continue
		}
		if k != "allow" && k != "disallow" {
			continue
		}
		hasRules = true
		if v == "" {
			continue
		}
		for _, a := range agents {
			groups[a] = append(groups[a], rule{v, k == "allow"})
		}
	}
	rules, ok := groups["coldflow"]
	if !ok {
		rules = groups["*"]
	}
	best := -1
	allow := true
	for _, r := range rules {
		pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(r.path), `\*`, ".*")
		pattern = strings.TrimSuffix(pattern, `\$`)
		if strings.HasSuffix(r.path, "$") {
			pattern += "$"
		}
		match, _ := regexp.MatchString(pattern, path)
		if match && (len(r.path) > best || len(r.path) == best && r.allow) {
			best = len(r.path)
			allow = r.allow
		}
	}
	return allow
}
func (w *Web) robots(ctx context.Context, raw string) error {
	u, _ := url.Parse(raw)
	data, _, err := w.request(ctx, u.Scheme+"://"+u.Host+"/robots.txt", 256<<10)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return fmt.Errorf("robots.txt unavailable: %w", err)
	}
	if !Allowed(string(data), u.RequestURI()) {
		return errors.New("source disallows crawling via robots.txt")
	}
	return nil
}
func (w *Web) Fetch(ctx context.Context, raw string) (string, error) {
	if err := ValidateURL(raw); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(raw))
	path := filepath.Join(w.CacheDir, hex.EncodeToString(sum[:])+".txt")
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < 24*time.Hour && st.Size() < 200000 {
		b, err := os.ReadFile(path)
		return string(b), err
	}
	if err := w.robots(ctx, raw); err != nil {
		return "", err
	}
	b, typ, err := w.request(ctx, raw, 12<<20)
	if err != nil {
		return "", err
	}
	var text string
	if strings.Contains(typ, "pdf") || strings.HasPrefix(string(b[:min(5, len(b))]), "%PDF") {
		temp, err := os.CreateTemp("", "coldflow-*.pdf")
		if err != nil {
			return "", err
		}
		name := temp.Name()
		defer os.Remove(name)
		if _, err = temp.Write(b); err != nil {
			temp.Close()
			return "", err
		}
		temp.Close()
		child, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(child, "pdftotext", "-f", "1", "-l", "25", name, "-")
		out := &boundedWriter{max: 160000}
		cmd.Stdout = out
		if err = cmd.Run(); err != nil {
			return "", errors.New("PDF extraction failed; install pdftotext or use abstract evidence")
		}
		text = out.String()
	} else {
		doc, err := html.Parse(strings.NewReader(string(b)))
		if err != nil {
			return "", err
		}
		var buf strings.Builder
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "nav") {
				return
			}
			if n.Type == html.TextNode && buf.Len() < 150000 {
				buf.WriteString(strings.TrimSpace(n.Data))
				buf.WriteByte('\n')
			}
			if n.Type == html.ElementNode && n.Data == "a" {
				for _, a := range n.Attr {
					if a.Key == "href" && strings.HasPrefix(a.Val, "mailto:") {
						buf.WriteString(a.Val + "\n")
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		text = buf.String()
	}
	if len(text) > 150000 {
		text = text[:150000]
	}
	if err = os.MkdirAll(w.CacheDir, 0700); err == nil {
		_ = os.WriteFile(path, []byte(text), 0600)
	}
	return text, nil
}

type boundedWriter struct {
	strings.Builder
	max int
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("extracted text exceeds limit")
	}
	return b.Builder.Write(p)
}
func (w *Web) Discover(ctx context.Context, query string, seeds []string) ([]string, error) {
	if len(seeds) > 0 {
		return seeds[:min(10, len(seeds))], nil
	}
	key := os.Getenv("BRAVE_API_KEY")
	if key == "" {
		return nil, errors.New("provide seed URLs or BRAVE_API_KEY for web discovery")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.search.brave.com/res/v1/web/search?q="+url.QueryEscape(query+" professor university research email")+"&count=8", nil)
	req.Header.Set("X-Subscription-Token", key)
	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("web search HTTP %d", resp.StatusCode)
	}
	var v struct {
		Web struct {
			Results []struct {
				URL string `json:"url"`
			} `json:"results"`
		} `json:"web"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range v.Web.Results {
		out = append(out, r.URL)
	}
	return out, nil
}
func (w *Web) Papers(ctx context.Context, c domain.Contact, interests string) ([]domain.Paper, error) {
	endpoint := "https://api.openalex.org/works?search=" + url.QueryEscape(c.Name+" "+interests) + "&per-page=12"
	if key := os.Getenv("OPENALEX_API_KEY"); key != "" {
		endpoint += "&api_key=" + url.QueryEscape(key)
	}
	b, _, err := w.request(ctx, endpoint, 3<<20)
	if err != nil {
		return nil, err
	}
	var v struct {
		Results []struct {
			ID          string
			Title       string
			DOI         string
			Year        int              `json:"publication_year"`
			Abstract    map[string][]int `json:"abstract_inverted_index"`
			Authorships []struct {
				Author struct {
					Name string `json:"display_name"`
				}
				Institutions []struct {
					Name string `json:"display_name"`
				}
			}
			Location struct {
				URL string `json:"landing_page_url"`
				PDF string `json:"pdf_url"`
			} `json:"best_oa_location"`
		} `json:"results"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	out := []domain.Paper{}
	for _, r := range v.Results {
		authors := []string{}
		matched := false
		for _, a := range r.Authorships {
			authors = append(authors, a.Author.Name)
			if strings.EqualFold(strings.TrimSpace(a.Author.Name), strings.TrimSpace(c.Name)) {
				for _, i := range a.Institutions {
					if c.University == "" || strings.EqualFold(i.Name, c.University) {
						matched = true
					}
				}
			}
		}
		if !matched {
			continue
		}
		positions := map[int]string{}
		for word, ps := range r.Abstract {
			for _, p := range ps {
				positions[p] = word
			}
		}
		keys := make([]int, 0, len(positions))
		for k := range positions {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		words := []string{}
		for _, k := range keys {
			words = append(words, positions[k])
		}
		link := r.Location.URL
		if link == "" {
			link = r.DOI
		}
		if link == "" {
			link = r.ID
		}
		score := 0.0
		for _, term := range strings.Fields(strings.ToLower(interests)) {
			if strings.Contains(strings.ToLower(r.Title+" "+strings.Join(words, " ")), term) {
				score++
			}
		}
		out = append(out, domain.Paper{ID: domain.ID(), ContactID: c.ID, Title: r.Title, URL: link, PDFURL: r.Location.PDF, Abstract: strings.Join(words, " "), Authors: authors, Year: r.Year, Score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Year > out[j].Year
		}
		return out[i].Score > out[j].Score
	})
	return out, nil
}
func (w *Web) Prune() {
	entries, _ := os.ReadDir(w.CacheDir)
	type file struct {
		path string
		at   time.Time
	}
	var files []file
	for _, e := range entries {
		if st, err := e.Info(); err == nil && !e.IsDir() {
			files = append(files, file{filepath.Join(w.CacheDir, e.Name()), st.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.After(files[j].at) })
	for i, f := range files {
		if i >= 100 || time.Since(f.at) > 7*24*time.Hour {
			_ = os.Remove(f.path)
		}
	}
}
