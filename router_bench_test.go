// ⚡️ Fiber is an Express inspired web framework written in Go with ☕️
// 🤖 GitHub Repository: https://github.com/gofiber/fiber
// 📌 API Documentation: https://docs.gofiber.io

package fiber

// Benchmark suite for the specificity-routing fork (see router_precedence.go).
//
// Three things are proven here, all against ONE representative ~70-route table
// modelled on a real Hanzo cloud gateway (static, param, multi-param, and
// wildcard routes across iam / tracker / commerce / kms / cloud / registry /
// o11y namespaces):
//
//  1. Match hot-path (Benchmark_Cloud_Match / _Dispatch): the fork changed
//     *registration order* (insertion-sorted stack), NOT the matcher. Match is
//     still the unmodified tree walk, so per-request cost is unchanged. The
//     cross-framework three-way comparison (fork vs upstream gofiber v3.2.0 vs
//     net/http.ServeMux) lives in ./bench (separate module, isolates the
//     upstream dep) — see BENCHMARKS.md.
//
//  2. Registration cost (Benchmark_Cloud_Registration): insertRouteSorted scans
//     the method stack once per Add (merge/conflict check) → O(n²) table build.
//     This quantifies it at N = 10 / 70 / 500 / 1000 so the one-time startup
//     cost is a number, not a worry.
//
//  3. Specificity correctness (Test_Cloud_Precedence + the assertions inside
//     Benchmark_Cloud_Match): the most-specific handler wins — static beats
//     wildcard, a deeper param beats a shallow wildcard — so throughput is never
//     measured against wrong routing.
//
// Run:
//
//	go test -run='^$' -bench='Benchmark_Cloud' -benchmem -count=6 .
//
// Methodology and headline numbers: BENCHMARKS.md.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// cloudNoop is the zero-work handler every benchmarked route resolves to.
// Routing cost is what we measure; the handler must add nothing.
func cloudNoop(_ Ctx) error { return nil }

