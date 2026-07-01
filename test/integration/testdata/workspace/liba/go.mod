module example.com/e2e/liba

go 1.21

require (
	example.com/e2e/libb v0.0.0
	example.com/e2e/libc v0.0.0
)

replace example.com/e2e/libb => ../libb

replace example.com/e2e/libc => ../libc
