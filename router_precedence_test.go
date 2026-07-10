// ⚡️ Fiber is an Express inspired web framework written in Go with ☕️
// 🤖 GitHub Repository: https://github.com/gofiber/fiber
// 📌 API Documentation: https://docs.gofiber.io

package fiber

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// hitBody drives a request through the app and returns the response body, which
// each test uses as a marker for which handler was selected.
func hitBody(tb testing.TB, app *App, method, path string) string {
	tb.Helper()
	resp, err := app.Test(httptest.NewRequest(method, path, http.NoBody))
	require.NoError(tb, err, "app.Test(%s %s)", method, path)
	body, err := io.ReadAll(resp.Body)
	require.NoError(tb, err, "read body")
	return string(body)
}

// Test 1: the most specific pattern wins even when the broad wildcard is
// registered first. This is the concrete zip case: `/v1/iam/*` before
// `/v1/iam/keys`.
func Test_Precedence_StaticBeatsWildcard_RegisteredFirst(t *testing.T) {
	t.Parallel()
	app := New()
	app.Get("/v1/iam/*", func(c Ctx) error { return c.SendString("wildcard") })
	app.Get("/v1/iam/keys", func(c Ctx) error { return c.SendString("static") })

	require.Equal(t, "static", hitBody(t, app, MethodGet, "/v1/iam/keys"))
	// The wildcard still serves everything else under the prefix.
	require.Equal(t, "wildcard", hitBody(t, app, MethodGet, "/v1/iam/tokens"))
}

// Test 2: a static sibling beats a named parameter at the same depth,
// regardless of registration order.
func Test_Precedence_StaticBeatsParam(t *testing.T) {
	t.Parallel()

	// param registered first
	app := New()
	app.Get("/users/:id", func(c Ctx) error { return c.SendString("param") })
	app.Get("/users/new", func(c Ctx) error { return c.SendString("static") })
	require.Equal(t, "static", hitBody(t, app, MethodGet, "/users/new"))
	require.Equal(t, "param", hitBody(t, app, MethodGet, "/users/42"))

	// static registered first — same outcome, order independent
	app2 := New()
	app2.Get("/users/new", func(c Ctx) error { return c.SendString("static") })
	app2.Get("/users/:id", func(c Ctx) error { return c.SendString("param") })
	require.Equal(t, "static", hitBody(t, app2, MethodGet, "/users/new"))
	require.Equal(t, "param", hitBody(t, app2, MethodGet, "/users/42"))
}

// Test 3: a deeper static route beats a shallower wildcard.
func Test_Precedence_DeepStaticBeatsShallowWildcard(t *testing.T) {
	t.Parallel()
	app := New()
	app.Get("/api/*", func(c Ctx) error { return c.SendString("wildcard") })
	app.Get("/api/v1/users", func(c Ctx) error { return c.SendString("deep-static") })

	require.Equal(t, "deep-static", hitBody(t, app, MethodGet, "/api/v1/users"))
	require.Equal(t, "wildcard", hitBody(t, app, MethodGet, "/api/v2/orders"))
}

// Test 4: an ambiguous conflict — two DISTINCT patterns of equal specificity
// that overlap (neither is more specific) — panics loudly at registration,
// naming both patterns, instead of one silently shadowing the other. This is
// the Go 1.22 net/http.ServeMux "neither more specific" conflict rule.
func Test_Precedence_AmbiguousConflictPanics(t *testing.T) {
	t.Parallel()
	app := New()
	app.Get("/users/:id", func(c Ctx) error { return c.SendString("id") })

	require.PanicsWithValue(t,
		"fiber: route conflict: GET /users/:name conflicts with GET /users/:id (equal specificity, ambiguous match)",
		func() {
			app.Get("/users/:name", func(c Ctx) error { return c.SendString("name") })
		},
	)
}

// The ambiguous-conflict check is by specificity, so a param sitting where a
// wildcard sits at the same depth is NOT a conflict (the param is more
// specific) and must not panic.
func Test_Precedence_NonConflictingOverlapNoPanic(t *testing.T) {
	t.Parallel()
	app := New()
	require.NotPanics(t, func() {
		app.Get("/a/:id", func(c Ctx) error { return c.SendString("param") })
		app.Get("/a/*", func(c Ctx) error { return c.SendString("wildcard") })
	})
	require.Equal(t, "param", hitBody(t, app, MethodGet, "/a/42"))
}

// Re-registering the SAME method + pattern keeps fiber's documented handler
// merge (both handler sets run in order); it is not a conflict because it never
// shadows. This is the deliberate divergence from a strict "identical pattern
// panics" rule — breaking merge would fail upstream tests and regress the common
// app.All-after-a-specific-method pattern.
func Test_Precedence_IdenticalPatternMerges(t *testing.T) {
	t.Parallel()
	app := New()
	var ran string
	app.Get("/v1/x", func(c Ctx) error { ran += "1"; return c.Next() })
	app.Get("/v1/x", func(c Ctx) error { ran += "2"; return c.SendString(ran) })

	require.Len(t, app.stack[app.methodInt(MethodGet)], 1)
	require.Equal(t, "12", hitBody(t, app, MethodGet, "/v1/x"))
}

