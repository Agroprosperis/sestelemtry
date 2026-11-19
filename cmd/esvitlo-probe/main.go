// Command esvitlo-probe signs in to an e-svitlo cabinet with the
// company's account and keeps every page the cabinet answers, so the
// outages reader can be checked against the real cabinet before the API
// relies on it.
//
//	go run ./cmd/esvitlo-probe [-host vn.e-svitlo.com.ua] [-eic 62Z…]
//
// It asks for the login and, without echo, the password; neither is
// written down. The pages land in tmp/esvitlo-probe/<time>/, which git
// ignores: they are the company's own cabinet, so they stay local.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/nesh/sestelemetry/internal/outages"
)

// maxEICs bounds how many metering points one run reads outages for.
const maxEICs = 5

func main() {
	host := flag.String("host", "vn.e-svitlo.com.ua", "кабінет оператора")
	eicFlag := flag.String("eic", "", "EIC через кому; знайдені в кабінеті перевіряються й без цього")
	outRoot := flag.String("out", "tmp/esvitlo-probe", "куди складати сторінки")
	follow := flag.Int("follow", 8, "скільки розділів кабінету відкрити після входу")
	flag.Parse()

	if _, ok := outages.OperatorFor(*host); !ok {
		fail("невідомий кабінет %q", *host)
	}
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		fail("%v", err)
	}
	in := bufio.NewReader(os.Stdin)
	login := strings.TrimSpace(os.Getenv("ESVITLO_LOGIN"))
	if login == "" {
		login = ask(in, "Логін кабінету (email або телефон): ")
	}
	password := strings.TrimSpace(os.Getenv("ESVITLO_PASSWORD"))
	if password == "" {
		password = askHidden(in, "Пароль (не показується): ")
	}
	if login == "" || password == "" {
		fail("потрібні логін і пароль")
	}

	dir := filepath.Join(*outRoot, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail("%v", err)
	}
	rec := &recorder{dir: dir}
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: rec.redirect}
	c := outages.NewClient(outages.BaseURL(*host), login, password, loc, hc)
	c.SetTrace(rec.answer)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("Вхід у %s…\n", *host)
	signErr := c.SignIn(ctx)
	if signErr != nil {
		fmt.Printf("  не вийшло: %v\n", signErr)
	} else {
		fmt.Printf("  так, сторінка після входу: %s\n", rec.lastFinal())
	}

	// Look around either way: a sign-in the reader misjudged still shows
	// in what the cabinet's sections answer. No second sign-in, though —
	// a wrong password tried again may lock the account.
	opened := map[string]bool{}
	for _, p := range rec.finals() {
		opened[p] = true
	}
	candidates := append([]string{"/account_household"}, cabinetLinks(rec.lastBody(), *host)...)
	rec.links = candidates[1:]
	n := 0
	for _, p := range candidates {
		if n >= *follow || ctx.Err() != nil {
			break
		}
		if opened[p] || !worthOpening(p) {
			continue
		}
		opened[p] = true
		n++
		if _, final, err := c.Get(ctx, p); err != nil {
			fmt.Printf("  %s: %v\n", p, err)
		} else {
			fmt.Printf("  відкрито %s → %s\n", p, final.RequestURI())
		}
	}

	found := rec.eics()
	if len(found) > 0 {
		fmt.Printf("EIC у кабінеті: %s\n", strings.Join(found, ", "))
	} else {
		fmt.Println("EIC у кабінеті не знайдено")
	}
	points := pointsToRead(*eicFlag, found)
	switch {
	case signErr != nil:
		fmt.Println("Відключення не читаю: вхід не вдався")
	case len(points) == 0:
		fmt.Println("Відключення не читаю: немає EIC (можна вказати -eic 62Z…)")
	}
	if signErr == nil {
		for _, eic := range points {
			readOutages(ctx, c, eic, loc)
		}
	}

	if err := rec.save(); err != nil {
		fail("%v", err)
	}
	fmt.Printf("\nЗбережено відповідей: %d — %s\n", rec.n, dir)
	fmt.Println("Пароль ніде не записано. Сторінки лишаються на цьому компʼютері (tmp/ поза git).")
}

func readOutages(ctx context.Context, c *outages.Client, eic string, loc *time.Location) {
	list, err := c.Outages(ctx, eic)
	if err != nil {
		fmt.Printf("Відключення %s: %v\n", eic, err)
		return
	}
	if len(list) == 0 {
		fmt.Printf("Відключення %s: немає запланованих\n", eic)
		return
	}
	fmt.Printf("Відключення %s:\n", eic)
	for _, iv := range list {
		kind := "ГПВ"
		if iv.Kind == outages.KindOther {
			kind = "інше (роботи / аварія)"
		}
		fmt.Printf("  %s – %s  %s\n", iv.Start.In(loc).Format("02.01 15:04"), iv.End.In(loc).Format("02.01 15:04"), kind)
	}
}

