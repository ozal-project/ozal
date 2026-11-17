//go:build !unix

package main

// privateSocketUmask is a no-op where unix sockets are refused or have no
// file mode to protect; listenAdminSocket fails closed there.
func privateSocketUmask() func() { return func() {} }
