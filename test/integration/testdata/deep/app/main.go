package main

import (
	"fmt"

	"example.com/deep/a"
	"example.com/deep/b"
	"example.com/deep/c"
)

func main() {
	fmt.Println(a.Do())
	fmt.Println(b.Do())
	fmt.Println(c.Do())
}
