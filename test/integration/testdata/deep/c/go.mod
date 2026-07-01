module example.com/deep/c

go 1.21

require (
	example.com/deep/s v0.0.0
	example.com/deep/t v0.0.0
)

replace example.com/deep/s => ../s

replace example.com/deep/t => ../t