// cloudRoutes is a representative ~70-route table shaped like a production Hanzo
// cloud gateway: a realistic mix of deep static literals, single- and
// multi-parameter routes, and greedy wildcards, spread across service
// namespaces. It is deliberately conflict-free under the fork's precedence rule
// (no two DISTINCT patterns of equal specificity at the same position), so it
// also registers cleanly on Go 1.22 net/http.ServeMux for the ./bench
// comparison. The wildcard routes (/v1/registry/*, /v1/commerce/webhooks/*, …)
// are catch-alls that the static/param siblings must out-rank — see
// cloudMatchRequests and Test_Cloud_Precedence.
var cloudRoutes = []testRoute{
	// iam — identity (all-static namespace, deep literals)
	{MethodGet, "/v1/iam/keys"},
	{MethodPost, "/v1/iam/keys"},
	{MethodGet, "/v1/iam/keys/:keyId"},
	{MethodDelete, "/v1/iam/keys/:keyId"},
	{MethodGet, "/v1/iam/organizations"},
	{MethodPost, "/v1/iam/organizations"},
	{MethodGet, "/v1/iam/organizations/:orgId"},
	{MethodPatch, "/v1/iam/organizations/:orgId"},
	{MethodGet, "/v1/iam/organizations/:orgId/members"},
	{MethodPost, "/v1/iam/organizations/:orgId/members"},
	{MethodGet, "/v1/iam/organizations/:orgId/members/:memberId"},
	{MethodDelete, "/v1/iam/organizations/:orgId/members/:memberId"},
	{MethodGet, "/v1/iam/organizations/:orgId/members/:memberId/roles"},
	{MethodGet, "/v1/iam/users"},
	{MethodGet, "/v1/iam/users/:userId"},
	{MethodGet, "/v1/iam/applications"},
	{MethodGet, "/v1/iam/applications/:appId"},
	{MethodGet, "/v1/iam/certs"},
	{MethodGet, "/v1/iam/oauth/authorize"},
	{MethodPost, "/v1/iam/oauth/token"},
	{MethodGet, "/v1/iam/oauth/userinfo"},

	// tracker — single-param resource tree
	{MethodPost, "/v1/tracker"},
	{MethodGet, "/v1/tracker/:id"},
	{MethodGet, "/v1/tracker/:id/events"},
	{MethodPost, "/v1/tracker/:id/events"},
	{MethodGet, "/v1/tracker/:id/events/:eventId"},

	// commerce — mixed static/param plus a webhook wildcard
	{MethodGet, "/v1/commerce/products"},
	{MethodPost, "/v1/commerce/products"},
	{MethodGet, "/v1/commerce/products/:productId"},
	{MethodGet, "/v1/commerce/orders"},
	{MethodPost, "/v1/commerce/orders"},
	{MethodGet, "/v1/commerce/orders/:orderId"},
	{MethodGet, "/v1/commerce/orders/:orderId/items"},
	{MethodGet, "/v1/commerce/orders/:orderId/items/:itemId"},
	{MethodGet, "/v1/commerce/checkout"},
	{MethodPost, "/v1/commerce/checkout"},
	{MethodPost, "/v1/commerce/webhooks/*"},

	// kms — secrets and key material
	{MethodGet, "/v1/kms/secrets"},
	{MethodPost, "/v1/kms/secrets"},
	{MethodGet, "/v1/kms/secrets/:secretId"},
	{MethodPut, "/v1/kms/secrets/:secretId"},
	{MethodDelete, "/v1/kms/secrets/:secretId"},
	{MethodPost, "/v1/kms/encrypt"},
	{MethodPost, "/v1/kms/decrypt"},
	{MethodGet, "/v1/kms/keys/:keyId"},
	{MethodGet, "/v1/kms/keys/:keyId/versions"},

	// cloud — deployments
	{MethodGet, "/v1/cloud/deployments"},
	{MethodPost, "/v1/cloud/deployments"},
	{MethodGet, "/v1/cloud/deployments/:deployId"},
	{MethodGet, "/v1/cloud/deployments/:deployId/logs"},
	{MethodGet, "/v1/cloud/deployments/:deployId/status"},
	{MethodGet, "/v1/cloud/regions"},

	// registry — the static-beats-wildcard / param-beats-wildcard testbed
	{MethodGet, "/v1/registry/images"},
	{MethodGet, "/v1/registry/images/:name"},
	{MethodGet, "/v1/registry/images/:name/tags"},
	{MethodGet, "/v1/registry/images/:name/tags/:tag"},
	{MethodGet, "/v1/registry/*"},

	// o11y — observability
	{MethodGet, "/v1/o11y/metrics"},
	{MethodPost, "/v1/o11y/query"},
	{MethodGet, "/v1/o11y/traces/:traceId"},
	{MethodGet, "/v1/o11y/logs"},

	// gateway + top-level health/assets
	{MethodGet, "/v1/health"},
	{MethodGet, "/v1/version"},
	{MethodGet, "/v1/status"},
	{MethodGet, "/v1/gateway/routes"},
	{MethodGet, "/v1/gateway/routes/:routeId"},
	{MethodGet, "/v1/gateway/upstreams"},
	{MethodGet, "/v1/static/*"},
	{MethodGet, "/assets/*"},
	{MethodGet, "/"},
}

// cloudReq is a single benchmarked request plus the route we assert it must
// resolve to — so the match benchmark can never be measured against wrong
// routing (item 3).
type cloudReq struct {
	name   string
	method string
	path   string
	expect string // route.Path of the most-specific matching route
}

// cloudMatchRequests exercises each routing shape and, critically, the three
// precedence cases where a wildcard would win under registration-order routing
// but must lose under specificity routing.
var cloudMatchRequests = []cloudReq{
	{"static_shallow", MethodGet, "/v1/health", "/v1/health"},
	{"static_deep", MethodGet, "/v1/iam/oauth/userinfo", "/v1/iam/oauth/userinfo"},
	{"param_single", MethodGet, "/v1/tracker/trk_abc123", "/v1/tracker/:id"},
	{"param_mid", MethodGet, "/v1/iam/organizations/org_42/members", "/v1/iam/organizations/:orgId/members"},
	{"param_multi_deep", MethodGet, "/v1/commerce/orders/ord_9/items/item_3", "/v1/commerce/orders/:orderId/items/:itemId"},
	{"wildcard", MethodGet, "/v1/registry/blobs/sha256/abc", "/v1/registry/*"},
	{"static_beats_wildcard", MethodGet, "/v1/registry/images", "/v1/registry/images"},
	{"param_beats_wildcard", MethodGet, "/v1/registry/images/alpine/tags", "/v1/registry/images/:name/tags"},
	{"root", MethodGet, "/", "/"},
}

