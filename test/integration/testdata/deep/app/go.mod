module example.com/deep/app

go 1.25

require (
	example.com/deep/a v0.0.0
	example.com/deep/b v0.0.0
	example.com/deep/c v0.0.0
)

require (
	example.com/deep/s v0.0.0 // indirect
	example.com/deep/t v0.0.0 // indirect
)

replace example.com/deep/a => ../a

replace example.com/deep/b => ../b

replace example.com/deep/c => ../c

replace example.com/deep/s => ../s

replace example.com/deep/t => ../t
