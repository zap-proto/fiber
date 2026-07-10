# fiber

> **Docs:** [fiber](https://zap-proto.dev/docs/fiber) · part of the [ZAP Protocol](https://zap-proto.io)

**A fork of [gofiber/fiber](https://github.com/gofiber/fiber) v3** that adds one thing: **specificity-based route precedence, natively in the router.** The most specific pattern wins regardless of registration order — [ServeMux-1.22](https://pkg.go.dev/net/http#ServeMux) semantics. Everything else is upstream Fiber v3, unchanged.

Drop-in replacement for gofiber v3.2.0. Module path `github.com/zap-proto/fiber/v3`. [zip](https://github.com/zap-proto/zip) is its intended consumer.

```bash
go get github.com/zap-proto/fiber/v3
```

## Why fork

Upstream Fiber matches routes in **registration order** — the first route on the per-method stack that matches wins. That makes precedence depend on *when* a route was registered rather than *how specific* it is, so a wildcard registered first silently shadows a static sibling:

```go
app.Get("/v1/iam/*",    listAll)   // registered first — matches everything
app.Get("/v1/iam/keys", getKeys)   // registered second — never reached
```

Precedence should be a property of the **pattern**, not of registration order. This fork moves that rule into the router — the one place that already owns matching — so services never hand-order routes.

## What changes

The fork keeps Fiber's stack model but maintains each per-method stack in **most-specific-first** order via sorted insertion. Fiber's tree buckets preserve the stack's relative order and the matcher returns the first match in a bucket, so ordering the stack by specificity makes the most specific pattern win for every registration order — **with no changes to the matcher or the tree builder**.

The comparator mirrors Go 1.22 `net/http.ServeMux`:

- **static literal ≻ `:param` ≻ `*`/`+` wildcard**; more static structure wins; a deeper static route beats a shallower wildcard; the longer literal breaks a static tie.
- **Ambiguous overlap panics at registration.** Two *distinct* patterns of equal specificity (e.g. `/x/:id` vs `/x/:name`) have no winner — the fork panics loudly, naming both, exactly as ServeMux rejects two patterns when neither is more specific.
- **Exemptions, so it stays drop-in:** the *same* pattern re-registered keeps Fiber's handler merge (both run); different methods are independent; and **constraint-typed params** (`:id<int>` vs `:slug`) match disjoint value sets, so they are not a conflict and keep registration order.
- **Middleware keeps declaration order.** `use` and mounted sub-apps are precedence barriers; endpoints sort only within the run between barriers, so middleware always wraps the handlers declared after it. Routes added at runtime land in the correct position after `RebuildTree`.

```go
// Registration order no longer matters — the static route always wins.
app.Get("/v1/iam/*",    listAll)
app.Get("/v1/iam/keys", getKeys)
// GET /v1/iam/keys          -> getKeys
// GET /v1/iam/anything-else -> listAll
```

The change lives in [`router_precedence.go`](https://github.com/zap-proto/fiber/blob/main/router_precedence.go); the contract is pinned by [`router_precedence_test.go`](https://github.com/zap-proto/fiber/blob/main/router_precedence_test.go).

## Compatibility

Same package name (`fiber`), same public API, same `fiber.Ctx` / `fiber.Handler` / `fiber.Config` / middleware packages as gofiber v3.2.0. Swap the import path from `github.com/gofiber/fiber/v3` to `github.com/zap-proto/fiber/v3`. Released on semver tags tracking upstream (fork tagged `v3.2.1`).

The only behavior differences are the intended ones: correct precedence in place of registration-order shadowing, and a loud startup panic for genuinely ambiguous unconstrained patterns.

Full rationale and the comparator: **[zap-proto.dev/docs/fiber](https://zap-proto.dev/docs/fiber)**.

## License & attribution

MIT. This is a fork of **[gofiber/fiber](https://github.com/gofiber/fiber)** — the upstream license is carried forward unchanged:

> Copyright © 2019-present Fenny and Contributors

See [LICENSE](https://github.com/zap-proto/fiber/blob/main/LICENSE). All upstream credit belongs to the Fiber maintainers and contributors; this fork adds only the router-precedence layer described above.