// newCloudApp builds and starts an app carrying the full cloudRoutes table.
func newCloudApp() *App {
	app := New()
	for _, r := range cloudRoutes {
		app.Add([]string{r.Method}, r.Path, cloudNoop)
	}
	app.startupProcess()
	return app
}

// Benchmark_Cloud_Match measures the routing hot-path in isolation: app.next
// (the tree walk) over the 70-route table, one sub-benchmark per representative
// request. This is the fork's authoritative match cost — no response is written.
// Each sub-benchmark first asserts the correct (most-specific) route wins.
func Benchmark_Cloud_Match(b *testing.B) {
	app := newCloudApp()

	for _, req := range cloudMatchRequests {
		b.Run(req.name, func(b *testing.B) {
			fctx := &fasthttp.RequestCtx{}
			fctx.Request.Header.SetMethod(req.method)
			fctx.URI().SetPath(req.path)

			// Correctness gate: the most-specific route must win before we
			// time anything, so ns/op is never measured on a mis-route.
			ctx := app.AcquireCtx(fctx).(*DefaultCtx) //nolint:errcheck,forcetypeassert
			matched, err := app.next(ctx)
			require.NoError(b, err)
			require.True(b, matched, "request %q did not match", req.path)
			require.Equal(b, req.expect, ctx.Route().Path)
			app.ReleaseCtx(ctx)

			b.ReportAllocs()
			for b.Loop() {
				c := app.AcquireCtx(fctx).(*DefaultCtx) //nolint:errcheck,forcetypeassert
				_, _ = app.next(c)
				app.ReleaseCtx(c)
			}
		})
	}
}

// Benchmark_Cloud_Dispatch measures the full public request path
// (app.Handler(): acquire ctx → route → run handler → write 200) for the same
// requests. This is the idiom used by the ./bench three-way comparison, so the
// numbers here line up directly with the fork column there.
func Benchmark_Cloud_Dispatch(b *testing.B) {
	app := newCloudApp()
	h := app.Handler()

	for _, req := range cloudMatchRequests {
		b.Run(req.name, func(b *testing.B) {
			fctx := &fasthttp.RequestCtx{}
			fctx.Request.Header.SetMethod(req.method)
			fctx.URI().SetPath(req.path)

			b.ReportAllocs()
			for b.Loop() {
				h(fctx)
			}
			require.Equal(b, StatusOK, fctx.Response.StatusCode())
		})
	}
}

// cloudScaleRoutes generates n distinct, conflict-free GET routes on a single
// method stack — the worst case for the insertion-sorted registration, since
// insertRouteSorted scans the whole (growing) stack once per Add. Distinct
// static first segments guarantee no ambiguous-conflict panic.
func cloudScaleRoutes(n int) []string {
	routes := make([]string, n)
	for i := range routes {
		routes[i] = fmt.Sprintf("/v1/g%05d/resource/:id", i)
	}
	return routes
}

// Benchmark_Cloud_Registration quantifies the O(n²) table build the fork's
// sorted insertion introduces, at realistic and stress route counts. NewOnly is
// the fixed App-construction baseline; AddOnly isolates the insertion sort;
// AddAndBuild adds the one-time tree build (startupProcess). Read ns/op as the
// wall-clock cost of building a whole table once (it is a startup cost, paid
// zero times per request).
func Benchmark_Cloud_Registration(b *testing.B) {
	b.Run("NewOnly", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = New()
		}
	})

	for _, n := range []int{10, 70, 500, 1000} {
		routes := cloudScaleRoutes(n)

		b.Run(fmt.Sprintf("AddOnly/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				app := New()
				for _, p := range routes {
					app.Add([]string{MethodGet}, p, cloudNoop)
				}
			}
		})

		b.Run(fmt.Sprintf("AddAndBuild/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				app := New()
				for _, p := range routes {
					app.Add([]string{MethodGet}, p, cloudNoop)
				}
				app.startupProcess()
			}
		})
	}
}

// Test_Cloud_Precedence proves specificity routing over the representative
// table: every request resolves to its most-specific route, and in particular a
// wildcard never shadows a static or param sibling. This is the correctness
// contract the benchmarks assume.
func Test_Cloud_Precedence(t *testing.T) {
	t.Parallel()
	app := newCloudApp()

	for _, req := range cloudMatchRequests {
		fctx := &fasthttp.RequestCtx{}
		fctx.Request.Header.SetMethod(req.method)
		fctx.URI().SetPath(req.path)

		ctx := app.AcquireCtx(fctx).(*DefaultCtx) //nolint:errcheck,forcetypeassert
		matched, err := app.next(ctx)
		require.NoError(t, err, req.name)
		require.True(t, matched, "%s: %s %s did not match", req.name, req.method, req.path)
		require.Equalf(t, req.expect, ctx.Route().Path,
			"%s: %s %s routed to %q, want %q", req.name, req.method, req.path, ctx.Route().Path, req.expect)
		app.ReleaseCtx(ctx)
	}
}

