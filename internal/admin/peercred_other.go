//go:build !linux

package admin

import "net"

// peerUID is only implemented on Linux; elsewhere socket requests stay "local".
func peerUID(*net.UnixConn) (int, bool) { return 0, false }
