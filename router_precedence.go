// ⚡️ Fiber is an Express inspired web framework written in Go with ☕️
// 🤖 GitHub Repository: https://github.com/gofiber/fiber
// 📌 API Documentation: https://docs.gofiber.io

package fiber

import (
	"fmt"
	"slices"
	"strings"
)

// Specificity-based route precedence (fork addition).
//
// Upstream fiber matches routes in registration order: the first route on the
// per-method stack that matches an incoming path wins. That makes precedence a
// property of *when* a route was registered rather than *how specific* it is,
// so `/v1/iam/*` registered before `/v1/iam/keys` silently shadows the static
// handler.
//
// This fork keeps the stack model but maintains it in most-specific-first order
// via sorted insertion (see insertRouteSorted). Because fiber's tree buckets
// (App.buildTree) preserve the relative order of App.stack as sub-sequences and
// App.next returns the first matching route in a bucket, ordering the stack by
// specificity is sufficient to make the most specific pattern win for every
// registration order — startup or runtime-added — with no changes to the
// matcher or the tree builder.
//
// The comparator mirrors Go 1.22 net/http.ServeMux precedence semantics: a
// static literal is more specific than a named parameter, which is more
// specific than a greedy wildcard; more static structure wins; deeper static
// segments beat shallower wildcards; the longer literal breaks a static tie.
// Two DISTINCT patterns of equal specificity are an ambiguous conflict and
// panic at registration (see insertRouteSorted), just as ServeMux rejects
// overlapping patterns when neither is more specific. Re-registering the SAME
// pattern keeps fiber's documented handler merge (both handler sets run, so it
// never shadows).

const (
	specStatic   = 0 // constant literal segment
	specParam    = 1 // named parameter, e.g. :id
	specWildcard = 2 // greedy parameter, e.g. * or +
)

// segSpecificity ranks a single parsed segment. A lower rank is more specific.
func segSpecificity(s *routeSegment) int {
	switch {
	case !s.IsParam:
		return specStatic
	case s.IsGreedy:
		return specWildcard
	default:
		return specParam
	}
}

// compareRoutes orders two routes most-specific-first. It returns a negative
// value when a is more specific than b (a should be matched first), a positive
// value when b is more specific, and zero when the two patterns are
// structurally equivalent for routing purposes (they differ, at most, in
// parameter names). A zero result leaves the two routes in registration order.
func compareRoutes(a, b *Route) int {
	as, bs := a.routeParser.segs, b.routeParser.segs

	for i := 0; i < len(as) && i < len(bs); i++ {
		ra, rb := segSpecificity(as[i]), segSpecificity(bs[i])
		if ra != rb {
			// static (0) < param (1) < wildcard (2): lower rank matches first.
			return ra - rb
		}

		// Two literal segments at the same position: the longer literal is more
		// specific (it constrains more of the path, and a shared prefix leading
		// into a parameter is always shorter than the fully static sibling).
		// Equal-length literals are ordered lexicographically for determinism.
		if ra == specStatic && as[i].Const != bs[i].Const {
			if len(as[i].Const) != len(bs[i].Const) {
				return len(bs[i].Const) - len(as[i].Const)
			}
			return strings.Compare(as[i].Const, bs[i].Const)
		}
	}

	// One pattern is a structural prefix of the other: more segments means more
	// static/parameter structure to satisfy, which is more specific.
	if len(as) != len(bs) {
		return len(bs) - len(as)
	}

	// Structurally identical (differ only in parameter names). Preserve
	// registration order.
	return 0
}

// paramConstrained reports whether the route has any parameter segment carrying
// a user constraint (e.g. :id<int>). Two constrained params at the same position
// can match disjoint value sets, so they are not treated as an ambiguous
// conflict.
func paramConstrained(r *Route) bool {
	for _, s := range r.routeParser.segs {
		if s.IsParam && len(s.Constraints) > 0 {
			return true
		}
	}
	return false
}

// sortedInsertIndex returns the index in stack at which route should be inserted
// so the stack stays most-specific-first within route's middleware context.
//
// Middleware (use) and mounted routes are precedence barriers: their relative
// order is meaningful and must be preserved, so an endpoint is never reordered
// across one. Insertion is therefore bounded to the run of endpoints that
// follows the last barrier, keeping middleware-before-handler semantics intact.
func sortedInsertIndex(stack []*Route, route *Route) int {
	lower := 0
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].use || stack[i].mount {
			lower = i + 1
			break
		}
	}

	for i := lower; i < len(stack); i++ {
		if compareRoutes(route, stack[i]) < 0 {
			return i
		}
	}
	return len(stack)
}

// insertRouteSorted inserts an endpoint route into app.stack[m] in specificity
// order, resolving three cases against the existing endpoint routes:
//
//   - Identical pattern (same normalized path): fiber's documented handler
//     merge — the new handlers are appended to the existing route. Both handler
//     sets run in sequence, so this is not shadowing; it is how app.All after a
//     specific method, or repeated registration of a path, chains handlers.
//
//   - Ambiguous conflict (a distinct pattern of equal specificity, e.g.
//     /users/:id vs /users/:name): the two patterns overlap and neither is more
//     specific, so one would silently shadow the other after sorting. This
//     panics loudly, naming both patterns — mirroring Go 1.22 net/http.ServeMux,
//     which rejects two patterns when neither is more specific than the other.
//
//   - Otherwise: the route is inserted at its most-specific-first position.
//
// Mechanically generated routes (auto-HEAD mirrors, mounted sub-apps) are exempt
// from both checks; they are not user registrations. The caller must already
// hold app.mutex.
func (app *App) insertRouteSorted(m int, route *Route) {
	stack := app.stack[m]

	for _, existing := range stack {
		if existing.use || existing.mount || existing.autoHead {
			continue
		}
		if existing.path == route.path {
			// Identical pattern: merge handlers into the existing route.
			existing.Handlers = append(existing.Handlers, route.Handlers...)
			return
		}
		if compareRoutes(route, existing) == 0 && !paramConstrained(route) && !paramConstrained(existing) {
			// Distinct patterns, equal specificity, unconstrained: ambiguous
			// overlap. Constrained params (e.g. :id<int>) can be mutually
			// exclusive, so they are exempt — they fall through to sorted
			// insertion and keep registration order, matching upstream fiber's
			// constraint-routing behavior.
			panic(fmt.Sprintf(
				"fiber: route conflict: %s %s conflicts with %s %s (equal specificity, ambiguous match)",
				route.Method, route.Path, existing.Method, existing.Path,
			))
		}
	}

	app.stack[m] = slices.Insert(stack, sortedInsertIndex(stack, route), route)
	app.routesRefreshed = true
}
