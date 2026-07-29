module github.com/achird-labs/rift-go

// 1.24 is the real floor: testing.T.Context (1.24) is the newest thing used. Declaring the
// toolchain that happened to build it would force every consumer onto that exact version for no
// reason — a library should state what it needs, not what its author had installed.
go 1.24

require github.com/ebitengine/purego v0.10.2
