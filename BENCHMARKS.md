# Benchmarks — zap-proto/fiber specificity-routing fork

Real numbers for the fork's routing, on the machine and Go version below. The
question this suite answers: **does the specificity-routing change (see
[`router_precedence.go`](router_precedence.go)) cost anything at match time?**
The short answer — proven three ways — is **no added cost in aggregate**, with a
per-route reshuffle and a quantified, one-time registration regression.

Everything here is reproducible from committed benchmarks. Where a number is
unflattering it is stated plainly; this file is evidence, not marketing.

## Environment

| | |
|---|---|
| CPU | Apple M1 Max (10 core) |
| RAM | 64 GB |
| OS | Darwin 25.5.0, arm64 (macOS) |
| Go | `go1.26.4 darwin/arm64` |
| Method | `-benchmem -benchtime=1s -count=6`, median reported via `benchstat` (`golang.org/x/perf`) |

`±` is the variation `benchstat` computes across the 6 runs. This is a laptop,
not an isolated benchmark host: **treat `sec/op` differences under ~5% as
noise.** Allocation counts (`allocs/op`) are exact and are the load-bearing
figures. Do not cherry-pick individual `sec/op` rows across separate runs.

## The route table

One representative **~70-route table** modelled on a Hanzo cloud gateway: deep
static literals, single/multi parameters, and greedy wildcards across
`iam / tracker / commerce / kms / cloud / registry / o11y` namespaces
(`cloudRoutes` in [`router_bench_test.go`](router_bench_test.go), duplicated in
[`bench/comparison_test.go`](bench/comparison_test.go)). It is conflict-free
under the fork's precedence rule, so it also registers cleanly on Go 1.22
`net/http.ServeMux` — which is both the fork's design oracle and a routing
baseline here.

Nine representative requests exercise every shape, including the three cases a
wildcard would win under registration-order routing but **must** lose under
specificity routing (`static_beats_wildcard`, `param_beats_wildcard`,
`wildcard` fall-through). `Test_Cloud_Precedence` and
`Test_Cloud_ServeMux_Precedence` assert the fork and ServeMux agree on all nine.

---

## 1. Match hot-path — fork vs upstream gofiber v3.2.0

Full request dispatch (`app.Handler()`: acquire ctx → route → run no-op handler
→ write 200), identical idiom for both, from [`bench/`](bench). **0 B, 0
allocs/op for every row, both implementations.**

| request → matched route | fork `ns/op` | upstream `ns/op` | Δ |
|---|--:|--:|--:|
| `static_deep` → `/v1/iam/oauth/userinfo` | **86.7** | 126.0 | **−31%** |
| `param_multi_deep` → `/v1/commerce/orders/:orderId/items/:itemId` | **176.3** | 257.3 | **−31%** |
| `param_beats_wildcard` → `/v1/registry/images/:name/tags` | **210.2** | 279.9 | **−25%** |
| `static_beats_wildcard` → `/v1/registry/images` | **173.7** | 222.6 | **−22%** |
| `root` → `/` | 44.2 | 44.7 | ≈0 |
| `wildcard` → `/v1/registry/*` | 276.0 | 269.8 | +2% |
| `static_shallow` → `/v1/health` | 261.2 | 243.0 | +7% |
| `param_mid` → `/v1/iam/organizations/:orgId/members` | 161.2 | 114.0 | +41% |
| `param_single` → `/v1/tracker/:id` | 336.5 | 141.2 | **+138%** |

**This is a permutation, not a regression.** fiber matches by scanning a
first-segment bucket linearly (see §5), so a route's latency is a function of its
**position** in that bucket. The fork orders the bucket most-specific-first;
upstream orders it by registration. Deep/specific routes move to the front and
get faster; shallow statics and short-prefix params (`/v1/tracker/:id`) move to
the back and get slower. Some rows win, some lose — by design.

The honest per-route caveat: **`/v1/tracker/:id` costs 336 ns under the fork vs
141 ns upstream** (still sub-µs, still 0-alloc). If a shallow param route is a
hot path, specificity ordering places it late in the linear scan. The fix is a
real matcher tree (§5), not a change to precedence.

