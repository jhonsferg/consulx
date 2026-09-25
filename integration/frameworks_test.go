package integration

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v5"

	"github.com/jhonsferg/consulx"
)

// Every router below is an http.Handler, so the generic integration covers
// it: health endpoints are injected in front of the router and the router
// keeps serving its own routes.
func TestFrameworks(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	routers := map[string]func() http.Handler{
		"net-http": func() http.Handler {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "orders") })
			return mux
		},
		"gin": func() http.Handler {
			r := gin.New()
			r.GET("/orders", func(c *gin.Context) { c.String(http.StatusOK, "orders") })
			return r
		},
		"echo": func() http.Handler {
			e := echo.New()
			e.GET("/orders", func(c *echo.Context) error { return c.String(http.StatusOK, "orders") })
			return e
		},
		"chi": func() http.Handler {
			r := chi.NewRouter()
			r.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "orders") })
			return r
		},
	}
	a := startConsul(t)
	for name, router := range routers {
		t.Run(name, func(t *testing.T) { exerciseRouter(t, a, router()) })
	}
}

func exerciseRouter(t *testing.T, a *agent, handler http.Handler) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	svcName := uniqueName(t)
	c, err := consulx.New(
		consulx.WithConsulAddress(a.addr),
		consulx.WithServer(srv), consulx.WithListener(ln),
		consulx.WithServiceName(svcName), consulx.WithServiceAddress(serviceHost),
		consulx.WithAutoHealth(),
		consulx.WithHealth(consulx.HealthConfig{Interval: time.Second, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, srv, ln)
	stop := runUntil(t, c.Run)

	base := "http://" + ln.Addr().String()
	for path, want := range map[string]int{"/orders": 200, "/health": 200, "/health/ready": 200, "/health/live": 200, "/nope": 404} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s: status %d, want %d", path, resp.StatusCode, want)
		}
		if path == "/orders" && string(body) != "orders" {
			t.Fatalf("router response changed: %q", body)
		}
	}

	eventually(t, 30*time.Second, "registered and passing", func() bool { return len(a.instances(t, svcName, true)) == 1 })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if n := len(a.instances(t, svcName, false)); n != 0 {
		t.Fatalf("not deregistered: %d", n)
	}
}
