module example.com/e2e/app

go 1.25

require (
	example.com/e2e/liba v0.0.0
	example.com/e2e/libs v0.0.0
	example.com/e2e/libz v0.0.0
)

require (
	example.com/e2e/libb v0.0.0 // indirect
	example.com/e2e/libc v0.0.0 // indirect
)

replace example.com/e2e/liba => ../liba

replace example.com/e2e/libs => ../libs

replace example.com/e2e/libb => ../libb

replace example.com/e2e/libc => ../libc

replace example.com/e2e/libz => ../libz