// pointsToRead is the EICs given on the command line, then the metering
// points (62Z…) the cabinet showed, at most maxEICs.
func pointsToRead(flagValue string, found []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(eic string) {
		eic = outages.NormalizeEIC(eic)
		if eic == "" || seen[eic] || len(out) >= maxEICs {
			return
		}
		seen[eic] = true
		out = append(out, eic)
	}
	for _, e := range strings.Split(flagValue, ",") {
		add(e)
	}
	for _, e := range found {
		if strings.HasPrefix(e, "62Z") {
			add(e)
		}
	}
	return out
}

// recorder writes every answer of the cabinet into dir and keeps an
// index of them.
type recorder struct {
	dir   string
	n     int
	index []string
	hops  []string
	pages []page
	links []string
}

type page struct {
	final *url.URL
	body  []byte
}

// redirect notes each hop; the answer it leads to carries them.
func (r *recorder) redirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("більше 10 перенаправлень")
	}
	r.hops = append(r.hops, "    → "+req.URL.RequestURI())
	return nil
}

func (r *recorder) answer(e outages.Exchange) {
	r.n++
	ct := e.Header.Get("Content-Type")
	ext := "html"
	if strings.Contains(ct, "json") || strings.HasPrefix(strings.TrimSpace(string(e.Body)), "{") {
		ext = "json"
	}
	name := fmt.Sprintf("%02d-%s-%s.%s", r.n, strings.ToLower(e.Method), slug(e.Final), ext)
	if err := os.WriteFile(filepath.Join(r.dir, name), e.Body, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "  не записано %s: %v\n", name, err)
	}
	line := fmt.Sprintf("%02d %s %s → %d %s [%s, %d байт] %s", r.n, e.Method, e.URL.RequestURI(), e.Status, e.Final.RequestURI(), ct, len(e.Body), name)
	if names := cookieNames(e.Header); names != "" {
		line += "\n    set-cookie: " + names
	}
	if len(r.hops) > 0 {
		line += "\n" + strings.Join(r.hops, "\n")
		r.hops = nil
	}
	r.index = append(r.index, line)
	r.pages = append(r.pages, page{final: e.Final, body: e.Body})
}

func (r *recorder) lastBody() []byte {
	if len(r.pages) == 0 {
		return nil
	}
	return r.pages[len(r.pages)-1].body
}

func (r *recorder) lastFinal() string {
	if len(r.pages) == 0 {
		return ""
	}
	return r.pages[len(r.pages)-1].final.RequestURI()
}

func (r *recorder) finals() []string {
	out := make([]string, 0, len(r.pages))
	for _, p := range r.pages {
		out = append(out, p.final.RequestURI())
	}
	return out
}

var eicRe = regexp.MustCompile(`\b62[A-Z][0-9A-Z-]{12}[0-9A-Z]\b`)

// eics is every EIC-looking code on the pages seen, sorted.
func (r *recorder) eics() []string {
	seen := map[string]bool{}
	for _, p := range r.pages {
		for _, m := range eicRe.FindAll(p.body, -1) {
			seen[string(m)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func (r *recorder) save() error {
	index := strings.Join(r.index, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(r.dir, "index.txt"), []byte(index), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "links.txt"), []byte(strings.Join(r.links, "\n")+"\n"), 0o600)
}

var hrefRe = regexp.MustCompile(`href\s*=\s*["']([^"'#\s]+)["']`)

// cabinetLinks is every link of the page that stays in the cabinet, as a
// path with its query, in page order.
func cabinetLinks(body []byte, host string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range hrefRe.FindAllSubmatch(body, -1) {
		u, err := url.Parse(html.UnescapeString(string(m[1])))
		if err != nil || (u.Host != "" && u.Host != host) || (u.Scheme != "" && u.Scheme != "https") {
			continue
		}
		p := u.RequestURI()
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// worthOpening keeps the probe to pages that show something: outage
// lists and the cabinet's own sections, never a link that acts.
func worthOpening(p string) bool {
	l := strings.ToLower(p)
	for _, bad := range []string{
		"logout", "exit", "vyhid", "delete", "remove", "del_", "edit", "change", "pay", "send",
		"subscribe", "set_", "add_", "static/", "download", "upload",
		".css", ".js", ".png", ".jpg", ".jpeg", ".svg", ".ico", ".pdf", ".doc", ".xls",
	} {
		if strings.Contains(l, bad) {
			return false
		}
	}
	for _, good := range []string{"disconnect", "vidkl", "grafik", "gpv"} {
		if strings.Contains(l, good) {
			return true
		}
	}
	return strings.HasPrefix(l, "/account") && !strings.Contains(l, "?") && strings.Count(l, "/") <= 2
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slug(u *url.URL) string {
	s := strings.Trim(slugRe.ReplaceAllString(u.RequestURI(), "_"), "_")
	if s == "" {
		return "root"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func cookieNames(h http.Header) string {
	var names []string
	for _, c := range h.Values("Set-Cookie") {
		if name, _, ok := strings.Cut(c, "="); ok {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func ask(in *bufio.Reader, prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		fail("%v", err)
	}
	return strings.TrimSpace(line)
}

func askHidden(in *bufio.Reader, prompt string) string {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ask(in, prompt)
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fail("%v", err)
	}
	return strings.TrimSpace(string(b))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "esvitlo-probe: "+format+"\n", args...)
	os.Exit(1)
}