// Test 5: middleware keeps declaration order — it is not sorted by specificity.
func Test_Precedence_MiddlewareOrderPreserved(t *testing.T) {
	t.Parallel()
	app := New()
	var order string
	app.Use(func(c Ctx) error { order += "A"; return c.Next() })
	app.Use(func(c Ctx) error { order += "B"; return c.Next() })
	app.Use(func(c Ctx) error { order += "C"; return c.Next() })
	app.Get("/x", func(c Ctx) error { order += "H"; return c.SendString(order) })

	require.Equal(t, "ABCH", hitBody(t, app, MethodGet, "/x"))
}

// Middleware is a precedence barrier: endpoints are sorted by specificity only
// within the run between barriers, which keeps middleware-before-handler
// semantics exact. Fiber stores middleware and endpoints in one ordered stack,
// so an endpoint cannot both beat an earlier route on precedence AND stay
// wrapped by a middleware that sits between them — the two orderings share a
// single dimension. This fork preserves the middleware ordering (the dangerous
// one to break) and sorts endpoints within each middleware context.
func Test_Precedence_MiddlewareBarrierWithSortedEndpoints(t *testing.T) {
	t.Parallel()

	// Common case: middleware first, then both endpoints in one context. The
	// specific route wins precedence AND the middleware wraps it.
	app := New()
	var pre string
	app.Use(func(c Ctx) error { pre += "M"; return c.Next() })
	app.Get("/v1/iam/*", func(c Ctx) error { return c.SendString("wildcard") })
	app.Get("/v1/iam/keys", func(c Ctx) error { return c.SendString("static:" + pre) })
	require.Equal(t, "static:M", hitBody(t, app, MethodGet, "/v1/iam/keys"))

	// Barrier case: a middleware declared BETWEEN two endpoints splits them into
	// separate contexts, so their relative order is registration order (not
	// specificity). The earlier wildcard therefore wins here — the deliberate,
	// documented cost of never reordering endpoints across a middleware.
	app2 := New()
	app2.Get("/v1/iam/*", func(c Ctx) error { return c.SendString("wildcard") })
	app2.Use(func(c Ctx) error { return c.Next() })
	app2.Get("/v1/iam/keys", func(c Ctx) error { return c.SendString("static") })
	require.Equal(t, "wildcard", hitBody(t, app2, MethodGet, "/v1/iam/keys"))
}

// Test 6: a route added at runtime (after startup, followed by RebuildTree)
// lands in the correct precedence position, not merely at the end.
func Test_Precedence_RuntimeAddedRouteSorted(t *testing.T) {
	t.Parallel()
	app := New()
	app.Get("/v1/iam/*", func(c Ctx) error { return c.SendString("wildcard") })

	// Before the static route exists, the wildcard serves the path.
	require.Equal(t, "wildcard", hitBody(t, app, MethodGet, "/v1/iam/keys"))

	// Add the more specific route at runtime and rebuild the tree.
	app.Get("/v1/iam/keys", func(c Ctx) error { return c.SendString("static") })
	app.RebuildTree()

	require.Equal(t, "static", hitBody(t, app, MethodGet, "/v1/iam/keys"))
	require.Equal(t, "wildcard", hitBody(t, app, MethodGet, "/v1/iam/tokens"))
}

// compareRoutes unit coverage for the ordering rules.
func Test_Precedence_CompareRoutes(t *testing.T) {
	t.Parallel()
	mk := func(p string) *Route { return &Route{Path: p, routeParser: parseRoute(p)} }

	cases := []struct {
		name string
		a, b string
		want int // -1 a first, +1 b first, 0 tie
	}{
		{"static-before-wildcard", "/v1/iam/keys", "/v1/iam/*", -1},
		{"static-before-param", "/users/new", "/users/:id", -1},
		{"deep-static-before-shallow-wildcard", "/api/v1/users", "/api/*", -1},
		{"param-before-wildcard", "/a/:id", "/a/*", -1},
		{"wildcard-after-static", "/v1/iam/*", "/v1/iam/keys", +1},
		{"structural-tie", "/users/:id", "/users/:name", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compareRoutes(mk(tc.a), mk(tc.b))
			switch tc.want {
			case -1:
				require.Negative(t, got, "%s vs %s", tc.a, tc.b)
			case +1:
				require.Positive(t, got, "%s vs %s", tc.a, tc.b)
			default:
				require.Zero(t, got, "%s vs %s", tc.a, tc.b)
			}
		})
	}
}
