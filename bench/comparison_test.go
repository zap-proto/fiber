// Package bench is a benchmark-only module that compares the zap-proto/fiber
// specificity-routing fork against the upstream gofiber/fiber v3.2.0 it forked,
// on one identical ~70-route table modelled on a Hanzo cloud gateway.
//
// It answers one question: does the fork's specificity model (insertion-sorted
// routes; see ../router_precedence.go) cost anything at match time? The fork
// changed *registration order*, not the matcher, so the claim is "zero added
// match cost". This module tests it two ways:
//
//   - Benchmark_Match: full request dispatch (app.Handler()) per representative
//     request, fork vs upstream, identical idiom. The fork reorders the router
//     stack by specificity, so per-route the scan position (and thus latency)
//     differs from upstream's registration order — this is a permutation, not a
//     regression.
//
//   - Benchmark_Sweep: dispatch EVERY GET route once per iteration. The total
//     work to match every route is the sum of scan positions, which is
//     permutation-invariant — so fork and upstream must be equal here. This is
//     the clean "zero added match cost" proof.
//
//   - Benchmark_Registration: table build cost, fork vs upstream, at N = 10 /
//     70 / 500 / 1000. Upstream appends (O(n)); the fork inserts sorted with a
//     per-Add conflict scan (O(n²)). This quantifies the one-time startup delta.
//
// The upstream dep is confined to THIS module (its own go.mod). The fork's own
// module never imports it. The net/http.ServeMux (Go 1.22) routing-only
// baseline lives in ../router_bench_test.go (stdlib, no dep to isolate).
package bench

import (
	"fmt"
	"strings"
	"testing"

	upfiber "github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	forkfiber "github.com/zap-proto/fiber/v3"
)

// route is one table entry in fiber path syntax (":x" param, "*" wildcard).
type route struct {
	method string
	path   string
}

// cloudRoutes mirrors the ~70-route table in ../router_bench_test.go. It is
// duplicated (not imported) because a _test.go fixture in package fiber is not
// importable from this separate module; the two must be kept in sync.
var cloudRoutes = []route{
	{"GET", "/v1/iam/keys"},
	{"POST", "/v1/iam/keys"},
	{"GET", "/v1/iam/keys/:keyId"},
	{"DELETE", "/v1/iam/keys/:keyId"},
	{"GET", "/v1/iam/organizations"},
	{"POST", "/v1/iam/organizations"},
	{"GET", "/v1/iam/organizations/:orgId"},
	{"PATCH", "/v1/iam/organizations/:orgId"},
	{"GET", "/v1/iam/organizations/:orgId/members"},
	{"POST", "/v1/iam/organizations/:orgId/members"},
	{"GET", "/v1/iam/organizations/:orgId/members/:memberId"},
	{"DELETE", "/v1/iam/organizations/:orgId/members/:memberId"},
	{"GET", "/v1/iam/organizations/:orgId/members/:memberId/roles"},
	{"GET", "/v1/iam/users"},
	{"GET", "/v1/iam/users/:userId"},
	{"GET", "/v1/iam/applications"},
	{"GET", "/v1/iam/applications/:appId"},
	{"GET", "/v1/iam/certs"},
	{"GET", "/v1/iam/oauth/authorize"},
	{"POST", "/v1/iam/oauth/token"},
	{"GET", "/v1/iam/oauth/userinfo"},

	{"POST", "/v1/tracker"},
	{"GET", "/v1/tracker/:id"},
	{"GET", "/v1/tracker/:id/events"},
	{"POST", "/v1/tracker/:id/events"},
	{"GET", "/v1/tracker/:id/events/:eventId"},

	{"GET", "/v1/commerce/products"},
	{"POST", "/v1/commerce/products"},
	{"GET", "/v1/commerce/products/:productId"},
	{"GET", "/v1/commerce/orders"},
	{"POST", "/v1/commerce/orders"},
	{"GET", "/v1/commerce/orders/:orderId"},
	{"GET", "/v1/commerce/orders/:orderId/items"},
	{"GET", "/v1/commerce/orders/:orderId/items/:itemId"},
	{"GET", "/v1/commerce/checkout"},
	{"POST", "/v1/commerce/checkout"},
	{"POST", "/v1/commerce/webhooks/*"},

	{"GET", "/v1/kms/secrets"},
	{"POST", "/v1/kms/secrets"},
	{"GET", "/v1/kms/secrets/:secretId"},
	{"PUT", "/v1/kms/secrets/:secretId"},
	{"DELETE", "/v1/kms/secrets/:secretId"},
	{"POST", "/v1/kms/encrypt"},
	{"POST", "/v1/kms/decrypt"},
	{"GET", "/v1/kms/keys/:keyId"},
	{"GET", "/v1/kms/keys/:keyId/versions"},

	{"GET", "/v1/cloud/deployments"},
	{"POST", "/v1/cloud/deployments"},
	{"GET", "/v1/cloud/deployments/:deployId"},
	{"GET", "/v1/cloud/deployments/:deployId/logs"},
	{"GET", "/v1/cloud/deployments/:deployId/status"},
	{"GET", "/v1/cloud/regions"},

	{"GET", "/v1/registry/images"},
	{"GET", "/v1/registry/images/:name"},
	{"GET", "/v1/registry/images/:name/tags"},
	{"GET", "/v1/registry/images/:name/tags/:tag"},
	{"GET", "/v1/registry/*"},

	{"GET", "/v1/o11y/metrics"},
	{"POST", "/v1/o11y/query"},
	{"GET", "/v1/o11y/traces/:traceId"},
	{"GET", "/v1/o11y/logs"},

	{"GET", "/v1/health"},
	{"GET", "/v1/version"},
	{"GET", "/v1/status"},
	{"GET", "/v1/gateway/routes"},
	{"GET", "/v1/gateway/routes/:routeId"},
	{"GET", "/v1/gateway/upstreams"},
	{"GET", "/v1/static/*"},
	{"GET", "/assets/*"},
	{"GET", "/"},
}

