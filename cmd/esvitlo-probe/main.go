// Command esvitlo-probe signs in to an e-svitlo cabinet with the
// company's account and keeps every page the cabinet answers, so the
// outages reader can be checked against the real cabinet before the API
// relies on it.
//
//	go run ./cmd/esvitlo-probe [-host vn.e-svitlo.com.ua] [-eic 62Z…] [-keep-session]
//
// It asks for the login and, without echo, the password; neither is
// written down. It opens only pages that show something — the cabinet's
// sections, an account's views, outage lists — never a link that acts.
// The pages land in tmp/esvitlo-probe/<time>/, which git ignores: they
// are the company's own cabinet, so they stay local. -keep-session leaves
// the session's cookie there too (cookies.txt, for curl -b) instead of
// signing out.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
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

const (
	// maxEICs bounds how many metering points one run reads outages for.
	maxEICs = 5
	// maxDepth is how many links deep from the cabinet's sections the
	// probe goes.
	maxDepth = 3
)

func main() {
	host := flag.String("host", "vn.e-svitlo.com.ua", "кабінет оператора")
	eicFlag := flag.String("eic", "", "EIC через кому; знайдені в кабінеті перевіряються й без цього")
	outRoot := flag.String("out", "tmp/esvitlo-probe", "куди складати сторінки")
	limit := flag.Int("pages", 25, "скільки сторінок кабінету відкрити після входу")
	keep := flag.Bool("keep-session", false, "не виходити з кабінету й лишити куку сесії в cookies.txt")
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
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: rec.redirect, Jar: jar}
	base := outages.BaseURL(*host)
	c := outages.NewClient(base, login, password, loc, hc)
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
	crawl(ctx, c, rec, *host, *limit)

	eics := rec.eics()
	accounts := rec.accounts()
	links := rec.outageLinks()
	fmt.Printf("Рахунки (a=): %s\n", listOrNone(accounts))
	fmt.Printf("EIC у кабінеті: %s\n", listOrNone(sortedKeys(eics)))
	fmt.Printf("Посилання на відключення: %s\n", listOrNone(links))
	points := pointsToRead(*eicFlag, sortedKeys(eics))
	switch {
	case signErr != nil:
		fmt.Println("Відключення не читаю: вхід не вдався")
	case len(points) == 0:
		fmt.Println("Відключення не читаю: немає EIC (можна вказати -eic 62Z…)")
	}
	if signErr == nil {
		for _, eic := range points {
			account := eics[eic]
			if account == "" && len(accounts) > 0 {
				account = accounts[0]
			}
			readOutages(ctx, c, eic, account, links, loc)
		}
		if *keep {
			if err := saveCookies(dir, base, jar); err != nil {
				fmt.Printf("Куку сесії не записано: %v\n", err)
			} else {
				fmt.Println("Сесію лишено відкритою: cookies.txt")
			}
		} else if _, _, err := c.Get(ctx, "/user/logout"); err != nil {
			fmt.Printf("Вихід з кабінету: %v\n", err)
		}
	}

	if err := rec.save(); err != nil {
		fail("%v", err)
	}
	fmt.Printf("\nЗбережено відповідей: %d — %s\n", rec.n, dir)
	fmt.Println("Пароль ніде не записано. Сторінки лишаються на цьому компʼютері (tmp/ поза git).")
}

// crawl opens, breadth first, the cabinet's sections and the pages they
// link that only show something, up to limit pages and maxDepth links
// deep.
func crawl(ctx context.Context, c *outages.Client, rec *recorder, host string, limit int) {
	type item struct {
		path  string
		depth int
	}
	seen := map[string]bool{}
	for _, p := range rec.finals() {
		seen[p] = true
	}
	var queue []item
	push := func(body []byte, depth int) {
		for _, p := range cabinetLinks(body, host) {
			rec.link(p)
			if !seen[p] && worthOpening(p) {
				seen[p] = true
				queue = append(queue, item{p, depth})
			}
		}
	}
	for _, p := range []string{"/account_legal", "/account_household"} {
		if !seen[p] {
			seen[p] = true
			queue = append(queue, item{p, 1})
		}
	}
	push(rec.lastBody(), 1)
	for opened := 0; len(queue) > 0 && opened < limit && ctx.Err() == nil; opened++ {
		it := queue[0]
		queue = queue[1:]
		body, final, err := c.Get(ctx, it.path)
		if err != nil {
			fmt.Printf("  %s: %v\n", it.path, err)
			continue
		}
		fmt.Printf("  відкрито %s → %s\n", it.path, final.RequestURI())
		seen[final.RequestURI()] = true
		if it.depth < maxDepth {
			push([]byte(body), it.depth+1)
		}
	}
}