// --- net/http.ServeMux (Go 1.22) routing-only baseline ------------------------
//
// The fork's precedence contract is modelled directly on Go 1.22's enhanced
// ServeMux (most-specific-wins, ambiguous patterns rejected). ServeMux is
// therefore both the natural correctness oracle and a fair routing-only
// baseline: mux.Handler(req) is a pure match (no handler run, no response
// written), which is the same shape of work as app.next. Both are stdlib-only,
// so this comparison lives here rather than in ./bench (which isolates the one
// dep that truly needs isolating — upstream gofiber). See BENCHMARKS.md.

// fiberToMuxPattern translates a fiber route into its Go 1.22 ServeMux pattern:
// ":name" → "{name}", trailing "*" → "{rest...}", and the root "/" → "/{$}"
// (exact match). The result is prefixed with the method, e.g. "GET /v1/x/{id}".
func fiberToMuxPattern(method, path string) string {
	if path == "/" {
		return method + " /{$}"
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		switch {
		case s == "*":
			segs[i] = "{rest...}"
		case strings.HasPrefix(s, ":"):
			segs[i] = "{" + s[1:] + "}"
		}
	}
	return method + " " + strings.Join(segs, "/")
}

// cloudServeMux registers the full cloudRoutes table on a Go 1.22 ServeMux,
// translating fiber syntax to ServeMux patterns. It shares the exact route set
// and specificity semantics with the fiber apps, so the comparison is on the
// same table.
func cloudServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, r := range cloudRoutes {
		mux.Handle(fiberToMuxPattern(r.Method, r.Path), noop)
	}
	return mux
}

// Benchmark_Cloud_Match_ServeMux is the routing-only counterpart to
// Benchmark_Cloud_Match, on net/http.ServeMux. mux.Handler(req) returns the
// matched handler+pattern without serving — the fair, allocation-light routing
// measurement to place beside app.next.
func Benchmark_Cloud_Match_ServeMux(b *testing.B) {
	mux := cloudServeMux()

	for _, req := range cloudMatchRequests {
		b.Run(req.name, func(b *testing.B) {
			hreq := httptest.NewRequest(req.method, req.path, nil)

			// Correctness gate: same most-specific route as fiber.
			_, pattern := mux.Handler(hreq)
			require.Equal(b, fiberToMuxPattern(req.method, req.expect), pattern)

			b.ReportAllocs()
			for b.Loop() {
				_, _ = mux.Handler(hreq)
			}
		})
	}
}

// Benchmark_Cloud_Registration_ServeMux measures ServeMux table build at the
// same N as the fiber registration benchmark. Go 1.22 ServeMux also runs a
// specificity/conflict check per registration, so it is the natural point of
// comparison for the fork's insertion-sorted (O(n²)) registration.
func Benchmark_Cloud_Registration_ServeMux(b *testing.B) {
	for _, n := range []int{10, 70, 500, 1000} {
		routes := cloudScaleRoutes(n)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			for b.Loop() {
				mux := http.NewServeMux()
				for _, p := range routes {
					mux.Handle(fiberToMuxPattern(MethodGet, p), noop)
				}
			}
		})
	}
}

// Test_Cloud_ServeMux_Precedence proves the fork and Go 1.22 ServeMux agree on
// every representative request — both the translation and the precedence model.
// If this passes alongside Test_Cloud_Precedence, the fork is a faithful
// most-specific-wins router by the same rules ServeMux uses.
func Test_Cloud_ServeMux_Precedence(t *testing.T) {
	t.Parallel()
	mux := cloudServeMux()

	for _, req := range cloudMatchRequests {
		hreq := httptest.NewRequest(req.method, req.path, nil)
		_, pattern := mux.Handler(hreq)
		require.Equalf(t, fiberToMuxPattern(req.method, req.expect), pattern,
			"%s: %s %s → ServeMux pattern %q, want fiber-equivalent of %q",
			req.name, req.method, req.path, pattern, req.expect)
	}
}
