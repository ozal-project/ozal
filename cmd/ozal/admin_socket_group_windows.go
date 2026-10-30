package main

import "errors"

// shareAdminSocket is never reached on Windows: admin.socket is refused there.
func shareAdminSocket(path, group string) error {
	if group == "" {
		return nil
	}
	return errors.New("admin.socket_group is not supported on Windows")
}
