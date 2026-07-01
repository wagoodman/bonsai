module example.com/diff/app

go 1.25

require (
	example.com/diff/liba v0.0.0
	example.com/diff/libold v0.0.0
)

require example.com/diff/libc v0.0.0 // indirect

replace example.com/diff/liba => ../liba

replace example.com/diff/libc => ../libc

replace example.com/diff/libold => ../libold