## 2. The zero-cost proof — aggregate sweep

Because per-route cost is a permutation of scan positions, the **total** work to
match every route once is permutation-invariant. Dispatching all 50 GET routes
once per iteration ([`bench/`](bench) `Benchmark_Sweep`), **0 allocs**:

| implementation | `ns/op` (50 routes) | per route |
|---|--:|--:|
| **fork** | **10,770** ± 1% | 215 ns |
| upstream v3.2.0 | 10,960 ± 6% | 219 ns |

**Within 2% — the fork adds zero aggregate match cost.** The specificity model is
free at steady state; it only moves latency between routes.

## 3. Routing-only — fork vs Go 1.22 `net/http.ServeMux`

The fairest routing comparison: `app.next` (fiber's tree walk, no dispatch)
vs `mux.Handler(req)` (ServeMux lookup, no serving). Both stdlib-drivable, so
this lives in [`router_bench_test.go`](router_bench_test.go). Median of 6.

| request | fork `ns` | fork `allocs` | ServeMux `ns` | ServeMux `allocs` |
|---|--:|--:|--:|--:|
| `static_shallow` | 258.1 | **0** | 90.8 | 0 |
| `static_deep` | 80.9 | **0** | 162.0 | 0 |
| `param_single` | 332.2 | **0** | 162.7 | 1 |
| `param_mid` | 156.7 | **0** | 248.7 | 1 |
| `param_multi_deep` | 173.6 | **0** | 293.6 | 2 |
| `wildcard` | 269.5 | **0** | 419.3 | 5 |
| `static_beats_wildcard` | 171.6 | **0** | 129.3 | 0 |
| `param_beats_wildcard` | 206.2 | **0** | 222.4 | 1 |
| `root` | 38.6 | **0** | 36.8 | 0 |
| **sum of 9** | **1687** | **0** | 1766 | 10 |

Two true findings, opposite in sign:

- **fiber routing is allocation-free on every shape; ServeMux allocates on every
  dynamic route** (1 per param, 2 for multi-param, **5 for a `{rest...}`
  wildcard**). Under load this is fiber's clearest routing advantage.
- The cost profiles are **mirror images**: fiber's linear scan is cheapest for
  deep/specific routes (front of the specificity order) and dearest for shallow
  ones; ServeMux's tree descent is cheapest for shallow paths and dearest for
  deep/wildcard ones. On this representative mix they land within ~5% (fiber
  sum 1687 ns vs 1766 ns), so **the fork is competitive with the stdlib tree on
  time and strictly better on allocations.**

## 4. Registration cost — the honest O(n²)

The fork keeps the router stack most-specific-first via **sorted insertion**;
`insertRouteSorted` scans the method stack once per `Add` (merge/conflict check),
so building an N-route table is **O(n²)**. Measured on a single method stack (the
worst case), median of 6. `allocs/op` is **identical between fork and upstream** —
this is a CPU-time regression, not a memory one.

| N | fork `Add` | fork `Add`+build | upstream `Add` | ServeMux (eager) | allocs (fork≡upstream) |
|--:|--:|--:|--:|--:|--:|
| 10 | 7.2 µs | 11.3 µs | 6.2 µs | 17.6 µs | 143 |
| 70 | 74.5 µs | 106.6 µs | 36.2 µs | 120.1 µs | 866 |
| 500 | 2.41 ms | 3.41 ms | 245 µs | 869 µs | 6,028 |
| 1000 | **9.53 ms** | 13.55 ms | **503 µs** | 1.73 ms | 12,030 |

- **Per-route cost grows with N for the fork** (0.72 → 9.5 µs/route as N goes
  10 → 1000) — the signature of O(n²). Upstream is flat O(n) at ~0.5 µs/route;
  Go's ServeMux is also flat O(n) at ~1.73 µs/route (it conflict-checks via its
  index, not a linear scan).