// readOutages tries the outage lists the cabinet linked, then the paths
// a household and a legal account would have, until one parses.
func readOutages(ctx context.Context, c *outages.Client, eic, account string, links []string, loc *time.Location) {
	for _, path := range outageCandidates(eic, account, links) {
		body, final, err := c.Get(ctx, path)
		if err != nil {
			fmt.Printf("Відключення %s за %s: %v\n", eic, path, err)
			continue
		}
		list, err := outages.ParseOutages([]byte(body), loc)
		if err != nil {
			fmt.Printf("Відключення %s за %s → %s: %v\n", eic, path, final.RequestURI(), err)
			continue
		}
		if len(list) == 0 {
			fmt.Printf("Відключення %s за %s: немає запланованих\n", eic, path)
			return
		}
		fmt.Printf("Відключення %s за %s:\n", eic, path)
		for _, iv := range list {
			kind := "ГПВ"
			if iv.Kind == outages.KindOther {
				kind = "інше (роботи / аварія)"
			}
			fmt.Printf("  %s – %s  %s\n", iv.Start.In(loc).Format("02.01 15:04"), iv.End.In(loc).Format("02.01 15:04"), kind)
		}
		return
	}
}

func outageCandidates(eic, account string, links []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string, withAccount bool) {
		u, err := url.Parse(path)
		if err != nil {
			return
		}
		q := u.Query()
		q.Set("eic", eic)
		if withAccount && account != "" && q.Get("a") == "" {
			q.Set("a", account)
		}
		u.RawQuery = q.Encode()
		if s := u.String(); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, l := range links {
		add(l, true)
	}
	add("/account_legal/show_only_disconnections?type_user=2", true)
	add("/account_household/show_only_disconnections?type_user=1", true)
	add("/account_household/show_only_disconnections", false)
	return out
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
	known map[string]bool
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

// link notes a cabinet link seen on any page, for links.txt.
func (r *recorder) link(p string) {
	if r.known == nil {
		r.known = map[string]bool{}
	}
	if !r.known[p] {
		r.known[p] = true
		r.links = append(r.links, p)
	}
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

// eics is every EIC-looking code on the pages seen, with the account
// (a=) of the first page it was on, "" when that page names none.
func (r *recorder) eics() map[string]string {
	out := map[string]string{}
	for _, p := range r.pages {
		for _, m := range eicRe.FindAll(p.body, -1) {
			if _, ok := out[string(m)]; !ok {
				out[string(m)] = p.final.Query().Get("a")
			}
		}
	}
	return out
}

var accountRe = regexp.MustCompile(`[?&](?:amp;)?a=(\d+)`)

// accounts is every account id (a=) the pages link, in order seen.
func (r *recorder) accounts() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range r.pages {
		for _, m := range accountRe.FindAllSubmatch(p.body, -1) {
			if id := string(m[1]); !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

var disconnectRe = regexp.MustCompile(`["'](/[^"'\s<>]*disconnect[^"'\s<>]*)["']`)

// outageLinks is every cabinet path about disconnections the pages
// mention, links and scripts alike.
func (r *recorder) outageLinks() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range r.pages {
		for _, m := range disconnectRe.FindAllSubmatch(p.body, -1) {
			if s := html.UnescapeString(string(m[1])); !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func (r *recorder) save() error {
	index := strings.Join(r.index, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(r.dir, "index.txt"), []byte(index), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "links.txt"), []byte(strings.Join(r.links, "\n")+"\n"), 0o600)
}

// saveCookies writes the session's cookies in the Netscape format curl
// reads with -b.
func saveCookies(dir, base string, jar http.CookieJar) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	for _, ck := range jar.Cookies(u) {
		fmt.Fprintf(&b, "%s\tFALSE\t/\tTRUE\t0\t%s\t%s\n", u.Hostname(), ck.Name, ck.Value)
	}
	return os.WriteFile(filepath.Join(dir, "cookies.txt"), []byte(b.String()), 0o600)
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

// actingWords mark a link that does something — signs out, sends,
// changes, signs — or is no page at all.
var actingWords = []string{
	"logout", "exit", "vyhid", "delete", "remove", "del_", "edit", "change", "pay", "send",
	"subscribe", "set_", "add_", "save", "submit", "confirm", "cancel", "create", "new_",
	"sign", "ecp", "request", "zapyt", "dogovor", "contract", "indication", "pokaz",
	"static/", "download", "upload",
	".css", ".js", ".png", ".jpg", ".jpeg", ".svg", ".ico", ".pdf", ".doc", ".xls",
}

// viewPrefixes start the name of a page that only shows an account.
var viewPrefixes = []string{"view", "show", "list", "info", "detail", "get_"}

// worthOpening keeps the probe to pages that show something: outage
// lists, the cabinet's sections and an account's views, never a link
// that acts.
func worthOpening(p string) bool {
	l := strings.ToLower(p)
	for _, w := range actingWords {
		if strings.Contains(l, w) {
			return false
		}
	}
	for _, w := range []string{"disconnect", "vidkl", "grafik", "gpv"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	path, _, _ := strings.Cut(l, "?")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if !strings.HasPrefix(segs[0], "account") {
		return false
	}
	switch len(segs) {
	case 1:
		return true
	case 2:
		for _, v := range viewPrefixes {
			if strings.HasPrefix(segs[1], v) {
				return true
			}
		}
	}
	return false
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

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func listOrNone(list []string) string {
	if len(list) == 0 {
		return "немає"
	}
	return strings.Join(list, ", ")
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
