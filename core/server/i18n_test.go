package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPickLang(t *testing.T) {
	for _, tc := range []struct{ accept, cookie, want string }{
		{"", "", langEN},
		{"vi-VN,vi;q=0.9,en;q=0.8", "", langVI},
		{"en-US,en;q=0.9,vi;q=0.8", "", langEN},
		{"fr-FR,vi;q=0.5", "", langVI}, // first supported language wins
		{"vi", "en", langEN},           // explicit choice overrides the browser
		{"en", "vi", langVI},
		{"en", "xx", langEN}, // bad cookie ignored
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", tc.accept)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: langCookie, Value: tc.cookie})
		}
		if got := pickLang(r); got != tc.want {
			t.Errorf("accept=%q cookie=%q: got %s want %s", tc.accept, tc.cookie, got, tc.want)
		}
	}
}

func TestEveryMessageHasBothLanguages(t *testing.T) {
	for k, m := range messages {
		if strings.TrimSpace(m[0]) == "" || strings.TrimSpace(m[1]) == "" {
			t.Errorf("%s: missing translation", k)
		}
	}
}

func TestLoginPageLanguages(t *testing.T) {
	srv := httptest.NewServer(NewHandler(Options{Root: t.TempDir()}))
	defer srv.Close()
	get := func(accept string) string {
		req, _ := http.NewRequest("GET", srv.URL+"/__nasfone/login", nil)
		req.Header.Set("Accept-Language", accept)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	if p := get("vi-VN"); !strings.Contains(p, "Đăng nhập") || !strings.Contains(p, `lang="vi"`) {
		t.Fatal("Vietnamese login page")
	}
	if p := get("en-US"); !strings.Contains(p, "Sign in") || !strings.Contains(p, `lang="en"`) {
		t.Fatal("English login page")
	}
}

func TestLegacyPathsRedirect(t *testing.T) {
	srv := httptest.NewServer(NewHandler(Options{Root: t.TempDir()}))
	defer srv.Close()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(srv.URL + "/__pnas/login?next=%2F")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/" {
		t.Fatalf("got %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}