- **At realistic scale it does not matter.** A service with ≤70 routes on a
  method builds its table in **<110 µs**, once, at startup. The 70-route
  `cloudRoutes` mix (spread across methods) is cheaper still.
- **At stress scale it is real.** 1000 routes on one method is **9.5 ms**
  (Add) / 13.6 ms (Add+tree) at startup — ~19× upstream and ~8× ServeMux. It is
  paid **zero times per request**, but it is the one axis where the fork loses,
  and it is fixable: adopt ServeMux's index-based conflict check to drop the
  per-`Add` linear scan and recover O(n).

## 5. Why match is O(bucket), not O(1) — a fiber-architecture note

The claim "match is an O(1) tree lookup" does **not** hold for a table where all
routes share a short leading prefix. fiber v3 buckets routes by the **first 3
characters of the leading static path** (`buildTree`). A Hanzo table is entirely
under `/v1/…`, so the first 3 chars are always `/v1` → **all 49 GET routes land
in one bucket**, which `app.next` scans **linearly**. Match cost is therefore
∝ the matched route's position in that bucket (e.g. `/v1/health` sits at index 46
→ 258 ns; `/v1/iam/oauth/userinfo` at index 5 → 81 ns).

This is a property of **upstream fiber's matcher**, unchanged by the fork — but
it is why the `/v1/` convention makes routing O(routes-per-method) rather than
O(1), and why the specificity order (which controls scan position) visibly moves
per-route latency. A genuine O(1) win would require replacing the linear
bucket scan with a segment tree; that is out of scope for a precedence change.

## 6. Static serving — embed.FS

File serve from a compiled-in `embed.FS` via `middleware/static`, full dispatch,
median of 6 ([`middleware/static/static_bench_test.go`](middleware/static/static_bench_test.go)):

| file | `ns/op` | `B/op` | `allocs/op` |
|---|--:|--:|--:|
| small (1 KiB) | 517 | 225 | 9 |
| medium (64 KiB) | 521 | 225 | 9 |

**Serving is size-independent** (~520 ns for both) with a **fixed 225 B / 9
allocs** regardless of file size: the embedded bytes are referenced into the
fasthttp response, not copied per request. The measured cost is the middleware
overhead (path sanitize → content-type detect → open), not byte movement. (The
implied "117 GiB/s" for the 64 KiB file is an artefact of that zero-copy body —
it is not a memory-bandwidth figure.)

## Verdict

| claim | verdict |
|---|---|
| Fork adds zero match cost vs upstream (aggregate) | **True** — sweep 10.77 µs vs 10.96 µs (§2) |
| Only registration changed; matcher untouched | **True** — per-route is a scan-order permutation (§1) |
| Match is O(1) tree lookup | **False for a shared-prefix table** — O(bucket), linear scan; a fiber trait, not the fork's (§5) |
| Registration is O(n²) | **True** — 9.5 ms at N=1000/method; <110 µs at N≤70; fixable (§4) |
| Routing beats stdlib ServeMux | **On allocations, yes** (0 vs 1–5/route); **on time, within ~5%** (§3) |

## Reproduce

```sh
# Fork internal: match, dispatch, registration, ServeMux baseline, correctness
go test -run='^$' -bench='Benchmark_Cloud' -benchmem -count=6 .
go test -run='Test_Cloud_Precedence|Test_Cloud_ServeMux_Precedence' -v .

# Static serving
go test -run='^$' -bench='Benchmark_Static_Serve' -benchmem -count=6 ./middleware/static

# Fork vs upstream gofiber v3.2.0 (separate module — isolates the upstream dep)
cd bench && go test -run='^$' -bench=. -benchmem -count=6 .

# Medians (requires golang.org/x/perf/cmd/benchstat)
go test -run='^$' -bench='Benchmark_Cloud' -benchmem -count=6 . | benchstat -
```

`bench/` is a **separate module** (its own `go.mod`, `replace ../`): the upstream
`gofiber/fiber/v3` dependency exists only there, never in the fork's module.
