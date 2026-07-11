// ⚡️ Fiber is an Express inspired web framework written in Go with ☕️
// 🤖 GitHub Repository: https://github.com/gofiber/fiber
// 📌 API Documentation: https://docs.gofiber.io

package static

// Static-serving benchmarks for the embed.FS path: the ZAP web stack ships
// assets straight out of a compiled-in embed.FS (no disk touch), so this
// measures the middleware's per-request cost (path sanitize → open → detect
// content-type → stream) plus the fixed + per-byte serve cost for a small
// (1 KiB) and a medium (64 KiB) file.
//
// Served through app.Handler() on a reused *fasthttp.RequestCtx — the same
// low-overhead idiom as the router benchmarks — so the numbers are the serve
// path, not net/http↔fasthttp conversion.
//
// Run:
//
//	go test -run='^$' -bench='Benchmark_Static_Serve' -benchmem -count=6 ./middleware/static
//
// Numbers and methodology: BENCHMARKS.md (fiber).

import (
	"embed"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/zap-proto/fiber/v3"
)

//go:embed testdata/small.txt testdata/medium.txt
var benchFS embed.FS

var staticServeFiles = []struct {
	name string
	path string
	size int
}{
	{"small_1KiB", "/testdata/small.txt", 1024},
	{"medium_64KiB", "/testdata/medium.txt", 65536},
}

// Benchmark_Static_Serve serves a small and a medium file from embed.FS.
func Benchmark_Static_Serve(b *testing.B) {
	app := fiber.New()
	app.Get("/*", New("", Config{FS: benchFS}))
	h := app.Handler()

	for _, f := range staticServeFiles {
		b.Run(f.name, func(b *testing.B) {
			fctx := &fasthttp.RequestCtx{}
			fctx.Request.Header.SetMethod(fiber.MethodGet)
			fctx.URI().SetPath(f.path)

			// Correctness gate: 200 with the full file body.
			h(fctx)
			if sc := fctx.Response.StatusCode(); sc != fiber.StatusOK {
				b.Fatalf("%s: status %d, want 200", f.path, sc)
			}
			if got := len(fctx.Response.Body()); got != f.size {
				b.Fatalf("%s: served %d bytes, want %d", f.path, got, f.size)
			}

			b.SetBytes(int64(f.size))
			b.ReportAllocs()
			for b.Loop() {
				h(fctx)
			}
		})
	}
}