// repRequest is a representative request with the route pattern it must resolve
// to. Concrete paths substitute a token for each ":param" and a tail for "*".
type repRequest struct {
	name   string
	method string
	path   string
	expect string
}

var repRequests = []repRequest{
	{"static_shallow", "GET", "/v1/health", "/v1/health"},
	{"static_deep", "GET", "/v1/iam/oauth/userinfo", "/v1/iam/oauth/userinfo"},
	{"param_single", "GET", "/v1/tracker/trk_abc123", "/v1/tracker/:id"},
	{"param_mid", "GET", "/v1/iam/organizations/org_42/members", "/v1/iam/organizations/:orgId/members"},
	{"param_multi_deep", "GET", "/v1/commerce/orders/ord_9/items/item_3", "/v1/commerce/orders/:orderId/items/:itemId"},
	{"wildcard", "GET", "/v1/registry/blobs/sha256/abc", "/v1/registry/*"},
	{"static_beats_wildcard", "GET", "/v1/registry/images", "/v1/registry/images"},
	{"param_beats_wildcard", "GET", "/v1/registry/images/alpine/tags", "/v1/registry/images/:name/tags"},
	{"root", "GET", "/", "/"},
}

// concretePath turns a route pattern into a request path: ":x" → "x_1", trailing
// "*" → "x/y". Used to build the full-table sweep request set.
func concretePath(pattern string) string {
	if pattern == "/" {
		return "/"
	}
	segs := strings.Split(pattern, "/")
	for i, s := range segs {
		switch {
		case s == "*":
			segs[i] = "wild/card/tail"
		case strings.HasPrefix(s, ":"):
			segs[i] = s[1:] + "_1"
		}
	}
	return strings.Join(segs, "/")
}

// --- app builders -------------------------------------------------------------

func buildFork() fasthttp.RequestHandler {
	app := forkfiber.New()
	for _, r := range cloudRoutes {
		app.Add([]string{r.method}, r.path, func(forkfiber.Ctx) error { return nil })
	}
	return app.Handler()
}

