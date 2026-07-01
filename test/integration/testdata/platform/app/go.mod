module example.com/plat/app

go 1.25

require (
	example.com/plat/libcommon v0.0.0
	example.com/plat/libextra v0.0.0
	example.com/plat/liblinux v0.0.0
	example.com/plat/libwin v0.0.0
)

replace example.com/plat/libcommon => ../libcommon

replace example.com/plat/liblinux => ../liblinux

replace example.com/plat/libwin => ../libwin

replace example.com/plat/libextra => ../libextra
