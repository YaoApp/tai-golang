//go:build wasip1

package main

//go:wasmimport tai_fetch fetch
func fetch(ptr, n uint32) uint64

func main() { _ = fetch(0, 0) }