func buildUpstream() fasthttp.RequestHandler {
	app := upfiber.New()
	for _, r := range cloudRoutes {
		app.Add([]string{r.method}, r.path, func(upfiber.Ctx) error { return nil })
	}
	return app.Handler()
}

// dispatch drives one request through a fasthttp handler on a reused ctx.
func newFctx(method, path string) *fasthttp.RequestCtx {
	fctx := &fasthttp.RequestCtx{}
	fctx.Request.Header.SetMethod(method)
	fctx.URI().SetPath(path)
	return fctx
}

// --- benchmarks ---------------------------------------------------------------

// Benchmark_Match: per representative request, fork vs upstream, full dispatch.
// Same idiom for both (app.Handler()), so any delta is routing. Expect a
// per-route permutation (fork's specificity order ≠ upstream's registration
// order), NOT a systematic regression — see Benchmark_Sweep for the invariant.
func Benchmark_Match(b *testing.B) {
	fork := buildFork()
	up := buildUpstream()

	for _, req := range repRequests {
		fctx := newFctx(req.method, req.path)
		b.Run(req.name+"/fork", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fork(fctx)
			}
		})
		b.Run(req.name+"/upstream", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				up(fctx)
			}
		})
	}
}

// Benchmark_Sweep dispatches every GET route once per iteration. Total match
// work = sum of scan positions over all routes, which is invariant under the
// fork's reordering — so fork and upstream must land within noise. This is the
// "the fork adds zero match cost" headline.
func Benchmark_Sweep(b *testing.B) {
	fork := buildFork()
	up := buildUpstream()

	var gets []*fasthttp.RequestCtx
	for _, r := range cloudRoutes {
		if r.method == "GET" {
			gets = append(gets, newFctx("GET", concretePath(r.path)))
		}
	}

	b.Run(fmt.Sprintf("fork/routes=%d", len(gets)), func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, fctx := range gets {
				fork(fctx)
			}
		}
	})
	b.Run(fmt.Sprintf("upstream/routes=%d", len(gets)), func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, fctx := range gets {
				up(fctx)
			}
		}
	})
}

// scaleRoutes generates n distinct, conflict-free GET routes on one method
// stack — the worst case for the fork's per-Add conflict scan.
func scaleRoutes(n int) []string {
	routes := make([]string, n)
	for i := range routes {
		routes[i] = fmt.Sprintf("/v1/g%05d/resource/:id", i)
	}
	return routes
}

// Benchmark_Registration compares table-build cost. Upstream appends (O(n)); the
// fork inserts sorted with a conflict scan (O(n²)). ns/op is the one-time cost
// of building a whole table once — paid at startup, never per request.
func Benchmark_Registration(b *testing.B) {
	for _, n := range []int{10, 70, 500, 1000} {
		routes := scaleRoutes(n)

		b.Run(fmt.Sprintf("fork/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				app := forkfiber.New()
				for _, p := range routes {
					app.Add([]string{"GET"}, p, func(forkfiber.Ctx) error { return nil })
				}
			}
		})
		b.Run(fmt.Sprintf("upstream/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				app := upfiber.New()
				for _, p := range routes {
					app.Add([]string{"GET"}, p, func(upfiber.Ctx) error { return nil })
				}
			}
		})
	}
}

// Test_Comparison_Routing guards the comparison: the fork must route every
// representative request to the expected pattern under full dispatch. Handlers
// echo their registered pattern so a mis-route is caught before any perf number
// is trusted.
func Test_Comparison_Routing(t *testing.T) {
	app := forkfiber.New()
	for _, r := range cloudRoutes {
		pattern := r.path
		app.Add([]string{r.method}, r.path, func(c forkfiber.Ctx) error {
			return c.SendString(pattern)
		})
	}
	h := app.Handler()

	for _, req := range repRequests {
		fctx := newFctx(req.method, req.path)
		h(fctx)
		if got := string(fctx.Response.Body()); got != req.expect {
			t.Errorf("%s: %s %s routed to %q, want %q", req.name, req.method, req.path, got, req.expect)
		}
	}
}
