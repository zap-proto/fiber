// Benchmark-only module: isolates the upstream gofiber/fiber v3.2.0 dependency
// so the fork's own module (../) never takes a dep on the package it forked.
// Run: cd bench && go test -bench=. -benchmem -count=6 .
module github.com/zap-proto/fiber/bench

go 1.25.0

require (
	github.com/gofiber/fiber/v3 v3.2.0
	github.com/valyala/fasthttp v1.70.0
	github.com/zap-proto/fiber/v3 v3.2.1
)

require (
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/gofiber/schema v1.7.1 // indirect
	github.com/gofiber/utils/v2 v2.0.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.21 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
)

replace github.com/zap-proto/fiber/v3 => ../
